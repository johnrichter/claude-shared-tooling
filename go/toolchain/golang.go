package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// The executables Go's seven MATRIX pairs spawn, named once so a tool's
// argv, its diagnostic-message prefix (OD46: keep every tool's finding
// attributable to it, since Diagnostic itself carries no tool field) and its
// composite Tool() label all read the same string.
const (
	goVet        = "go vet"
	goTest       = "go test"
	golangciLint = "golangci-lint"
	goImports    = "goimports"
	staticcheck  = "staticcheck"
	gosec        = "gosec"
	govulncheck  = "govulncheck"
	gotestsum    = "gotestsum"
)

// lintResultTool, vetResultTool and securityResultTool are the fixed
// RunResult.Tool labels for lint, vet and security: each pair's two tools
// always run together, so the composite names both accurately. testResultTool
// names only go test, the one tool both test kinds share — gotestsum joins
// it for the unit kind alone, and Tool's per-check signature (no
// target.Test) can't name that distinction for both kinds at once.
const (
	lintResultTool     = golangciLint + "+" + goImports
	vetResultTool      = goVet + "+" + staticcheck
	securityResultTool = gosec + "+" + govulncheck
	testResultTool     = goTest
)

// goTestJUnitFile and goTestCoverageFile are the fixed names runUnitTest
// writes its structured report and coverage profile to, inside target.Dir —
// the same place a caller running gotestsum by hand there would leave them.
const (
	goTestJUnitFile    = "junit.xml"
	goTestCoverageFile = "coverage.out"
)

// goAdapter is the Adapter for Go modules. build and format each spawn one
// tool, so they take the subprocess route like every other language's
// adapter. lint, vet, security and test do not: lint, vet and security each
// front two tools that answer different questions (OD46), and test's tool
// choice depends on target.Test — unit wraps go test in gotestsum for
// coverage and a structured report (OD50), e2e runs go test alone — which
// Command and Tool's per-check signature can never see. All four route
// in-process instead, spawning every tool they need themselves and tagging
// each diagnostic's message with the tool that raised it.
type goAdapter struct{}

// NewGoAdapter returns the Adapter for Go modules. go/toolchain registers no
// Go adapter on its own, unlike cargoAdapter's and pythonAdapter's init-time
// self-registration — a caller registers the result itself.
func NewGoAdapter() Adapter {
	return goAdapter{}
}

func (goAdapter) Language() string { return "go" }

// Route reports the subprocess route for build and format, and the
// in-process route for everything else — see the goAdapter doc for why lint,
// vet, security and test all need it.
func (goAdapter) Route(check Check) Route {
	switch check {
	case CheckBuild, CheckFormat:
		return RouteSubprocess
	default:
		return RouteInProcess
	}
}

// Tool names gofmt and go for the two subprocess-routed checks, the fixed
// two-tool composite for lint, vet and security, and go test alone for test
// — see testResultTool's doc for why test can only name its common tool.
func (goAdapter) Tool(check Check) string {
	switch check {
	case CheckFormat:
		return "gofmt"
	case CheckLint:
		return lintResultTool
	case CheckVet:
		return vetResultTool
	case CheckSecurity:
		return securityResultTool
	case CheckTest:
		return testResultTool
	default:
		return "go"
	}
}

// Command returns gofmt's or go's argv for the two subprocess-routed checks.
// format lists rather than rewrites (-l, never -w) — `go fmt` forwards to
// `gofmt -l -w`, which rewrites the tree rather than just reporting what
// needs it, so this never shells out to `go fmt`. Every other check routes
// in-process (Route above) and never reaches Command through Run; a direct
// caller still gets ErrUnsupportedCheck rather than a silent nil.
func (a goAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckFormat:
		return []string{"-l", "."}, nil
	case CheckBuild:
		return []string{"build"}, nil
	default:
		return nil, errUnsupportedCheck(a.Tool(check), check)
	}
}

