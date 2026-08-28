package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

func init() {
	Register(pythonAdapter{})
}

// pythonAdapter is the Adapter for Python projects, fronting five tools: uv
// for build and to run pytest, ruff for lint and format, mypy for vet, and
// bandit for security. build, lint, format and vet each spawn one tool and
// take the subprocess route, letting Parse alone read their output. security
// and test do not: security's bandit reports a single JSON document rather
// than the per-line shapes Parse already reads, and test's tool set depends
// on target.Test — unit adds coverage to the same pytest run, e2e adds
// Playwright — which Command's per-check signature can never see. Both route
// in-process, spawning what they need themselves.
//
// ruff, mypy and bandit run as toolchain-pinned binaries directly on PATH,
// exactly like Go's golangci-lint or Rust's clippy — never through `uv run`.
// pytest is the one exception: it is never installed as a toolchain pin, it
// arrives through the project's own dependency group (e.g.
// dependency-groups.dev), and `uv run` is what resolves and activates that
// project's environment before invoking it — a bare `pytest` on PATH would
// depend on whatever happened to be installed globally instead.
type pythonAdapter struct{}

func (pythonAdapter) Language() string { return "python" }

// Route reports the subprocess route for build, format, lint and vet, and
// the in-process route for security and test — see the pythonAdapter doc for
// why those two need it.
func (pythonAdapter) Route(check Check) Route {
	switch check {
	case CheckSecurity, CheckTest:
		return RouteInProcess
	default:
		return RouteSubprocess
	}
}

// Tool names ruff for lint and format, mypy for vet, bandit for security,
// pytest for test — the one tool both test kinds share, mirroring Go's
// testResultTool: Tool's per-check signature (no target.Test) can't name
// e2e's added Playwright dependency separately — and uv for build.
func (pythonAdapter) Tool(check Check) string {
	switch check {
	case CheckLint, CheckFormat:
		return "ruff"
	case CheckVet:
		return "mypy"
	case CheckSecurity:
		return "bandit"
	case CheckTest:
		return "pytest"
	default:
		return "uv"
	}
}

// mypyConfigDir and mypyConfigBase name the fixed language-tools config file
// vet's mypy invocation reads (SC37: every tool config lives in
// language-tools, never a caller's). mypyConfigPath materializes it under a
// private per-process temp directory rather than target.Dir, so it can never
// collide with, or be shadowed by, a caller's own mypy.ini or pyproject.toml
// [tool.mypy] section. The language-tools dir nests under a unique parent
// (mypyConfigParent) rather than sitting directly under os.TempDir(), because
// "language-tools" is exactly the fleet CLI's own binary name and a build of
// it commonly already occupies os.TempDir()/language-tools as a plain file —
// MkdirAll on that path would fail ENOTDIR and break every vet run.
const (
	mypyConfigDir  = "language-tools"
	mypyConfigBase = "mypy.ini"
)

// mypyDefaultConfig is the language-tools mypy configuration: permissive
// enough to run against a project with no type stubs of its own
// (ignore_missing_imports), while still catching a body mypy would otherwise
// skip entirely on an untyped signature (check_untyped_defs).
const mypyDefaultConfig = `[mypy]
ignore_missing_imports = True
check_untyped_defs = True
warn_redundant_casts = True
warn_unused_ignores = True
`

// mypyConfigParent resolves, once per process, a unique private parent
// directory under os.TempDir() to hold the language-tools config tree.
// os.MkdirTemp yields a name that cannot collide with a pre-existing file
// (the fleet CLI's own language-tools binary among them) and creates it 0700,
// so a co-tenant on a shared /tmp can neither pre-seed nor read it. The
// result is memoized: the parent is stable for the process, while
// mypyConfigPath still rewrites the config file itself on every call.
var (
	mypyParentOnce sync.Once
	mypyParentDir  string
	mypyParentErr  error
)

func mypyConfigParent() (string, error) {
	mypyParentOnce.Do(func() {
		mypyParentDir, mypyParentErr = os.MkdirTemp("", "claude-toolchain-")
	})
	return mypyParentDir, mypyParentErr
}

