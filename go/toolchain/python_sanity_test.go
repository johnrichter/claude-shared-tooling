package toolchain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// TestSanityPythonAdapterToolAndCommandTable checks Tool and Command answer
// exactly what the Python adapter's spec requires for each check: uv for
// build and test, ruff for lint and format, and the exact argv per check.
func TestSanityPythonAdapterToolAndCommandTable(t *testing.T) {
	a := pythonAdapter{}
	cases := []struct {
		check   Check
		tool    string
		argv    []string
		wantErr bool
	}{
		{CheckBuild, "uv", []string{"sync", "--locked"}, false},
		{CheckTest, "uv", []string{"run", "pytest"}, false},
		{CheckLint, "ruff", []string{"check"}, false},
		{CheckFormat, "ruff", []string{"format", "--check"}, false},
		{CheckVet, "uv", nil, true},
	}
	for _, c := range cases {
		if got := a.Tool(c.check); got != c.tool {
			t.Errorf("Tool(%s) = %q, want %q", c.check, got, c.tool)
		}
		if got := a.Route(c.check); got != RouteSubprocess {
			t.Errorf("Route(%s) = %q, want subprocess", c.check, got)
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

// TestSanityPythonAdapterRunInProcessRejectsEveryCheck checks RunInProcess
// is unreachable through Run (Route is subprocess-only) and reports
// ErrUnsupportedCheck to a direct caller for every check, including the
// ones Command itself supports.
func TestSanityPythonAdapterRunInProcessRejectsEveryCheck(t *testing.T) {
	a := pythonAdapter{}
	for _, c := range []Check{CheckBuild, CheckTest, CheckLint, CheckFormat, CheckVet} {
		if _, err := a.RunInProcess(context.Background(), Target{Language: "python", Check: c}); !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestSanityPythonAdapterParsesRuffAndPytestOutput checks Parse turns a ruff
// check violation, a ruff format violation, and a pytest short-summary
// failure line into their own diagnostics, and that a clean exit-0 run with
// no matching shape parses to nothing.
func TestSanityPythonAdapterParsesRuffAndPytestOutput(t *testing.T) {
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

	test := "FAILED tests/test_add.py::test_add - assert 1 == 2\n"
	diags, err = a.Parse(1, []byte(test), nil)
	if err != nil {
		t.Fatalf("Parse(test): %v", err)
	}
	if len(diags) != 1 || diags[0].Severity != SeverityError {
		t.Fatalf("Parse(test) = %+v, want one error diagnostic", diags)
	}

	clean, err := a.Parse(0, nil, nil)
	if err != nil {
		t.Fatalf("Parse(clean): %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("Parse(clean) = %+v, want no diagnostics", clean)
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
