package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	Register(cargoAdapter{})
}

// cargoAdapter is the Adapter for Rust crates, run through cargo. It is the
// proven top context-injector among language tools — an unfiltered cargo
// run can emit thousands of lines — so its Parse is the one every other
// adapter's cap-and-log behavior (run.go) exists to bound. build, format,
// lint and vet each spawn one cargo subcommand and take the subprocess
// route, letting Parse alone read its output. security and test do not:
// security fronts two subcommands answering different questions (OD46) —
// cargo-audit checks the resolved lockfile against known advisories,
// cargo-deny checks license/ban/source policy — and test's subcommand
// depends on target.Test, which Command's per-check signature can never
// see. Both route in-process, spawning cargo themselves.
//
// Every subcommand this adapter names — audit, bench, check, clippy, deny,
// fmt, llvm-cov, nextest — is a plugin cargo itself dispatches to (a
// `cargo-<name>` executable resolved from PATH), so the one binary ever
// spawned directly here is cargo; Tool answers "cargo" for every check on
// that basis, unlike a language whose lint and its import organiser are two
// distinct top-level binaries.
type cargoAdapter struct{}

func (cargoAdapter) Language() string { return "rust" }

// Route reports the subprocess route for build, format, lint and vet, and
// the in-process route for security and test — see the cargoAdapter doc for
// why those two need it.
func (cargoAdapter) Route(check Check) Route {
	switch check {
	case CheckBuild, CheckFormat, CheckLint, CheckVet:
		return RouteSubprocess
	default:
		return RouteInProcess
	}
}

func (cargoAdapter) Tool(check Check) string { return "cargo" }

// RunInProcess performs target's check by spawning the cargo subcommand(s)
// it needs and merging their normalized diagnostics. build, format, lint and
// vet are unreachable here, since Route sends them through the subprocess
// path instead. A returned error means a subcommand could not even be
// started or waited on — the same infrastructure-failure contract Run's own
// subprocess path upholds; a subcommand that ran and reported findings is
// always a Diagnostic, never an error.
func (a cargoAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Check {
	case CheckSecurity:
		return a.runSecurity(ctx, target)
	case CheckTest:
		return a.runTest(ctx, target)
	default:
		return nil, errUnsupportedCheck("cargo", target.Check)
	}
}

// Command returns cargo's argv for check. Only build, format, lint and vet
// reach it through Run — security and test route in-process (Route above)
// and Run never calls it for them, though a direct caller still gets
// ErrUnsupportedCheck rather than a silent nil for either. build, lint and
// vet all pass --locked, so a run fails on a stale Cargo.lock rather than
// silently re-resolving it; format never does — cargo fmt forwards
// unrecognized options straight to rustfmt, and rustfmt has no --locked, so
// passing it there fails the check itself at exit 2 before rustfmt ever
// inspects a file. build, lint and vet also request --message-format=json,
// giving Parse the compiler's own structured diagnostics rather than its
// human-rendered text. vet runs `cargo check` rather than a second linter:
// clippy's own --all-targets --all-features pass already denies the
// compiler's own suite alongside its lints (F48), so vet exists only to give
// that compiler pass its own pair, widened to lint's target/feature surface
// rather than build's narrower default — build alone would leave test and
// bench code unchecked. build's profile (e.g. "--release") and cross-compile
// target triple (e.g. "--target", "x86_64-unknown-linux-gnu") are never
// named here — Run appends target.Args, which carries exactly those, to this
// argv's end. A cross-compile's linker comes from whatever
// CARGO_TARGET_<triple>_LINKER (or an equivalent .cargo/config.toml) is
// already set in the environment cargo inherits; this adapter reads none of
// that and sets no linker variable of its own.
func (cargoAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckBuild:
		return []string{"build", "--locked", "--message-format=json"}, nil
	case CheckVet:
		return []string{"check", "--locked", "--message-format=json", "--all-targets", "--all-features"}, nil
	case CheckLint:
		return []string{"clippy", "--locked", "--message-format=json", "--all-targets", "--all-features"}, nil
	case CheckFormat:
		return []string{"fmt", "--check"}, nil
	default:
		return nil, errUnsupportedCheck("cargo", check)
	}
}