// mypyConfigPath writes mypyDefaultConfig to its fixed path and returns it.
// MkdirAll re-creates the language-tools subdir on every call, so the config
// self-heals even if a temp reaper removes the parent mid-process; the write
// is a few hundred bytes and Command runs once per check invocation, never in
// a hot loop.
func mypyConfigPath() (string, error) {
	parent, err := mypyConfigParent()
	if err != nil {
		return "", fmt.Errorf("toolchain: create language-tools config parent: %w", err)
	}
	dir := filepath.Join(parent, mypyConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("toolchain: create language-tools config dir: %w", err)
	}
	path := filepath.Join(dir, mypyConfigBase)
	if err := os.WriteFile(path, []byte(mypyDefaultConfig), 0o644); err != nil {
		return "", fmt.Errorf("toolchain: write mypy config: %w", err)
	}
	return path, nil
}

// Command returns the argv for build, format, lint and vet — the four
// subprocess-routed checks; security and test route in-process (Route above)
// and Run never calls it for them, though a direct caller still gets
// ErrUnsupportedCheck rather than a silent nil. build runs `uv build`, which
// produces both a wheel and a source distribution into dist/ per MATRIX —
// unlike cargo build or go build, packaging a wheel/sdist reads only
// pyproject.toml and the source tree, never the project's own resolved
// dependency graph, so uv build takes no lock-file flag to pass in the first
// place. lint runs `ruff check`; format runs `ruff format --check`, never
// bare `ruff format`, because without --check ruff format rewrites every
// unformatted file in place rather than just reporting which ones need it.
// vet runs mypy against target.Dir with --config-file pointed at the
// language-tools config (SC37): passing --config-file explicitly makes mypy
// read only that file, skipping its own upward search for a caller's
// mypy.ini, setup.cfg or pyproject.toml [tool.mypy] section entirely.
func (a pythonAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckBuild:
		return []string{"build"}, nil
	case CheckLint:
		return []string{"check"}, nil
	case CheckFormat:
		return []string{"format", "--check"}, nil
	case CheckVet:
		cfg, err := mypyConfigPath()
		if err != nil {
			return nil, err
		}
		return []string{"--config-file", cfg, "."}, nil
	default:
		return nil, errUnsupportedCheck(a.Tool(check), check)
	}
}

// RunInProcess performs target's check by spawning the tool(s) it needs and
// merging their normalized diagnostics. build, format, lint and vet are
// unreachable here, since Route sends them through the subprocess path
// instead. A returned error means a tool could not even be started or waited
// on — the same infrastructure-failure contract Run's own subprocess path
// upholds; a tool that ran and reported findings is always a Diagnostic,
// never an error.
func (a pythonAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Check {
	case CheckSecurity:
		return a.runSecurity(ctx, target)
	case CheckTest:
		return a.runTest(ctx, target)
	default:
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
}

// banditExcludeDirs lists the paths bandit's -x skips: a project's own
// virtual environment, VCS metadata, build output, the cache directories
// ruff, mypy and pytest each leave behind, and the test tree itself. Without
// the first group, -r's recursion walks straight into .venv and reports
// every installed third-party package's own findings as if they belonged to
// the project under test. Without the last, bandit's B101 fires on every
// bare `assert` a pytest suite uses by convention — a false positive on the
// test framework's own idiom, not a finding about the shipped code security
// is meant to gate.
const banditExcludeDirs = "./.venv,./venv,./.git,./build,./dist,./.mypy_cache,./.pytest_cache,./.ruff_cache,./__pycache__,./tests,./test"

// runSecurity runs bandit against target.Dir and turns its JSON report into
// diagnostics. -r recurses the project tree (skipping banditExcludeDirs);
// -f json gives Parse-equivalent structure rather than bandit's
// human-rendered text.
func (pythonAdapter) runSecurity(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "bandit", []string{"-r", "-f", "json", "-x", banditExcludeDirs, "."})
	if err != nil {
		return nil, err
	}
	diags := parseBanditJSON(res.Stdout)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic("bandit", res.ExitCode))
	}
	return diags, nil
}