// RunInProcess performs target's check by spawning the tool(s) that check
// needs and merging their normalized diagnostics. build and format are
// unreachable here, since Route sends them through the subprocess path
// instead. A returned error means one of the tools could not even be
// started or waited on — the same infrastructure-failure contract Run's own
// subprocess path upholds; a tool that ran and reported findings is always a
// Diagnostic, never an error.
func (a goAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Check {
	case CheckLint:
		return a.runLint(ctx, target)
	case CheckVet:
		return a.runVet(ctx, target)
	case CheckSecurity:
		return a.runSecurity(ctx, target)
	case CheckTest:
		return a.runTest(ctx, target)
	default:
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
}

// runTool spawns tool with args in dir and returns its captured result, or a
// wrapped error if it could not even be started or waited on — the same
// infrastructure-failure contract runSubprocess (run.go) upholds for the
// single-tool route.
func runTool(ctx context.Context, dir, tool string, args []string) (*sysops.Result, error) {
	res, err := sysops.Run(ctx, tool, args, sysops.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("toolchain: run %s %v in %s: %w", tool, args, dir, err)
	}
	return res, nil
}

// fallbackDiagnostic is the synthetic diagnostic a multi-tool check reports
// for one sub-tool that exited non-zero but whose output none of this
// adapter's parsers recognized — the same safety net every subprocess-routed
// adapter's Parse gives a single tool, scoped here to the one sub-tool it
// names so a reader still knows which invocation actually failed.
func fallbackDiagnostic(tool string, exitCode int) Diagnostic {
	return Diagnostic{
		Severity: SeverityError,
		Message:  fmt.Sprintf("%s exited %d with no parsed diagnostics; see log_ref for raw output", tool, exitCode),
	}
}

// runLint runs golangci-lint and goimports against target.Dir and merges
// their findings (OD5: lint invokes golangci-lint as a subprocess). Both
// front the lint pair per OD46: golangci-lint carries the configured linter
// suite (its config resolves from the language-tools tree, per OD47 —
// Command never names one, so golangci-lint discovers it from target.Dir the
// way it would from any working directory), goimports catches an unsorted or
// ungrouped import block golangci-lint's own report doesn't carry on its own.
func (goAdapter) runLint(ctx context.Context, target Target) ([]Diagnostic, error) {
	var diags []Diagnostic

	lintRes, err := runTool(ctx, target.Dir, golangciLint, []string{
		"run", "--output.json.path", "stdout", "--output.text.path", "stderr",
	})
	if err != nil {
		return nil, err
	}
	lintDiags := parseGolangciLintJSON(lintRes.Stdout)
	if len(lintDiags) == 0 && lintRes.ExitCode != 0 {
		lintDiags = append(lintDiags, fallbackDiagnostic(golangciLint, lintRes.ExitCode))
	}
	diags = append(diags, lintDiags...)

	impRes, err := runTool(ctx, target.Dir, goImports, []string{"-l", "."})
	if err != nil {
		return nil, err
	}
	// goimports -l always exits 0, exactly like gofmt -l, so no path printed
	// is unambiguously a clean run — no fallback branch needed here.
	diags = append(diags, parseGoImportsPaths(impRes.Stdout)...)

	return diags, nil
}

// runVet runs the compiler's own analyses (go vet) and staticcheck against
// target.Dir and merges their findings. Go carries both because F48 records
// that Rust's single cargo check gate needs no separate vet step while Go
// does: no one tool here covers both compiler-level and
// style-or-correctness-level analysis the way cargo check does for Rust.
// staticcheck stays out of the lint pair (OD46) — it is vet's second tool,
// not lint's.
func (goAdapter) runVet(ctx context.Context, target Target) ([]Diagnostic, error) {
	var diags []Diagnostic

	vetRes, err := runTool(ctx, target.Dir, "go", []string{"vet", "./..."})
	if err != nil {
		return nil, err
	}
	// go vet writes its file:line:col findings to stderr, exiting non-zero
	// when it reports any.
	vetDiags := parseFileLineMessages(vetRes.Stderr, goVet)
	if len(vetDiags) == 0 && vetRes.ExitCode != 0 {
		vetDiags = append(vetDiags, fallbackDiagnostic(goVet, vetRes.ExitCode))
	}
	diags = append(diags, vetDiags...)

	scRes, err := runTool(ctx, target.Dir, staticcheck, []string{"-f", "json", "./..."})
	if err != nil {
		return nil, err
	}
	scDiags := parseStaticcheckJSON(scRes.Stdout)
	if len(scDiags) == 0 && scRes.ExitCode != 0 {
		scDiags = append(scDiags, fallbackDiagnostic(staticcheck, scRes.ExitCode))
	}
	diags = append(diags, scDiags...)

	return diags, nil
}

// runSecurity runs gosec and govulncheck against target.Dir and merges their
// findings: gosec catches an insecure pattern in target's own source,
// govulncheck catches a dependency carrying a known vulnerability actually
// reachable from target's code — two different questions neither tool
// answers alone.
func (goAdapter) runSecurity(ctx context.Context, target Target) ([]Diagnostic, error) {
	var diags []Diagnostic

	secRes, err := runTool(ctx, target.Dir, gosec, []string{"-fmt=json", "./..."})
	if err != nil {
		return nil, err
	}
	secDiags := parseGosecJSON(secRes.Stdout)
	if len(secDiags) == 0 && secRes.ExitCode != 0 {
		secDiags = append(secDiags, fallbackDiagnostic(gosec, secRes.ExitCode))
	}
	diags = append(diags, secDiags...)

	vulnRes, err := runTool(ctx, target.Dir, govulncheck, []string{"-json", "./..."})
	if err != nil {
		return nil, err
	}
	vulnDiags := parseGovulncheckStream(vulnRes.Stdout)
	if len(vulnDiags) == 0 && vulnRes.ExitCode != 0 {
		vulnDiags = append(vulnDiags, fallbackDiagnostic(govulncheck, vulnRes.ExitCode))
	}
	diags = append(diags, vulnDiags...)

	return diags, nil
}

// runTest dispatches on target.Test — the one piece of information Command
// and Tool can never see, since their signature carries only the check —
// which is why the test pair routes in-process even though its e2e kind
// alone spawns only one tool.
func (a goAdapter) runTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	switch target.Test {
	case TestUnit:
		return a.runUnitTest(ctx, target)
	case TestE2E:
		return a.runE2ETest(ctx, target)
	default:
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
}

// runUnitTest runs the unit-test pair through gotestsum, which wraps go
// test's own machinery and, per OD50, produces a coverage profile
// (goTestCoverageFile) and a structured JUnit report (goTestJUnitFile) in
// this one run rather than a second invocation. --format standard-verbose
// makes gotestsum re-render go test's events the way `go test -v` itself
// prints them, so parseGoTestFailures reads the same "--- FAIL:" shape
// whether gotestsum or runE2ETest's plain go test ran.
func (goAdapter) runUnitTest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, gotestsum, []string{
		"--format", "standard-verbose",
		"--junitfile", filepath.Join(target.Dir, goTestJUnitFile),
		"--", "-coverprofile=" + filepath.Join(target.Dir, goTestCoverageFile), "./...",
	})
	if err != nil {
		return nil, err
	}
	diags := parseGoTestFailures(res.Stdout, gotestsum)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(gotestsum, res.ExitCode))
	}
	return diags, nil
}