// cargoMessage is the subset of cargo's --message-format=json event shape
// (https://doc.rust-lang.org/cargo/reference/external-tools.html) Parse
// needs: a compiler diagnostic's level, text, optional lint code, and the
// file/line of its primary span.
type cargoMessage struct {
	Reason  string `json:"reason"`
	Message struct {
		Message string `json:"message"`
		Level   string `json:"level"`
		Code    *struct {
			Code string `json:"code"`
		} `json:"code"`
		Spans []struct {
			FileName  string `json:"file_name"`
			LineStart int    `json:"line_start"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"spans"`
	} `json:"message"`
}

// cargoTestFailureRE matches libtest's plain-text per-test failure line
// (`test <path::to::test> ... FAILED`), the one result cargo test does not
// carry in its JSON stream on the stable toolchain.
var cargoTestFailureRE = regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`)

// Parse reads cargo's stdout line by line: each line is either a
// --message-format=json event or, during a test run, one of the test
// harness's own plain-text lines. A line that is neither yields nothing —
// cargo's build progress and summary lines are expected noise, not an
// unparseable-output error. cargo fmt --check carries neither shape: it
// prints a unified diff per unformatted file and nothing else, so a failing
// format check falls through to the synthetic exit-code diagnostic below
// rather than a per-file one — the diff's presence in the log the run writes
// is what a reader consults for which file and what changed.
func (cargoAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	var diags []Diagnostic
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg cargoMessage
		if err := json.Unmarshal(line, &msg); err == nil && msg.Reason == "compiler-message" {
			if d, ok := diagnosticFromCargoMessage(msg); ok {
				diags = append(diags, d)
			}
			continue
		}
		if m := cargoTestFailureRE.FindStringSubmatch(string(line)); m != nil {
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("test failed: %s", m[1])})
		}
	}
	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("cargo exited %d with no parsed diagnostics; see log_ref for raw output", exitCode),
		})
	}
	return diags, nil
}

// diagnosticFromCargoMessage converts one compiler-message event into a
// Diagnostic, or reports false for a level Parse doesn't count (cargo also
// emits "note", "help" and "failure-note" as their own top-level events for
// a diagnostic's children — only its own error/warning level is counted
// once).
func diagnosticFromCargoMessage(msg cargoMessage) (Diagnostic, bool) {
	var severity Severity
	switch msg.Message.Level {
	case "error":
		severity = SeverityError
	case "warning":
		severity = SeverityWarning
	default:
		return Diagnostic{}, false
	}
	d := Diagnostic{Severity: severity, Message: msg.Message.Message}
	if msg.Message.Code != nil {
		d.Code = msg.Message.Code.Code
	}
	for _, span := range msg.Message.Spans {
		if span.IsPrimary {
			d.File = span.FileName
			d.Line = span.LineStart
			break
		}
	}
	return d, true
}

// Subcommand labels used only in a diagnostic's message and in the
// fallback synthetic diagnostic (fallbackDiagnostic, defined once in
// golang.go and shared package-wide): every one of them still spawns
// through the single "cargo" binary Tool names for every check, so these
// exist only to tell a reader which subcommand raised a given finding when
// a check runs more than one in the same pass.
const (
	cargoAuditTool   = "cargo audit"
	cargoDenyTool    = "cargo deny"
	cargoNextestTool = "cargo nextest"
	cargoLlvmCovTool = "cargo llvm-cov nextest"
)

// cargoCoverageFile is the fixed name runUnitTest writes its lcov coverage
// report to, inside target.Dir — the same place a caller running cargo
// llvm-cov by hand there would leave it. Coverage rides the unit-test run
// itself rather than a second invocation, mirroring Go's gotestsum wrapper
// (OD50).
const cargoCoverageFile = "lcov.info"

// runSecurity runs cargo-audit and cargo-deny against target.Dir and merges
// their findings (OD46): cargo-audit checks the resolved lockfile against
// RustSec's advisory database, cargo-deny checks license, ban and
// source-registry policy — two different questions neither tool answers
// alone. Both are cargo plugins (cargo-audit, cargo-deny resolved from
// PATH), spawned through cargo exactly like every other check here — a
// provisioned binary, never a Cargo.toml dependency.
func (cargoAdapter) runSecurity(ctx context.Context, target Target) ([]Diagnostic, error) {
	var diags []Diagnostic

	auditRes, err := runTool(ctx, target.Dir, "cargo", []string{"audit", "--json"})
	if err != nil {
		return nil, err
	}
	auditDiags := parseCargoAuditJSON(auditRes.Stdout)
	if len(auditDiags) == 0 && auditRes.ExitCode != 0 {
		auditDiags = append(auditDiags, fallbackDiagnostic(cargoAuditTool, auditRes.ExitCode))
	}
	diags = append(diags, auditDiags...)

	denyRes, err := runTool(ctx, target.Dir, "cargo", []string{"deny", "--format=json", "check"})
	if err != nil {
		return nil, err
	}
	denyDiags := parseCargoDenyJSON(denyRes.Stdout)
	if len(denyDiags) == 0 && denyRes.ExitCode != 0 {
		denyDiags = append(denyDiags, fallbackDiagnostic(cargoDenyTool, denyRes.ExitCode))
	}
	diags = append(diags, denyDiags...)

	return diags, nil
}

// runTest dispatches on target.Test — the one piece of information Command
// can never see, since its signature carries only the check — which is why
// the whole test pair routes in-process even though its e2e kind alone
// spawns a single subcommand.
func (a cargoAdapter) runTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Test {
	case TestUnit:
		return a.runUnitTest(ctx, target)
	case TestE2E:
		return a.runE2ETest(ctx, target)
	case TestBenchmark:
		return a.runBenchmarkTest(ctx, target)
	default:
		return nil, errUnsupportedCheck("cargo", target.Check)
	}
}

// runUnitTest runs the unit-test pair through `cargo llvm-cov nextest`,
// which wraps cargo-nextest's own run and produces an lcov coverage report
// (cargoCoverageFile) in this one invocation rather than a second one —
// coverage rides the same run as the tests it measures, never a pair of its
// own. The filterset `not binary(e2e)` is the exact complement of
// runE2ETest's `binary(e2e)`, so the two test pairs partition the crate's
// binaries: the unit pair runs everything except the e2e integration binary,
// which the e2e pair owns. --ignore-run-fail is required to keep coverage
// riding the same run: cargo-llvm-cov writes no report when a wrapped test
// fails unless told to ignore the run's failure, and that flag also forces
// its own exit to 0 — so a failing test is detected not from the exit code
// but from the FAIL lines nextest still prints, which parseCargoNextestFailures
// turns into error diagnostics; a compile failure (which prints no FAIL line
// and still exits non-zero even under --ignore-run-fail) falls through to the
// exit-code fallback. assert_cmd and criterion are dev-dependencies a crate
// under test declares in its own Cargo.toml, not binaries this adapter ever
// spawns — a library resolved that way is never counted among the binaries a
// check needs provisioned.
func (cargoAdapter) runUnitTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "cargo", []string{
		"llvm-cov", "nextest", "--locked", "--all-features", "--ignore-run-fail",
		"-E", "not binary(e2e)",
		"--lcov", "--output-path", filepath.Join(target.Dir, cargoCoverageFile),
	})
	if err != nil {
		return nil, err
	}
	diags := parseCargoNextestFailures(res.Stderr)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(cargoLlvmCovTool, res.ExitCode))
	}
	return diags, nil
}

// runE2ETest runs the e2e-test pair through plain `cargo nextest run`,
// filtered to the binary named "e2e" via nextest's own filterset DSL: an
// integration-test file (tests/e2e.rs) is already its own nextest binary by
// Cargo's convention, so this names no file path and no platform, and stays
// dispatchable on every fleet target rather than assuming one host
// architecture. assert_cmd, which that binary uses to drive the crate's own
// CLI, is a dev-dependency exercised inside it — again never a binary this
// adapter spawns.
func (cargoAdapter) runE2ETest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "cargo", []string{"nextest", "run", "--locked", "-E", "binary(e2e)"})
	if err != nil {
		return nil, err
	}
	diags := parseCargoNextestFailures(res.Stderr)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(cargoNextestTool, res.ExitCode))
	}
	return diags, nil
}

// runBenchmarkTest runs the benchmark pair through `cargo bench`, which
// builds and runs a crate's benches/ targets — each a criterion-driven
// binary the crate's own Cargo.toml wires with `harness = false`, again a
// dev-dependency rather than a spawned binary. --message-format=json
// surfaces a build failure the same structured way build and vet do, so
// this reuses Parse rather than a second JSON reader; a benchmark carries no
// pass/fail status of its own, so a run that compiles and executes cleanly
// reports nothing further, and one that panics falls through to Parse's own
// exit-code fallback.
func (a cargoAdapter) runBenchmarkTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "cargo", []string{"bench", "--locked", "--message-format=json"})
	if err != nil {
		return nil, err
	}
	return a.Parse(res.ExitCode, res.Stdout, res.Stderr)
}

// cargoNextestFailureRE matches one line of cargo-nextest's default human
// summary for a failed test: a status word (FAIL, or "TRY n FAIL" after a
// retry), its duration, the "(passed/total)" progress counter nextest prints
// between the duration and the binary ID, then the binary ID and the test
// name — nextest's own shape, distinct from the libtest "test <path> ...
// FAILED" line cargoTestFailureRE matches for a plain `cargo test` run. The
// counter is optional so a future or configured reporter that omits it still
// matches.
var cargoNextestFailureRE = regexp.MustCompile(`^(?:TRY \d+ )?FAIL\s+\[\s*[\d.]+s\]\s+(?:\(\d+/\d+\)\s+)?(\S+)\s+(\S+)$`)

// parseCargoNextestFailures turns cargo-nextest's FAIL lines into one
// diagnostic per failing test, naming the binary and test nextest itself
// reports rather than a bare test path. nextest writes this human summary to
// stderr, not stdout, so callers pass res.Stderr. nextest prints a failed
// test's FAIL line twice on a cancelled run — once when it happens and once
// in the final summary — so identical failures are collapsed to one
// diagnostic.
func parseCargoNextestFailures(stdout []byte) []Diagnostic {
	var diags []Diagnostic
	seen := map[string]bool{}
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		m := cargoNextestFailureRE.FindSubmatch(line)
		if m == nil {
			continue
		}
		msg := fmt.Sprintf("test failed: %s %s", string(m[1]), string(m[2]))
		if seen[msg] {
			continue
		}
		seen[msg] = true
		diags = append(diags, Diagnostic{Severity: SeverityError, Message: msg})
	}
	return diags
}

// cargoAuditReport is the subset of `cargo audit --json`'s shape this
// adapter needs: one finding per known-vulnerable dependency actually
// resolved into the lockfile.
type cargoAuditReport struct {
	Vulnerabilities struct {
		List []struct {
			Advisory struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"advisory"`
			Package struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"package"`
		} `json:"list"`
	} `json:"vulnerabilities"`
}

// parseCargoAuditJSON turns one cargo-audit report into one error diagnostic
// per vulnerable package — cargo-audit reports only a finding, never a
// severity grade of its own, so every one counts as an error.
func parseCargoAuditJSON(stdout []byte) []Diagnostic {
	var report cargoAuditReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Vulnerabilities.List))
	for _, v := range report.Vulnerabilities.List {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Code:     v.Advisory.ID,
			Message:  fmt.Sprintf("%s: %s (%s@%s)", cargoAuditTool, v.Advisory.Title, v.Package.Name, v.Package.Version),
		})
	}
	return diags
}

// cargoDenyDiagnostic is one line of `cargo deny --format=json check`'s
// NDJSON stream: cargo-deny reports every finding — a banned crate, a
// license mismatch, an advisory, an untrusted source — as one line of this
// shape, regardless of which of its four check kinds raised it.
type cargoDenyDiagnostic struct {
	Type   string `json:"type"`
	Fields struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"fields"`
}

// parseCargoDenyJSON turns cargo-deny's NDJSON stream into diagnostics. A
// line whose severity is neither "error" nor "warning" (e.g. "note",
// "help") is a finding's own child rather than a top-level one, exactly
// like the compiler-message levels Parse itself skips, and is not counted.
func parseCargoDenyJSON(stdout []byte) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var d cargoDenyDiagnostic
		if err := json.Unmarshal(line, &d); err != nil || d.Type != "diagnostic" {
			continue
		}
		var severity Severity
		switch {
		case strings.EqualFold(d.Fields.Severity, "error"):
			severity = SeverityError
		case strings.EqualFold(d.Fields.Severity, "warning"):
			severity = SeverityWarning
		default:
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Message:  fmt.Sprintf("%s: %s", cargoDenyTool, d.Fields.Message),
		})
	}
	return diags
}
