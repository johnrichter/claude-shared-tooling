package toolchain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// TestSanityPythonAdapterRegistered checks the Python adapter self-registers
// at init, the same contract cargoAdapter documents for the rust key.
func TestSanityPythonAdapterRegistered(t *testing.T) {
	if _, ok := lookup("python"); !ok {
		t.Fatalf(`no adapter registered for "python"`)
	}
}

// TestSanityPythonAdapterToolAndCommandTable checks Route and Tool answer
// correctly for all six checks, and Command answers an argv for the
// subprocess-routed ones (build, format, lint) and ErrUnsupportedCheck for
// the two that instead route in-process (security, test). vet is checked
// separately (TestSanityPythonAdapterVetCommandReadsLanguageToolsConfig)
// since its argv carries a path Command writes fresh on every call.
func TestSanityPythonAdapterToolAndCommandTable(t *testing.T) {
	a := pythonAdapter{}
	cases := []struct {
		check   Check
		tool    string
		route   Route
		argv    []string
		wantErr bool
	}{
		{CheckBuild, "uv", RouteSubprocess, []string{"build"}, false},
		{CheckFormat, "ruff", RouteSubprocess, []string{"format", "--check"}, false},
		{CheckLint, "ruff", RouteSubprocess, []string{"check"}, false},
		{CheckSecurity, "bandit", RouteInProcess, nil, true},
		{CheckTest, "pytest", RouteInProcess, nil, true},
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

// TestSanityPythonAdapterVetToolAndRoute checks vet names mypy (OD29) and
// routes through the subprocess path like build/format/lint.
func TestSanityPythonAdapterVetToolAndRoute(t *testing.T) {
	a := pythonAdapter{}
	if got := a.Tool(CheckVet); got != "mypy" {
		t.Errorf("Tool(vet) = %q, want mypy", got)
	}
	if got := a.Route(CheckVet); got != RouteSubprocess {
		t.Errorf("Route(vet) = %q, want subprocess", got)
	}
}

// TestSanityPythonAdapterVetCommandReadsLanguageToolsConfig checks vet's argv
// points mypy's --config-file at the language-tools tree (SC37) rather than
// letting mypy search upward for a caller's own mypy.ini, setup.cfg or
// pyproject.toml [tool.mypy] section.
func TestSanityPythonAdapterVetCommandReadsLanguageToolsConfig(t *testing.T) {
	a := pythonAdapter{}
	argv, err := a.Command(CheckVet)
	if err != nil {
		t.Fatalf("Command(vet): %v", err)
	}
	if len(argv) != 3 || argv[0] != "--config-file" || argv[2] != "." {
		t.Fatalf("Command(vet) = %v, want [--config-file <path> .]", argv)
	}
	cfg := argv[1]
	if filepath.Base(cfg) != mypyConfigBase {
		t.Errorf("Command(vet) config = %q, want base name %q", cfg, mypyConfigBase)
	}
	if filepath.Base(filepath.Dir(cfg)) != mypyConfigDir {
		t.Errorf("Command(vet) config = %q, want parent dir %q", cfg, mypyConfigDir)
	}
	if !filepath.IsAbs(cfg) {
		t.Errorf("Command(vet) config = %q, want an absolute path outside any caller's tree", cfg)
	}
}

// TestSanityPythonMypyConfigWrittenToLanguageToolsTree checks mypyConfigPath
// materializes mypyDefaultConfig at its fixed language-tools path, so a
// caller's own mypy.ini or pyproject.toml [tool.mypy] section is never the
// one vet's --config-file resolves to.
func TestSanityPythonMypyConfigWrittenToLanguageToolsTree(t *testing.T) {
	path, err := mypyConfigPath()
	if err != nil {
		t.Fatalf("mypyConfigPath: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mypy config: %v", err)
	}
	if string(content) != mypyDefaultConfig {
		t.Errorf("mypy config content = %q, want %q", content, mypyDefaultConfig)
	}
}

// TestSanityPythonAdapterRunInProcessRejectsSubprocessChecks checks
// RunInProcess rejects build, format, lint and vet (Route sends those
// through the subprocess path instead) with ErrUnsupportedCheck, and never
// reaches a tool spawn to do so.
func TestSanityPythonAdapterRunInProcessRejectsSubprocessChecks(t *testing.T) {
	a := pythonAdapter{}
	for _, c := range []Check{CheckBuild, CheckFormat, CheckLint, CheckVet} {
		_, err := a.RunInProcess(context.Background(), Target{Check: c, Dir: "."})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestSanityPythonAdapterRunTestRejectsUnrecognizedKind checks the test
// pair's subcommand dispatch (runTest) rejects a target.Test outside
// TestUnit/TestE2E — including the zero value (a bare "test" request) and
// benchmark, which MATRIX names for Rust alone — before it ever spawns
// pytest.
func TestSanityPythonAdapterRunTestRejectsUnrecognizedKind(t *testing.T) {
	a := pythonAdapter{}
	for _, kind := range []TestKind{"", TestBenchmark, TestKind("bogus")} {
		_, err := a.RunInProcess(context.Background(), Target{Check: CheckTest, Test: kind, Dir: "."})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(test, kind=%q): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", kind, err)
		}
	}
}

// TestSanityPythonAdapterParsesRuffAndMypyOutput checks Parse turns a ruff
// check violation, a ruff format violation, and a mypy finding into their own
// diagnostics, and that a clean exit-0 run with no matching shape parses to
// nothing. Parse never sees pytest's own output — the test pair routes
// in-process through parsePytestFailures instead (its own sanity test
// below).
func TestSanityPythonAdapterParsesRuffAndMypyOutput(t *testing.T) {
	a := pythonAdapter{}

	lint := "F401 [*] `os` imported but unused\n --> src/app.py:1:8\n\nFound 1 error.\n"
	diags, err := a.Parse(1, []byte(lint), nil)
	if err != nil {
		t.Fatalf("Parse(lint): %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "F401" || diags[0].File != "src/app.py" || diags[0].Line != 1 {
		t.Fatalf("Parse(lint) = %+v, want one F401 diagnostic at src/app.py:1", diags)
	}

	format := "unformatted: File would be reformatted\n --> src/app.py:1:1\n  |\n  - x=1\n1 + x = 1\n  |\n\n1 file would be reformatted\n"
	diags, err = a.Parse(1, []byte(format), nil)
	if err != nil {
		t.Fatalf("Parse(format): %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "" || diags[0].File != "src/app.py" || diags[0].Line != 1 {
		t.Fatalf("Parse(format) = %+v, want one unformatted diagnostic at src/app.py:1", diags)
	}

	vet := "src/app.py:3: error: Incompatible return value type [return-value]\n"
	diags, err = a.Parse(1, []byte(vet), nil)
	if err != nil {
		t.Fatalf("Parse(vet): %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "return-value" || diags[0].File != "src/app.py" || diags[0].Line != 3 {
		t.Fatalf("Parse(vet) = %+v, want one return-value diagnostic at src/app.py:3", diags)
	}
	if !strings.Contains(diags[0].Message, "mypy") {
		t.Errorf("Parse(vet) message = %q, want it to name mypy", diags[0].Message)
	}

	clean, err := a.Parse(0, nil, nil)
	if err != nil {
		t.Fatalf("Parse(clean): %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("Parse(clean) = %+v, want no diagnostics", clean)
	}
}

// TestSanityParsePytestFailuresRecognizesFailedLines checks the test pair's
// own parser (never Parse, since test routes in-process) turns a pytest
// short-summary FAILED line into one error diagnostic naming the failing
// node ID and reason.
func TestSanityParsePytestFailuresRecognizesFailedLines(t *testing.T) {
	out := []byte("FAILED tests/test_add.py::test_add - assert 1 == 2\n")
	diags := parsePytestFailures(out, nil)
	if len(diags) != 1 {
		t.Fatalf("parsePytestFailures = %+v, want 1 diagnostic", diags)
	}
	if diags[0].Severity != SeverityError || !strings.Contains(diags[0].Message, "test_add") {
		t.Errorf("diagnostic = %+v, want an error naming test_add", diags[0])
	}

	if diags := parsePytestFailures([]byte("1 passed in 0.01s\n"), nil); len(diags) != 0 {
		t.Errorf("parsePytestFailures(clean) = %+v, want no diagnostics", diags)
	}
}

// TestSanityParseBanditJSONSeverityMapping checks HIGH and MEDIUM bandit
// findings classify as errors, LOW classifies as a warning (mirroring
// parseGosecJSON's own split), and each keeps its test ID as Code.
func TestSanityParseBanditJSONSeverityMapping(t *testing.T) {
	report := `{"results":[
		{"filename":"a.py","issue_severity":"HIGH","issue_text":"hardcoded password","line_number":4,"test_id":"B105"},
		{"filename":"b.py","issue_severity":"LOW","issue_text":"assert used","line_number":2,"test_id":"B101"}
	]}`
	diags := parseBanditJSON([]byte(report))
	if len(diags) != 2 {
		t.Fatalf("parseBanditJSON = %+v, want 2 diagnostics", diags)
	}
	if diags[0].Code != "B105" || diags[0].Severity != SeverityError {
		t.Errorf("HIGH finding = %+v, want code B105 and error severity", diags[0])
	}
	if diags[1].Code != "B101" || diags[1].Severity != SeverityWarning {
		t.Errorf("LOW finding = %+v, want code B101 and warning severity", diags[1])
	}
}

// TestSanityPythonSecurityExcludesVenvAndTestDirs checks banditExcludeDirs
// names the project's own virtual environment (so -r's recursion never
// reports a vendored third-party package's own findings as the project's)
// and the test tree (so bandit's B101 assert-used rule never fires on
// pytest's own idiom).
func TestSanityPythonSecurityExcludesVenvAndTestDirs(t *testing.T) {
	for _, want := range []string{"./.venv", "./tests"} {
		if !strings.Contains(banditExcludeDirs, want) {
			t.Errorf("banditExcludeDirs = %q, want it to contain %q", banditExcludeDirs, want)
		}
	}
}

const unformattedPy = "x=1\n"

// TestSanityPythonFormatCheckFailsWithoutWritingFile checks the acceptance
// fixture: a directory with one unformatted file fails `ruff format
// --check` through Run, and the file's bytes on disk are unchanged
// afterward — --check reports what needs reformatting, it never rewrites
// it.
func TestSanityPythonFormatCheckFailsWithoutWritingFile(t *testing.T) {
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff not on PATH")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.py")
	if err := os.WriteFile(path, []byte(unformattedPy), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}

	res, err := Run(context.Background(), Target{Language: "python", Check: CheckFormat, Dir: dir}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusGateNegative {
		t.Fatalf("Status = %q, want gate_negative", res.Status)
	}
	if res.Tool != "ruff" {
		t.Fatalf("Tool = %q, want ruff", res.Tool)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatalf("Diagnostics = %+v, want at least one", res.Diagnostics)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read app.py after run: %v", err)
	}
	if string(after) != unformattedPy {
		t.Fatalf("app.py changed by format check: got %q, want unchanged %q", after, unformattedPy)
	}
}