// runE2ETest runs the e2e-tagged suite through plain go test: the MATRIX
// names no gotestsum for this pair, unlike unit's. An e2e test imports
// chromedp, which drives an actual Chrome/Chromium binary over its DevTools
// protocol rather than anything this adapter spawns itself; per OD56 and E8,
// that binary is never assumed ambient here — the CI template invoking this
// check is what installs and pins Chrome before this check ever runs. A
// missing Chrome surfaces as chromedp's own error inside the test output,
// parsed like any other test failure rather than a distinct failure mode.
func (goAdapter) runE2ETest(ctx context.Context, target Target) ([]Diagnostic, error) {
	res, err := runTool(ctx, target.Dir, "go", []string{"test", "-tags=e2e", "./..."})
	if err != nil {
		return nil, err
	}
	diags := parseGoTestFailures(res.Stdout, goTest)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(goTest, res.ExitCode))
	}
	return diags, nil
}

// gofmtUnformattedPathRE matches one line of `gofmt -l .` or `goimports -l
// .` output: a bare relative file path, with none of the "file:line:
// message" shape a build, vet or test diagnostic carries. The
// whitespace-free shape is deliberate: it is what keeps a prose line ending
// in a .go word — anything a test under `go test ./...` chooses to print —
// from reading as an unformatted path. The cost is a path that itself
// contains a space, legal but vanishingly rare in a Go tree; loosening the
// shape to admit one would admit that prose too.
var gofmtUnformattedPathRE = regexp.MustCompile(`^\S+\.go$`)