// runTest dispatches on target.Test — the one piece of information Command
// and Tool can never see, since their signature carries only the check —
// which is why the test pair routes in-process even though its e2e kind
// alone spawns pytest with no extra flag of its own.
func (a pythonAdapter) runTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Test {
	case TestUnit:
		return a.runUnitTest(ctx, target)
	case TestE2E:
		return a.runE2ETest(ctx, target)
	default:
		return nil, errUnsupportedCheck("pytest", target.Check)
	}
}

// pythonCoverageFile is the fixed name runUnitTest writes its coverage
// report to, inside target.Dir — the same place a caller running pytest by
// hand there would leave it.
const pythonCoverageFile = "coverage.xml"

// runUnitTest runs the unit-test pair through `uv run pytest`, adding
// coverage in this one invocation rather than a second one (pytest-cov,
// resolved the same way pytest itself is — a project dev dependency, never a
// toolchain pin) — mirroring Go's gotestsum wrapper and Rust's cargo-llvm-cov
// nextest (OD50). `-m "not e2e"` excludes the e2e-marked suite runE2ETest
// owns, the exact complement of its own `-m e2e`, so the two test pairs
// partition the project's tests.
func (pythonAdapter) runUnitTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "uv", []string{
		"run", "pytest", "-m", "not e2e",
		"--cov=.", "--cov-report=xml:" + filepath.Join(target.Dir, pythonCoverageFile),
	})
	if err != nil {
		return nil, err
	}
	diags := parsePytestFailures(res.Stdout, res.Stderr)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic("pytest", res.ExitCode))
	}
	return diags, nil
}

// runE2ETest runs the e2e-test pair through `uv run pytest -m e2e`: an
// e2e-marked test imports pytest-playwright's fixtures, which drive an
// actual Chromium instance rather than anything this adapter spawns itself.
// Per OD61, Playwright's Python distribution ships its own Chromium, unlike
// Go's chromedp (which needs an ambient Chrome the CI template installs
// separately) — so no browser-install step belongs here or in the caller
// that invokes this check.
func (pythonAdapter) runE2ETest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "uv", []string{"run", "pytest", "-m", "e2e"})
	if err != nil {
		return nil, err
	}
	diags := parsePytestFailures(res.Stdout, res.Stderr)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic("pytest", res.ExitCode))
	}
	return diags, nil
}

// pytestFailureRE matches one line of pytest's short test summary info (the
// section after "===... short test summary info ...===="): a failed test's
// node ID and, optionally, the one-line reason pytest printed after the
// dash. This is the one line in pytest's default output that names a
// specific failing test without also carrying a full traceback for a parser
// to pick apart.
var pytestFailureRE = regexp.MustCompile(`^FAILED (\S+)(?: - (.*))?$`)

// parsePytestFailures turns pytest's short-summary FAILED lines into one
// diagnostic per failing test. It reads both streams since pytest's own
// destination for that summary is stdout by default, but a caller's
// conftest.py can redirect logging output that interleaves with it.
func parsePytestFailures(stdout, stderr []byte) []Diagnostic {
	var diags []Diagnostic
	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			trimmed := bytes.TrimSpace(raw)
			m := pytestFailureRE.FindStringSubmatch(string(trimmed))
			if m == nil {
				continue
			}
			msg := "test failed: " + m[1]
			if m[2] != "" {
				msg = fmt.Sprintf("test failed: %s - %s", m[1], m[2])
			}
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: msg})
		}
	}
	return diags
}

// ruffRuleHeaderRE matches the first line of one `ruff check` violation
// block: a rule code, an optional "[*]" marking it fixable, and the
// violation's message.
var ruffRuleHeaderRE = regexp.MustCompile(`^([A-Z]+[0-9]+) (?:\[\*\] )?(.+)$`)

// ruffFormatHeaderRE matches the first line of one `ruff format --check`
// violation block. ruff names every such block "unformatted" regardless of
// what changed, so the block carries no rule code the way a lint violation
// does.
var ruffFormatHeaderRE = regexp.MustCompile(`^unformatted: (.+)$`)

