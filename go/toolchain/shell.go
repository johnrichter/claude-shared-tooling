package toolchain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

func init() {
	Register(shellAdapter{})
}

// shellAdapter is the Adapter for shell scripts. Unlike Go, Rust and Python,
// a shell target names no build system and no manifest — the file set is
// discovered by walking the tree (discoverShellFiles), never declared by a
// dependency file — and a script neither compiles nor type-checks, so MATRIX
// gives shell no build and no vet (OD3): Command answers ErrUnsupportedCheck
// for both, and RunInProcess's default case does too, so an out-of-matrix
// request fails closed at EXIT 80 rather than passing silently.
//
// All five pairs — format, lint, security, test unit, test e2e — route
// in-process rather than subprocess: every one of them needs Target.Dir to
// either discover the file set or locate the test tree first, and Command's
// per-check signature (no Target) can never see it. RunInProcess spawns
// every tool itself and tags each diagnostic's message with the tool that
// raised it, the same convention goAdapter and cargoAdapter use for their
// own in-process checks.
type shellAdapter struct{}

func (shellAdapter) Language() string { return LanguageShell }

// Route reports the in-process route for every check — see the shellAdapter
// doc for why none of the five pairs can take the subprocess route.
func (shellAdapter) Route(check Check) Route { return RouteInProcess }

// The executables shell's five MATRIX pairs spawn, named once so a tool's
// argv, its diagnostic-message prefix and its composite Tool() label all
// read the same string.
const (
	shfmtTool         = "shfmt"
	shellcheckTool    = "shellcheck"
	checkbashismsTool = "checkbashisms"
	semgrepTool       = "semgrep"
	batsTool          = "bats"
	kcovTool          = "kcov"
)

// shellLintResultTool and shellTestResultTool are the fixed RunResult.Tool
// labels for lint and test. lint's two tools always run together, so the
// composite names both accurately (mirroring golang.go's lintResultTool).
// testResultTool names only bats, the one tool both test kinds share — kcov
// joins it for the unit kind alone, and Tool's per-check signature (no
// target.Test) can't name that distinction for both kinds at once (mirroring
// golang.go's testResultTool).
const (
	shellLintResultTool = shellcheckTool + "+" + checkbashismsTool
	shellTestResultTool = batsTool
)

// Tool names shfmt, the lint composite, semgrep and bats for the four checks
// MATRIX gives shell; build and vet have no tool, so the default answers the
// bare language name — a request for either is rejected before this is ever
// consulted for real (ResolveCheck's matrixSpec has no shell row for them),
// but Tool itself carries no error return and so must still answer.
func (shellAdapter) Tool(check Check) string {
	switch check {
	case CheckFormat:
		return shfmtTool
	case CheckLint:
		return shellLintResultTool
	case CheckSecurity:
		return semgrepTool
	case CheckTest:
		return shellTestResultTool
	default:
		return LanguageShell
	}
}

// Command always answers ErrUnsupportedCheck: every shell check routes
// in-process (Route above), so Run's subprocess path never calls this — it
// exists only to satisfy the Adapter interface, exactly as cargoAdapter's
// Command does for its own in-process checks.
func (a shellAdapter) Command(check Check) ([]string, error) {
	return nil, errUnsupportedCheck(a.Tool(check), check)
}

// Parse is never reached: every shell check routes in-process, so Run's
// subprocess path never calls it. It exists only to satisfy the Adapter
// interface.
func (shellAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	return nil, nil
}

