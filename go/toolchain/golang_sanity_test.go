package toolchain

import (
	"context"
	"reflect"
	"testing"
)

// fakeLintDriver is a minimal LintDriver double: it returns whatever
// diagnostics it was constructed with, and records the target it saw.
type fakeLintDriver struct {
	diags []Diagnostic
	err   error
	saw   Target
}

func (f *fakeLintDriver) Lint(_ context.Context, target Target) ([]Diagnostic, error) {
	f.saw = target
	return f.diags, f.err
}

// TestSanityGoAdapterDoesNotSelfRegister checks NewGoAdapter never calls
// Register on go/toolchain's behalf — a caller must register the result
// itself, unlike cargoAdapter's init-time self-registration.
func TestSanityGoAdapterDoesNotSelfRegister(t *testing.T) {
	if _, ok := lookup("go"); ok {
		t.Fatalf(`adapter registered for "go" before any caller registered one`)
	}
}

// TestSanityGoAdapterToolAndCommandTable checks Tool and Command answer
// exactly what SC4a requires for each check.
func TestSanityGoAdapterToolAndCommandTable(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	cases := []struct {
		check   Check
		tool    string
		route   Route
		argv    []string
		wantErr bool
	}{
		{CheckFormat, "gofmt", RouteSubprocess, []string{"-l", "."}, false},
		{CheckBuild, "go", RouteSubprocess, []string{"build"}, false},
		{CheckTest, "go", RouteSubprocess, []string{"test", "./..."}, false},
		{CheckVet, "go", RouteSubprocess, []string{"vet", "./..."}, false},
		{CheckLint, goLintTool, RouteInProcess, nil, true},
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

// TestSanityGoAdapterRunInProcessCallsDriver checks the lint check's
// in-process route dispatches to driver and rejects every other check.
func TestSanityGoAdapterRunInProcessCallsDriver(t *testing.T) {
	driver := &fakeLintDriver{diags: []Diagnostic{{Severity: SeverityError, Message: "boom"}}}
	a := NewGoAdapter(driver)
	target := Target{Language: "go", Check: CheckLint, Dir: "."}
	diags, err := a.RunInProcess(context.Background(), target)
	if err != nil {
		t.Fatalf("RunInProcess(lint): %v", err)
	}
	if len(diags) != 1 || diags[0].Message != "boom" {
		t.Fatalf("RunInProcess(lint) = %+v, want driver's diagnostic", diags)
	}
	if !reflect.DeepEqual(driver.saw, target) {
		t.Fatalf("driver saw %+v, want %+v", driver.saw, target)
	}
	if _, err := a.RunInProcess(context.Background(), Target{Check: CheckBuild}); err == nil {
		t.Fatalf("RunInProcess(build) = nil error, want ErrUnsupportedCheck")
	}
}

// TestSanityGoAdapterParsesGofmtPaths checks Parse turns a gofmt -l sample —
// one unformatted path per line — into one error diagnostic per path, and
// that a clean (empty) run with exit 0 parses to no diagnostics.
func TestSanityGoAdapterParsesGofmtPaths(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
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