// Parse turns the raw stdout/stderr of whichever tool ran — gofmt for the
// format check, go build for the build check, the two subprocess-routed
// checks (Route above) — into diagnostics. Parse's signature carries no
// check, so it tells the two apart by shape: gofmt -l prints one bare
// unformatted path per line and exits 0 regardless of what it found, while a
// failed build prints one "file:line:col: message" compiler error per line
// (fileLineMessageRE, the same shape go vet's own output takes) and exits
// non-zero. A line matching neither shape (e.g. build's leading "# package"
// header) is skipped, and an exit code with nothing recognized falls back to
// one synthetic diagnostic — without this step an unformatted tree would
// read as a clean run, and a build failure Parse couldn't recognize would
// read as a silent pass. Every other check routes in-process (Route above)
// and never reaches Parse through Run.
func (goAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	var diags []Diagnostic
	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				continue
			}
			if gofmtUnformattedPathRE.Match(line) {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Message:  "not gofmt-formatted",
					File:     string(line),
				})
				continue
			}
			if m := fileLineMessageRE.FindSubmatch(line); m != nil {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Message:  fmt.Sprintf("%s: %s", "go build", string(m[4])),
					File:     string(m[1]),
					Line:     atoiOrZero(string(m[2])),
				})
			}
		}
	}
	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, fallbackDiagnostic("gofmt or go", exitCode))
	}
	return diags, nil
}

// parseGoImportsPaths turns goimports -l's output — one unformatted path per
// line, exit 0 regardless of what it found — into one error diagnostic per
// path, tagged as goimports's own finding so it reads distinctly from a
// golangci-lint issue in the same run.
func parseGoImportsPaths(stdout []byte) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || !gofmtUnformattedPathRE.Match(line) {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("%s: import block not organized", goImports),
			File:     string(line),
		})
	}
	return diags
}