// ruffLocationRE matches the second line of either ruff violation block: the
// file/line/column the block's header describes, e.g. " --> src/app.py:3:1".
var ruffLocationRE = regexp.MustCompile(`^ --> (.+):([0-9]+):[0-9]+$`)

// mypyMessageRE matches one line of mypy's default plain-text output: a
// file:line, a severity word, the message, and an optional trailing
// "[error-code]" mypy omits on some diagnostics. mypy's own "note" lines
// (e.g. a multi-line hint following an error) carry a third severity word
// this pattern doesn't match, so they are skipped as a finding's child
// exactly like a compiler's own note/help lines are elsewhere in this
// package.
var mypyMessageRE = regexp.MustCompile(`^(\S+\.py):(\d+): (error|warning): (.+?)(?: \[([\w-]+)\])?$`)

// Parse reads ruff's and mypy's output line by line. A ruff violation prints
// its header (a rule code or "unformatted") on one line and its file
// location on the next; Parse holds the header's code/message until that
// location line arrives, then emits one Diagnostic combining both. A mypy
// finding names its own file, line and message on a single line. Everything
// else — uv's own progress and error text, ruff's per-run summary line, the
// diff body under a violation block, mypy's own summary line — is expected
// noise and yields nothing directly; a non-zero exit with nothing parsed
// still falls back to one synthetic diagnostic, the same fallback every
// subprocess-routed adapter in this package uses for a shape it doesn't
// specifically parse.
func (pythonAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	var diags []Diagnostic
	var pendingCode, pendingMessage string
	havePending := false

	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			line := string(bytes.TrimRight(raw, "\r"))
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 {
				continue
			}

			if m := ruffLocationRE.FindStringSubmatch(line); m != nil && havePending {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Code:     pendingCode,
					Message:  pendingMessage,
					File:     m[1],
					Line:     atoiOrZero(m[2]),
				})
				havePending = false
				continue
			}
			if m := ruffRuleHeaderRE.FindStringSubmatch(line); m != nil {
				pendingCode, pendingMessage, havePending = m[1], m[2], true
				continue
			}
			if m := ruffFormatHeaderRE.FindStringSubmatch(line); m != nil {
				pendingCode, pendingMessage, havePending = "", m[1], true
				continue
			}
			if m := mypyMessageRE.FindStringSubmatch(line); m != nil {
				severity := SeverityError
				if m[3] == "warning" {
					severity = SeverityWarning
				}
				diags = append(diags, Diagnostic{
					Severity: severity,
					Code:     m[5],
					Message:  fmt.Sprintf("mypy: %s", m[4]),
					File:     m[1],
					Line:     atoiOrZero(m[2]),
				})
				continue
			}
		}
	}

	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("uv, ruff or mypy exited %d with no parsed diagnostics; see log_ref for raw output", exitCode),
		})
	}
	return diags, nil
}

// banditReport is the subset of `bandit -r -f json`'s shape this adapter
// needs: one finding per flagged pattern in the scanned tree.
type banditReport struct {
	Results []struct {
		Filename      string `json:"filename"`
		IssueSeverity string `json:"issue_severity"`
		IssueText     string `json:"issue_text"`
		LineNumber    int    `json:"line_number"`
		TestID        string `json:"test_id"`
	} `json:"results"`
}

// parseBanditJSON turns one bandit report into diagnostics tagged with its
// rule ID (e.g. "B101"). MEDIUM and HIGH severity count as errors; LOW counts
// as a warning, mirroring gosec's own severity split (parseGosecJSON).
func parseBanditJSON(stdout []byte) []Diagnostic {
	var report banditReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Results))
	for _, r := range report.Results {
		severity := SeverityWarning
		if strings.EqualFold(r.IssueSeverity, "HIGH") || strings.EqualFold(r.IssueSeverity, "MEDIUM") {
			severity = SeverityError
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     r.TestID,
			Message:  fmt.Sprintf("bandit: %s", r.IssueText),
			File:     r.Filename,
			Line:     r.LineNumber,
		})
	}
	return diags
}

// atoiOrZero parses a decimal string already validated by the caller's own
// digit class, falling back to zero rather than propagating an error Parse
// has no channel to report through.
func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