// RunInProcess performs target's check by discovering the relevant files
// under target.Dir and spawning the tool(s) that check needs, merging their
// normalized diagnostics. build and vet fall to the default case (OD3: not
// in the shell row of matrixSpec, so ResolveCheck already refuses them
// before Run ever calls an adapter — this is the adapter's own defense in
// depth for a caller that bypasses ResolveCheck). A returned error means a
// tool could not even be started or waited on, or the tree could not be
// walked — an infrastructure failure, never a code problem; a tool that ran
// and reported findings is always a Diagnostic.
func (a shellAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Check {
	case CheckFormat:
		return a.runFormat(ctx, target)
	case CheckLint:
		return a.runLint(ctx, target)
	case CheckSecurity:
		return a.runSecurity(ctx, target)
	case CheckTest:
		return a.runTest(ctx, target)
	default:
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
}

// shellShebangRE matches the first line of an extensionless file that
// declares itself a POSIX-family shell interpreter, directly or through
// env — the two shapes F49 found among the fleet's nine extensionless
// scripts.
var shellShebangRE = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+)?(?:/bin/|/usr/bin/)?(?:sh|bash|dash|ksh)\b`)

// shellShebangReadLimit bounds how much of a candidate extensionless file
// isShellShebang reads before giving up — enough for the longest realistic
// shebang line, never the whole file, so a large non-shell binary is never
// scanned in full.
const shellShebangReadLimit = 256

// isShellShebang reports whether path's first line names a shell
// interpreter.
func isShellShebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, shellShebangReadLimit)
	n, _ := f.Read(buf)
	line, _, _ := bufio.NewReader(bytes.NewReader(buf[:n])).ReadLine()
	return shellShebangRE.Match(line)
}

// discoverShellFiles walks root — the shell script root a Target.Dir names
// for a shell check — and returns every shell file at or below it, sorted
// for a deterministic tool invocation order. A file counts as shell if it
// carries the .sh extension, or is extensionless with a shell shebang on its
// first line (isShellShebang); this is OD54's discovery rule, and it is what
// every one of the five checks below consults, rather than each tool's own
// (inconsistent, in shellcheck's and checkbashisms's case nonexistent)
// directory-walk convention. F49 measured this rule against the fleet on
// 2026-08-25 at 117 in-scope files — 109 outside .githooks/ and 8 inside it,
// one per repository across 8 repositories — nine of the 117 extensionless.
// Only .git is skipped (its own hook-sample and internal plumbing scripts
// are not the fleet's own shell files); every other directory, .githooks/
// included, is walked.
func discoverShellFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		switch ext := filepath.Ext(d.Name()); {
		case strings.EqualFold(ext, ".sh"):
			files = append(files, path)
		case ext == "" && isShellShebang(path):
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("toolchain: discover shell files under %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

// relativeTo converts each absolute path in files to a path relative to
// dir, the working directory runTool spawns every shell tool in — the same
// convention gofmt -l's and goimports -l's own output already follows, so a
// diagnostic's File field reads as a caller running the tool by hand there
// would see it.
func relativeTo(dir string, files []string) ([]string, error) {
	rel := make([]string, len(files))
	for i, f := range files {
		r, err := filepath.Rel(dir, f)
		if err != nil {
			return nil, fmt.Errorf("toolchain: relativize %s to %s: %w", f, dir, err)
		}
		rel[i] = r
	}
	return rel, nil
}

// runFormat runs shfmt -l against every file discoverShellFiles finds under
// target.Dir, reporting one diagnostic per path shfmt would reformat. Like
// gofmt -l, -l alone only lists; it never rewrites what it's asked to check.
// An empty file set is a trivial pass — there is nothing for shfmt to check.
func (shellAdapter) runFormat(ctx context.Context, target Target) ([]Diagnostic, error) {
	files, err := discoverShellFiles(target.Dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	rel, err := relativeTo(target.Dir, files)
	if err != nil {
		return nil, err
	}
	res, err := runTool(ctx, target.Dir, shfmtTool, append([]string{"-l"}, rel...))
	if err != nil {
		return nil, err
	}
	diags := parseShfmtList(res.Stdout)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(shfmtTool, res.ExitCode))
	}
	return diags, nil
}

// parseShfmtList turns shfmt -l's output — one unformatted path per line —
// into one error diagnostic per path.
func parseShfmtList(stdout []byte) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  "not shfmt-formatted",
			File:     string(line),
		})
	}
	return diags
}

// shellcheckConfigDir and shellcheckConfigBase name the fixed language-tools
// config file lint's shellcheck invocation reads (SC37: every tool config
// lives in language-tools, never a caller's). shellcheckConfigPath
// materializes it under a private per-process temp directory, mirroring
// python.go's mypyConfigPath, so it can never collide with a caller's own
// .shellcheckrc.
const (
	shellcheckConfigDir  = "language-tools"
	shellcheckConfigBase = ".shellcheckrc"
)

// shellcheckDefaultConfig is the language-tools shellcheck configuration.
// external-sources=true lets a `source`/`.` statement resolve without
// shellcheck failing SC1091 on a target it cannot see from this run's own
// file list; OD12 makes the CI workflow (ci-shell.yml) responsible for the
// include list a sourced file actually needs, not this adapter.
const shellcheckDefaultConfig = "external-sources=true\n"

// shellcheckParentOnce, shellcheckParentDir and shellcheckParentErr memoize
// shellcheckConfigParent exactly as python.go's mypyParentOnce/mypyParentDir
// do for mypy's own config parent.
var (
	shellcheckParentOnce sync.Once
	shellcheckParentDir  string
	shellcheckParentErr  error
)

// shellcheckConfigParent resolves, once per process, a unique private
// parent directory under os.TempDir() to hold the language-tools config
// tree — see python.go's mypyConfigParent for why a fixed, predictable
// parent is unsafe here too.
func shellcheckConfigParent() (string, error) {
	shellcheckParentOnce.Do(func() {
		shellcheckParentDir, shellcheckParentErr = os.MkdirTemp("", "claude-toolchain-")
	})
	return shellcheckParentDir, shellcheckParentErr
}

// shellcheckConfigPath writes shellcheckDefaultConfig to its fixed path and
// returns it, re-creating the language-tools subdir on every call so the
// config self-heals even if a temp reaper removes the parent mid-process.
func shellcheckConfigPath() (string, error) {
	parent, err := shellcheckConfigParent()
	if err != nil {
		return "", fmt.Errorf("toolchain: create language-tools config parent: %w", err)
	}
	dir := filepath.Join(parent, shellcheckConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("toolchain: create language-tools config dir: %w", err)
	}
	path := filepath.Join(dir, shellcheckConfigBase)
	if err := os.WriteFile(path, []byte(shellcheckDefaultConfig), 0o644); err != nil {
		return "", fmt.Errorf("toolchain: write shellcheck config: %w", err)
	}
	return path, nil
}

// runLint runs shellcheck and checkbashisms against every file
// discoverShellFiles finds under target.Dir and merges their findings
// (OD46): shellcheck catches a broad range of quoting, quoting-adjacent and
// correctness issues in any shell dialect; checkbashisms catches the one
// thing shellcheck does not — a script using a bash-only construct despite
// itself running under a plain POSIX sh shebang. checkbashisms already skips
// a script whose own shebang names bash explicitly, so running it over the
// whole discovered set (rather than pre-filtering by shebang here) reports
// nothing extra for a file it would not have flagged anyway. An empty file
// set is a trivial pass. shellcheck's --rcfile points at target.ConfigPath
// when the caller set one, winning over shellcheckConfigPath's
// language-tools constant, which remains the fallback when it is unset.
func (shellAdapter) runLint(ctx context.Context, target Target) ([]Diagnostic, error) {
	files, err := discoverShellFiles(target.Dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	rel, err := relativeTo(target.Dir, files)
	if err != nil {
		return nil, err
	}

	var diags []Diagnostic

	rcfile := target.ConfigPath
	if rcfile == "" {
		rcfile, err = shellcheckConfigPath()
		if err != nil {
			return nil, err
		}
	}
	scRes, err := runTool(ctx, target.Dir, shellcheckTool, append([]string{"--rcfile", rcfile, "-f", "json1"}, rel...))
	if err != nil {
		return nil, err
	}
	scDiags := parseShellcheckJSON1(scRes.Stdout)
	if len(scDiags) == 0 && scRes.ExitCode != 0 {
		scDiags = append(scDiags, fallbackDiagnostic(shellcheckTool, scRes.ExitCode))
	}
	diags = append(diags, scDiags...)

	cbRes, err := runTool(ctx, target.Dir, checkbashismsTool, rel)
	if err != nil {
		return nil, err
	}
	cbDiags := parseCheckbashisms(cbRes.Stdout, cbRes.Stderr)
	if len(cbDiags) == 0 && cbRes.ExitCode != 0 {
		cbDiags = append(cbDiags, fallbackDiagnostic(checkbashismsTool, cbRes.ExitCode))
	}
	diags = append(diags, cbDiags...)

	return diags, nil
}

// shellcheckJSON1Report is the subset of `shellcheck -f json1`'s shape this
// adapter needs: one comment per finding.
type shellcheckJSON1Report struct {
	Comments []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Level   string `json:"level"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"comments"`
}

// parseShellcheckJSON1 turns one shellcheck json1 report into diagnostics
// tagged with its SC code (e.g. "SC2086"). "error" and "warning" level
// findings count as errors; "info" and "style" count as warnings, mirroring
// this package's other severity-graded tools (parseBanditJSON,
// parseGosecJSON).
func parseShellcheckJSON1(stdout []byte) []Diagnostic {
	var report shellcheckJSON1Report
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Comments))
	for _, c := range report.Comments {
		severity := SeverityWarning
		if strings.EqualFold(c.Level, "error") || strings.EqualFold(c.Level, "warning") {
			severity = SeverityError
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     fmt.Sprintf("SC%d", c.Code),
			Message:  fmt.Sprintf("%s: %s", shellcheckTool, c.Message),
			File:     c.File,
			Line:     c.Line,
		})
	}
	return diags
}

// checkbashismsFindingRE matches checkbashisms's own per-finding header:
// "possible bashism in <file> line <N> (<reason>):", followed by the
// offending line on its own indented line (which this does not need to
// capture — the header alone already names the file, line and reason).
var checkbashismsFindingRE = regexp.MustCompile(`^possible bashism in (\S+) line (\d+) \((.+)\):$`)

// parseCheckbashisms turns checkbashisms's finding headers into one error
// diagnostic per bashism.
func parseCheckbashisms(stdout, stderr []byte) []Diagnostic {
	var diags []Diagnostic
	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			line := string(bytes.TrimSpace(raw))
			m := checkbashismsFindingRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: %s", checkbashismsTool, m[3]),
				File:     m[1],
				Line:     atoiOrZero(m[2]),
			})
		}
	}
	return diags
}

// shellSecurityConfig is the Semgrep registry ruleset security's semgrep
// invocation runs. F49 measured zero .zsh and zero .fish files fleet-wide
// alongside its 117 in-scope shell files, so a bash-specific ruleset covers
// the dialect actually present rather than a broader, noisier multi-dialect
// config left over from an assumption never checked against the fleet (E6).
const shellSecurityConfig = "r/bash"

// runSecurity runs Semgrep's bash ruleset against every file
// discoverShellFiles finds under target.Dir. An empty file set is a trivial
// pass.
func (shellAdapter) runSecurity(ctx context.Context, target Target) ([]Diagnostic, error) {
	files, err := discoverShellFiles(target.Dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	rel, err := relativeTo(target.Dir, files)
	if err != nil {
		return nil, err
	}
	res, err := runTool(ctx, target.Dir, semgrepTool, append([]string{"--config", shellSecurityConfig, "--json"}, rel...))
	if err != nil {
		return nil, err
	}
	diags := parseSemgrepJSON(res.Stdout)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(semgrepTool, res.ExitCode))
	}
	return diags, nil
}

// semgrepReport is the subset of `semgrep --json`'s shape this adapter
// needs: one result per flagged pattern in the scanned files.
type semgrepReport struct {
	Results []struct {
		CheckID string `json:"check_id"`
		Path    string `json:"path"`
		Start   struct {
			Line int `json:"line"`
		} `json:"start"`
		Extra struct {
			Message  string `json:"message"`
			Severity string `json:"severity"`
		} `json:"extra"`
	} `json:"results"`
}

// parseSemgrepJSON turns one semgrep report into diagnostics tagged with its
// rule ID. ERROR and WARNING severities count as errors; INFO counts as a
// warning, mirroring parseBanditJSON's and parseGosecJSON's own severity
// split.
func parseSemgrepJSON(stdout []byte) []Diagnostic {
	var report semgrepReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Results))
	for _, r := range report.Results {
		severity := SeverityWarning
		if strings.EqualFold(r.Extra.Severity, "ERROR") || strings.EqualFold(r.Extra.Severity, "WARNING") {
			severity = SeverityError
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     r.CheckID,
			Message:  fmt.Sprintf("%s: %s", semgrepTool, r.Extra.Message),
			File:     r.Path,
			Line:     r.Start.Line,
		})
	}
	return diags
}

// shellTestDir is the bats suite root this adapter looks for under
// target.Dir — the one convention every shell project checked by this
// adapter is expected to follow, mirroring the role a *_test.go suffix
// plays for Go or an `e2e` pytest marker plays for Python. A target.Dir with
// no such directory has nothing to test, and both test kinds report a
// trivial pass rather than an error.
const shellTestDir = "test"

// shellUnitCoverageDir is the fixed directory runUnitTest writes kcov's
// coverage report to, inside target.Dir — the same place a caller running
// kcov by hand there would leave it.
const shellUnitCoverageDir = "kcov-coverage"

// runTest dispatches on target.Test — the one piece of information Command
// and Tool can never see, since their signature carries only the check —
// which is why the test pair routes in-process even though its e2e kind
// alone spawns only bats.
func (a shellAdapter) runTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Test {
	case TestUnit:
		return a.runUnitTest(ctx, target)
	case TestE2E:
		return a.runE2ETest(ctx, target)
	default:
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
}

// shellTestTreeExists reports whether target.Dir has a bats suite root
// (shellTestDir) at all, so a project with no shell tests reports a trivial
// pass rather than an error from a tool given a directory that isn't there.
func shellTestTreeExists(target Target) (bool, error) {
	_, err := os.Stat(filepath.Join(target.Dir, shellTestDir))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("toolchain: stat %s: %w", shellTestDir, err)
	}
	return true, nil
}

// runUnitTest runs the unit-test pair through bats wrapped in kcov, which
// produces a coverage report (shellUnitCoverageDir) in this one run rather
// than a second invocation (OD51), mirroring Go's gotestsum wrapper and
// Rust's cargo-llvm-cov nextest (OD50). --filter-tags '!e2e' excludes the
// e2e-tagged suite runE2ETest owns, the exact complement of its own
// --filter-tags e2e, so the two test pairs partition the project's bats
// files the same way Python's `-m "not e2e"`/`-m e2e` partition its pytest
// suite.
func (a shellAdapter) runUnitTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	exists, err := shellTestTreeExists(target)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	outDir := filepath.Join(target.Dir, shellUnitCoverageDir)
	res, err := runTool(ctx, target.Dir, kcovTool, []string{
		"--include-path=" + target.Dir,
		outDir,
		batsTool, "-r", "--filter-tags", "!e2e", shellTestDir,
	})
	if err != nil {
		return nil, err
	}
	diags := parseBatsFailures(res.Stdout, res.Stderr, kcovTool+"+"+batsTool)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(kcovTool+"+"+batsTool, res.ExitCode))
	}
	return diags, nil
}

// runE2ETest runs the e2e-test pair through plain bats: the MATRIX names no
// kcov for this pair, unlike unit's, mirroring Go's and Rust's own
// coverage-on-unit-only convention.
func (a shellAdapter) runE2ETest(ctx context.Context, target Target) ([]Diagnostic, error) {
	exists, err := shellTestTreeExists(target)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	res, err := runTool(ctx, target.Dir, batsTool, []string{"-r", "--filter-tags", "e2e", shellTestDir})
	if err != nil {
		return nil, err
	}
	diags := parseBatsFailures(res.Stdout, res.Stderr, batsTool)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(batsTool, res.ExitCode))
	}
	return diags, nil
}

// batsFailureRE matches bats's own TAP-adjacent "not ok <N> <name>" failure
// line, printed in its default pretty formatter's underlying stream
// regardless of which test kind or coverage wrapper ran it.
var batsFailureRE = regexp.MustCompile(`^not ok \d+ (.+)$`)

// parseBatsFailures turns bats's "not ok" lines into one diagnostic per
// failing test, tagged with tool.
func parseBatsFailures(stdout, stderr []byte, tool string) []Diagnostic {
	var diags []Diagnostic
	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			line := string(bytes.TrimSpace(raw))
			m := batsFailureRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: test failed: %s", tool, m[1]),
			})
		}
	}
	return diags
}