// golangciLintReport is the subset of golangci-lint's --output.json.path
// shape this adapter needs. golangci-lint keeps writing a plain-text issue
// count after the JSON object on the same stream regardless of
// --output.text.path, so parseGolangciLintJSON decodes with json.Decoder
// rather than json.Unmarshal: only the JSON value at the head of stdout is
// read, and whatever text follows it is ignored rather than causing a parse
// error.
type golangciLintReport struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Severity   string `json:"Severity"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
		} `json:"Pos"`
	} `json:"Issues"`
}

// parseGolangciLintJSON turns one golangci-lint JSON report into
// diagnostics, tagged with the linter that raised each one (FromLinter) so a
// caller can tell golangci-lint's own finding apart from goimports's in the
// same lint run. An issue with no Severity set — golangci-lint's own default
// when a linter reports none — counts as an error, matching this package's
// must-fail-on-warning default.
func parseGolangciLintJSON(stdout []byte) []Diagnostic {
	var report golangciLintReport
	if err := json.NewDecoder(bytes.NewReader(stdout)).Decode(&report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Issues))
	for _, issue := range report.Issues {
		severity := SeverityError
		if strings.EqualFold(issue.Severity, "warning") {
			severity = SeverityWarning
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     issue.FromLinter,
			Message:  fmt.Sprintf("%s: %s", golangciLint, issue.Text),
			File:     issue.Pos.Filename,
			Line:     issue.Pos.Line,
		})
	}
	return diags
}

// fileLineMessageRE matches one line of go vet's plain-text output —
// file:line:col: message — with no trailing rule code the way staticcheck's
// own plain text carries one. parseGoTestFailures reuses it for the same
// shape a failed test compile prints ahead of any test actually running.
var fileLineMessageRE = regexp.MustCompile(`^(\S+\.go):(\d+):(\d+): (.+)$`)

// parseFileLineMessages turns tool's file:line:col: message lines into
// diagnostics tagged with tool.
func parseFileLineMessages(output []byte, tool string) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range bytes.Split(output, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		m := fileLineMessageRE.FindSubmatch(line)
		if m == nil {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("%s: %s", tool, string(m[4])),
			File:     string(m[1]),
			Line:     atoiOrZero(string(m[2])),
		})
	}
	return diags
}

// staticcheckFinding is one line of `staticcheck -f json`'s output: one JSON
// object per finding (JSON Lines), never a wrapping array.
type staticcheckFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Location struct {
		File string `json:"file"`
		Line int    `json:"line"`
	} `json:"location"`
	Message string `json:"message"`
}

// parseStaticcheckJSON turns staticcheck's JSON Lines output into
// diagnostics tagged with its own tool name and each finding's check code
// (e.g. "SA4006"), so it reads distinctly from a go vet finding in the same
// vet run.
func parseStaticcheckJSON(stdout []byte) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var f staticcheckFinding
		if err := json.Unmarshal(line, &f); err != nil {
			continue
		}
		severity := SeverityError
		if strings.EqualFold(f.Severity, "warning") {
			severity = SeverityWarning
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     f.Code,
			Message:  fmt.Sprintf("%s: %s", staticcheck, f.Message),
			File:     f.Location.File,
			Line:     f.Location.Line,
		})
	}
	return diags
}

// gosecReport is the subset of `gosec -fmt=json`'s shape this adapter needs.
// gosec prints line and column as strings, not numbers.
type gosecReport struct {
	Issues []struct {
		Severity string `json:"severity"`
		RuleID   string `json:"rule_id"`
		Details  string `json:"details"`
		File     string `json:"file"`
		Line     string `json:"line"`
	} `json:"Issues"`
}

// parseGosecJSON turns one gosec JSON report into diagnostics tagged with
// its rule ID (e.g. "G404"). MEDIUM and HIGH severity count as errors; LOW
// counts as a warning, since a LOW gosec finding sits closer to an advisory
// than a must-fix on gosec's own severity scale.
func parseGosecJSON(stdout []byte) []Diagnostic {
	var report gosecReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &report); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(report.Issues))
	for _, issue := range report.Issues {
		severity := SeverityWarning
		if strings.EqualFold(issue.Severity, "high") || strings.EqualFold(issue.Severity, "medium") {
			severity = SeverityError
		}
		diags = append(diags, Diagnostic{
			Severity: severity,
			Code:     issue.RuleID,
			Message:  fmt.Sprintf("%s: %s", gosec, issue.Details),
			File:     issue.File,
			Line:     atoiOrZero(issue.Line),
		})
	}
	return diags
}

// govulncheckMessage is the one message shape parseGovulncheckStream reads
// out of `govulncheck -json`'s NDJSON stream: each vulnerable call path is
// its own "finding" message naming the OSV ID and the call trace into it.
// Every other message kind on the stream ("config", "progress", "osv")
// carries no finding and is skipped.
type govulncheckMessage struct {
	Finding *struct {
		OSV   string `json:"osv"`
		Trace []struct {
			Package string `json:"package"`
		} `json:"trace"`
	} `json:"finding"`
}

// parseGovulncheckStream turns govulncheck's NDJSON stream into one
// diagnostic per vulnerable module actually reachable from target's code —
// deduplicated by OSV ID, since govulncheck emits one finding per call-stack
// frame on the way to the vulnerable symbol rather than one per
// vulnerability.
func parseGovulncheckStream(stdout []byte) []Diagnostic {
	seen := map[string]bool{}
	var diags []Diagnostic
	for _, raw := range bytes.Split(stdout, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var msg govulncheckMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Finding == nil || msg.Finding.OSV == "" || seen[msg.Finding.OSV] {
			continue
		}
		seen[msg.Finding.OSV] = true
		file := ""
		if len(msg.Finding.Trace) > 0 {
			file = msg.Finding.Trace[0].Package
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Code:     msg.Finding.OSV,
			Message:  fmt.Sprintf("%s: known vulnerability %s reachable from this module", govulncheck, msg.Finding.OSV),
			File:     file,
		})
	}
	return diags
}

// goTestFailureRE matches go test's own per-test failure line, printed
// whether go test ran directly (runE2ETest) or gotestsum's standard-verbose
// format re-rendered it (runUnitTest).
var goTestFailureRE = regexp.MustCompile(`^--- FAIL: (\S+)`)

// goTestLogLineRE matches the indented "file:line: message" line testing.T's
// own Error/Errorf/Fatal/Fatalf prints at the caller's source location. It
// carries no column — unlike fileLineMessageRE's three-part
// compiler-diagnostic shape — since a test log line names a statement, not a
// token position.
var goTestLogLineRE = regexp.MustCompile(`^(\S+\.go):(\d+): (.+)$`)

// goTestBlockBoundaryRE marks a line goTestFailureLocation never crosses
// while hunting for a failing test's own log line: a blank separator, the
// next test starting or finishing, or the package's own trailing summary
// line. Plain (non-verbose) go test prints "=== RUN" for nothing, so a
// boundary here means "some other test's or the package's own output",
// never this failure's.
var goTestBlockBoundaryRE = regexp.MustCompile(`^(=== RUN|--- (FAIL|PASS)|FAIL|PASS|ok)\b`)

// goTestFailureLocation returns the file and line goTestLogLineRE names
// nearest failIdx (the index of that failure's own "--- FAIL: Name" line)
// within lines, or ("", 0) if none is found before a block boundary.
// Verbose output (go test -v, and gotestsum's standard-verbose re-rendering
// of it) flushes a failing test's Error/Fatal log lines immediately before
// its "--- FAIL" header, separated from the prior test by that test's own
// "=== RUN"; plain go test (no -v, what runE2ETest spawns) prints them
// immediately after the header instead, with no "=== RUN" separating one
// failure's trailing logs from the next failure's header. Forward is
// searched first: in plain output that reads this failure's own logs, while
// in verbose the forward scan hits a boundary at once and falls through to
// the backward scan. A backward-first scan would misattribute the second of
// two adjacent plain failures to the first's trailing log line.
func goTestFailureLocation(lines [][]byte, failIdx int) (file string, line int) {
	for j := failIdx + 1; j < len(lines); j++ {
		l := bytes.TrimSpace(lines[j])
		if len(l) == 0 || goTestBlockBoundaryRE.Match(l) {
			break
		}
		if m := goTestLogLineRE.FindSubmatch(l); m != nil {
			return string(m[1]), atoiOrZero(string(m[2]))
		}
	}
	for j := failIdx - 1; j >= 0; j-- {
		l := bytes.TrimSpace(lines[j])
		if len(l) == 0 || goTestBlockBoundaryRE.Match(l) {
			break
		}
		if m := goTestLogLineRE.FindSubmatch(l); m != nil {
			return string(m[1]), atoiOrZero(string(m[2]))
		}
	}
	return "", 0
}

// parseGoTestFailures turns go test's "--- FAIL: Name" lines into one
// diagnostic per failing test, tagged with tool. Each failure's diagnostic
// carries the file and line goTestFailureLocation finds nearest it — the
// caller's actual source location, not just which test failed. A compile
// failure ahead of any test running prints file:line:col lines instead
// (fileLineMessageRE), which this also recognizes so a broken build doesn't
// read as a silently clean test run.
func parseGoTestFailures(stdout []byte, tool string) []Diagnostic {
	var diags []Diagnostic
	lines := bytes.Split(stdout, []byte("\n"))
	for i := 0; i < len(lines); i++ {
		line := bytes.TrimSpace(lines[i])
		if m := goTestFailureRE.FindSubmatch(line); m != nil {
			file, lineNo := goTestFailureLocation(lines, i)
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: test failed: %s", tool, string(m[1])),
				File:     file,
				Line:     lineNo,
			})
			continue
		}
		if m := fileLineMessageRE.FindSubmatch(line); m != nil {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: %s", tool, string(m[4])),
				File:     string(m[1]),
				Line:     atoiOrZero(string(m[2])),
			})
		}
	}
	return diags
}
