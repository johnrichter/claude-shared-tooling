package toolchain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSanityGoAdapterDoesNotSelfRegister checks NewGoAdapter never calls
// Register on go/toolchain's behalf — a caller must register the result
// itself, unlike cargoAdapter's and pythonAdapter's init-time
// self-registration.
func TestSanityGoAdapterDoesNotSelfRegister(t *testing.T) {
	if _, ok := lookup("go"); ok {
		t.Fatalf(`adapter registered for "go" before any caller registered one`)
	}
}

// TestSanityGoAdapterToolAndCommandTable checks Route and Tool answer
// correctly for all seven checks, and Command answers an argv for the two
// subprocess-routed checks (build, format) and ErrUnsupportedCheck for
// every check that instead routes in-process.
func TestSanityGoAdapterToolAndCommandTable(t *testing.T) {
	a := NewGoAdapter()
	cases := []struct {
		check   Check
		tool    string
		route   Route
		argv    []string
		wantErr bool
	}{
		{CheckBuild, "go", RouteSubprocess, []string{"build"}, false},
		{CheckFormat, "gofmt", RouteSubprocess, []string{"-l", "."}, false},
		{CheckLint, lintResultTool, RouteInProcess, nil, true},
		{CheckVet, vetResultTool, RouteInProcess, nil, true},
		{CheckSecurity, securityResultTool, RouteInProcess, nil, true},
		{CheckTest, testResultTool, RouteInProcess, nil, true},
	}
	for _, c := range cases {
		if got := a.Tool(c.check); got != c.tool {
			t.Errorf("Tool(%s) = %q, want %q", c.check, got, c.tool)
		}
		if got := a.Route(c.check); got != c.route {
			t.Errorf("Route(%s) = %q, want %q", c.check, got, c.route)
		}
		argv, err := a.Command(c.check)
		if c.wantErr {
			if err == nil {
				t.Errorf("Command(%s) = %v, want ErrUnsupportedCheck", c.check, argv)
			} else if !errors.Is(err, ErrUnsupportedCheck) {
				t.Errorf("Command(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c.check, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Command(%s): unexpected error %v", c.check, err)
			continue
		}
		if len(argv) != len(c.argv) {
			t.Errorf("Command(%s) = %v, want %v", c.check, argv, c.argv)
			continue
		}
		for i := range argv {
			if argv[i] != c.argv[i] {
				t.Errorf("Command(%s) = %v, want %v", c.check, argv, c.argv)
				break
			}
		}
	}
}

// TestSanityGoAdapterRunInProcessRejectsSubprocessChecks checks RunInProcess
// rejects build and format (Route sends those through the subprocess path
// instead) with ErrUnsupportedCheck, and never reaches a tool spawn to do
// so.
func TestSanityGoAdapterRunInProcessRejectsSubprocessChecks(t *testing.T) {
	a := NewGoAdapter()
	for _, c := range []Check{CheckBuild, CheckFormat} {
		_, err := a.RunInProcess(context.Background(), Target{Check: c})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestSanityGoAdapterRunTestRejectsUnrecognizedKind checks the test pair's
// subcommand dispatch (runTest) rejects a target.Test outside
// TestUnit/TestE2E — including the zero value, a bare "test" request with no
// kind — before it ever spawns a tool.
func TestSanityGoAdapterRunTestRejectsUnrecognizedKind(t *testing.T) {
	a := NewGoAdapter()
	for _, kind := range []TestKind{"", TestBenchmark, TestKind("bogus")} {
		_, err := a.RunInProcess(context.Background(), Target{Check: CheckTest, Test: kind, Dir: "."})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(test, kind=%q): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", kind, err)
		}
	}
}

// TestSanityGoAdapterParsesGofmtPaths checks Parse turns a gofmt -l sample —
// one unformatted path per line — into one error diagnostic per path, and
// that a clean (empty) run with exit 0 parses to no diagnostics.
func TestSanityGoAdapterParsesGofmtPaths(t *testing.T) {
	a := NewGoAdapter()
	diags, err := a.Parse(0, []byte("main.go\nsub/other.go\n"), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("Parse = %+v, want 2 diagnostics", diags)
	}
	for _, d := range diags {
		if d.Severity != SeverityError {
			t.Errorf("diagnostic severity = %q, want error", d.Severity)
		}
	}
	if diags[0].File != "main.go" || diags[1].File != "sub/other.go" {
		t.Fatalf("Parse = %+v, want files main.go and sub/other.go", diags)
	}

	clean, err := a.Parse(0, nil, nil)
	if err != nil {
		t.Fatalf("Parse (clean): %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("Parse (clean) = %+v, want no diagnostics", clean)
	}
}

// TestSanityParseGolangciLintJSONAttributesToLinter checks a golangci-lint
// report's issue is tagged with its own FromLinter as Code, so it reads
// distinctly from goimports's own finding in the same lint run (OD46).
func TestSanityParseGolangciLintJSONAttributesToLinter(t *testing.T) {
	report := `{"Issues":[{"FromLinter":"errcheck","Text":"unchecked error","Severity":"","Pos":{"Filename":"main.go","Line":7}}]}`
	diags := parseGolangciLintJSON([]byte(report))
	if len(diags) != 1 {
		t.Fatalf("parseGolangciLintJSON = %+v, want 1 diagnostic", diags)
	}
	d := diags[0]
	if d.Code != "errcheck" || d.File != "main.go" || d.Line != 7 {
		t.Errorf("diagnostic = %+v, want code errcheck at main.go:7", d)
	}
	if d.Severity != SeverityError {
		t.Errorf("severity = %q, want error for an unset golangci-lint severity", d.Severity)
	}
}

// TestSanityParseGoImportsPathsAttributesToGoimports checks an unformatted
// import block is tagged with goimports's own name, distinct from a
// golangci-lint finding.
func TestSanityParseGoImportsPathsAttributesToGoimports(t *testing.T) {
	diags := parseGoImportsPaths([]byte("pkg/file.go\n"))
	if len(diags) != 1 || diags[0].File != "pkg/file.go" {
		t.Fatalf("parseGoImportsPaths = %+v, want one diagnostic for pkg/file.go", diags)
	}
	if !strings.Contains(diags[0].Message, goImports) {
		t.Errorf("message = %q, want it to name %q", diags[0].Message, goImports)
	}
}

// TestSanityParseStaticcheckJSONAttributesToStaticcheck checks a staticcheck
// finding carries its own check code and names staticcheck in its message,
// distinct from a go vet finding in the same vet run (OD46).
func TestSanityParseStaticcheckJSONAttributesToStaticcheck(t *testing.T) {
	line := `{"code":"SA4006","severity":"error","location":{"file":"main.go","line":12},"message":"value never used"}`
	diags := parseStaticcheckJSON([]byte(line))
	if len(diags) != 1 {
		t.Fatalf("parseStaticcheckJSON = %+v, want 1 diagnostic", diags)
	}
	d := diags[0]
	if d.Code != "SA4006" || d.File != "main.go" || d.Line != 12 {
		t.Errorf("diagnostic = %+v, want code SA4006 at main.go:12", d)
	}
	if !strings.Contains(d.Message, staticcheck) {
		t.Errorf("message = %q, want it to name %q", d.Message, staticcheck)
	}
}

// TestSanityParseFileLineMessagesAttributesToGivenTool checks go vet's
// file:line:col output is tagged with whatever tool name the caller passes,
// so the same parser serves go vet and a compile failure ahead of a test run
// alike, each keeping its own attribution.
func TestSanityParseFileLineMessagesAttributesToGivenTool(t *testing.T) {
	diags := parseFileLineMessages([]byte("./main.go:10:2: undefined: foo\n"), goVet)
	if len(diags) != 1 {
		t.Fatalf("parseFileLineMessages = %+v, want 1 diagnostic", diags)
	}
	if diags[0].File != "./main.go" || diags[0].Line != 10 {
		t.Errorf("diagnostic = %+v, want ./main.go:10", diags[0])
	}
	if !strings.Contains(diags[0].Message, goVet) {
		t.Errorf("message = %q, want it to name %q", diags[0].Message, goVet)
	}
}

// TestSanityParseGosecJSONSeverityMapping checks HIGH and MEDIUM gosec
// findings classify as errors, LOW classifies as a warning, and each keeps
// its rule ID as Code.
func TestSanityParseGosecJSONSeverityMapping(t *testing.T) {
	report := `{"Issues":[
		{"severity":"HIGH","rule_id":"G401","details":"weak crypto","file":"a.go","line":"1"},
		{"severity":"LOW","rule_id":"G304","details":"file path from variable","file":"b.go","line":"2"}
	]}`
	diags := parseGosecJSON([]byte(report))
	if len(diags) != 2 {
		t.Fatalf("parseGosecJSON = %+v, want 2 diagnostics", diags)
	}
	if diags[0].Code != "G401" || diags[0].Severity != SeverityError {
		t.Errorf("HIGH finding = %+v, want code G401 and error severity", diags[0])
	}
	if diags[1].Code != "G304" || diags[1].Severity != SeverityWarning {
		t.Errorf("LOW finding = %+v, want code G304 and warning severity", diags[1])
	}
}

// TestSanityParseGovulncheckStreamDedupesByOSV checks two finding messages on
// the same vulnerable OSV ID — one per call-stack frame on the way to the
// vulnerable symbol — collapse into a single diagnostic.
func TestSanityParseGovulncheckStreamDedupesByOSV(t *testing.T) {
	stream := `{"finding":{"osv":"GO-2024-0001","trace":[{"package":"example.com/dep"}]}}
{"finding":{"osv":"GO-2024-0001","trace":[{"package":"example.com/dep/inner"}]}}`
	diags := parseGovulncheckStream([]byte(stream))
	if len(diags) != 1 {
		t.Fatalf("parseGovulncheckStream = %+v, want 1 deduplicated diagnostic", diags)
	}
	if diags[0].Code != "GO-2024-0001" {
		t.Errorf("Code = %q, want GO-2024-0001", diags[0].Code)
	}
}

// TestSanityParseGoTestFailuresRecognizesFailAndCompileError checks a failing
// test's "--- FAIL:" line and a compile error ahead of any test running both
// yield a diagnostic tagged with the given tool, so a broken build never
// reads as a silently clean test run.
func TestSanityParseGoTestFailuresRecognizesFailAndCompileError(t *testing.T) {
	out := []byte("--- FAIL: TestAdd\n./main.go:5:1: missing return\n")
	diags := parseGoTestFailures(out, gotestsum)
	if len(diags) != 2 {
		t.Fatalf("parseGoTestFailures = %+v, want 2 diagnostics", diags)
	}
	if !strings.Contains(diags[0].Message, "TestAdd") {
		t.Errorf("first diagnostic = %+v, want it to name TestAdd", diags[0])
	}
	if diags[1].File != "./main.go" || diags[1].Line != 5 {
		t.Errorf("second diagnostic = %+v, want ./main.go:5", diags[1])
	}
}

// TestSanityFallbackDiagnosticNamesToolAndExitCode checks the synthetic
// fallback a multi-tool check reports for an unparsed non-zero exit names
// both the tool that produced it and the exit code it saw.
func TestSanityFallbackDiagnosticNamesToolAndExitCode(t *testing.T) {
	d := fallbackDiagnostic(gosec, 2)
	if d.Severity != SeverityError {
		t.Errorf("severity = %q, want error", d.Severity)
	}
	if !strings.Contains(d.Message, gosec) {
		t.Errorf("message = %q, want it to name %q", d.Message, gosec)
	}
}
