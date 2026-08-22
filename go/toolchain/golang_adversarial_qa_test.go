package toolchain

import (
	"context"
	"errors"
	"testing"
)

// TestQAGoAdapterToolNamesGofmtNotGoFmt is adversarial coverage for the
// format check: it must report the tool as "gofmt", never "go fmt" or
// "go" — `go fmt` invokes `gofmt -l -w` and rewrites files, which this
// adapter must never do.
func TestQAGoAdapterToolNamesGofmtNotGoFmt(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	if got := a.Tool(CheckFormat); got != "gofmt" {
		t.Fatalf("Tool(format) = %q, want %q", got, "gofmt")
	}
	if got := a.Tool(CheckFormat); got == "go fmt" || got == "go" {
		t.Fatalf("Tool(format) = %q, must not be go/go fmt", got)
	}
	argv, err := a.Command(CheckFormat)
	if err != nil {
		t.Fatalf("Command(format): %v", err)
	}
	for _, arg := range argv {
		if arg == "-w" {
			t.Fatalf("Command(format) = %v, must never carry -w (rewrites the tree)", argv)
		}
	}
}

// TestQAGoAdapterLintReportsInProcessRouteAndFixedTool checks lint routes
// in-process and Tool answers the fixed "go-analysis" label there,
// independent of whatever driver is supplied.
func TestQAGoAdapterLintReportsInProcessRouteAndFixedTool(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	if got := a.Route(CheckLint); got != RouteInProcess {
		t.Fatalf("Route(lint) = %q, want in-process", got)
	}
	if got := a.Tool(CheckLint); got != "go-analysis" {
		t.Fatalf("Tool(lint) = %q, want go-analysis", got)
	}
}

// TestQAGoAdapterParseDoesNotFalsePositiveOnVetOrBuildErrorLines checks a
// vet/build-shaped error line (file:line:col: message) is not mistaken for
// a bare gofmt -l path, since it carries a similar .go-looking prefix but
// with trailing diagnostic text and a space.
func TestQAGoAdapterParseDoesNotFalsePositiveOnVetOrBuildErrorLines(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	stdout := []byte("./main.go:10:2: undefined: foo\n# example.com/pkg\n./other.go:3:1: missing return\n")
	diags, err := a.Parse(1, stdout, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, d := range diags {
		if d.Message == "not gofmt-formatted" {
			t.Fatalf("Parse misclassified a build/vet error line as a gofmt path: %+v", diags)
		}
	}
	// None of these lines match the bare-path shape, and exit is non-zero,
	// so Parse must fall back to exactly one synthetic diagnostic rather
	// than silently reporting a clean run.
	if len(diags) != 1 {
		t.Fatalf("Parse = %+v, want exactly one synthetic fallback diagnostic", diags)
	}
}

// TestQAGoAdapterParseUnformattedTreeIsNeverAQuietSuccess checks that since
// gofmt -l always exits 0, Parse must still read stdout: exit 0 with
// unformatted paths on stdout yields one error diagnostic per path rather
// than a clean result.
func TestQAGoAdapterParseUnformattedTreeIsNeverAQuietSuccess(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	diags, err := a.Parse(0, []byte("dirty.go\n"), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(diags) != 1 || diags[0].Severity != SeverityError {
		t.Fatalf("Parse(exit=0, dirty.go) = %+v, want one error diagnostic despite exit 0", diags)
	}
}

// TestQAGoAdapterRunInProcessRejectsEveryNonLintCheck checks every check
// other than lint is rejected by RunInProcess with the ErrUnsupportedCheck
// sentinel, since Route only ever sends lint down this path — a caller
// invoking RunInProcess directly for another check is a misuse the
// adapter must still reject correctly.
func TestQAGoAdapterRunInProcessRejectsEveryNonLintCheck(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	for _, c := range []Check{CheckFormat, CheckBuild, CheckTest, CheckVet} {
		_, err := a.RunInProcess(context.Background(), Target{Check: c})
		if err == nil {
			t.Fatalf("RunInProcess(%s) = nil error, want ErrUnsupportedCheck", c)
		}
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Fatalf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestQAGoAdapterDriverErrorPropagates checks a driver-side error (analysis
// itself failed to run, distinct from findings) surfaces as an error from
// RunInProcess rather than being swallowed or turned into a diagnostic.
func TestQAGoAdapterDriverErrorPropagates(t *testing.T) {
	wantErr := errors.New("driver exploded")
	a := NewGoAdapter(&fakeLintDriver{err: wantErr})
	diags, err := a.RunInProcess(context.Background(), Target{Check: CheckLint})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunInProcess error = %v, want %v", err, wantErr)
	}
	if diags != nil {
		t.Fatalf("RunInProcess diagnostics = %+v, want nil alongside driver error", diags)
	}
}

// TestQAGoAdapterNilDriverIsAnErrorNotAPanic checks an adapter built with a
// nil driver — the one misconfiguration NewGoAdapter's pinned signature
// cannot reject at construction — reports the lint check as an error instead
// of panicking through Run, and still answers every other check normally.
func TestQAGoAdapterNilDriverIsAnErrorNotAPanic(t *testing.T) {
	a := NewGoAdapter(nil)
	diags, err := a.RunInProcess(context.Background(), Target{Check: CheckLint, Dir: "."})
	if err == nil {
		t.Fatalf("RunInProcess(lint) with nil driver = %+v, want an error", diags)
	}
	if diags != nil {
		t.Fatalf("RunInProcess(lint) diagnostics = %+v, want nil alongside the error", diags)
	}
	if got := a.Tool(CheckFormat); got != "gofmt" {
		t.Fatalf("Tool(format) with nil driver = %q, want gofmt", got)
	}
}

// TestQAGoAdapterCommandUnsupportedCheckMatchesSentinel checks Command's
// error for an unrecognized check satisfies errors.Is against
// ErrUnsupportedCheck, matching the contract every other adapter's Command
// upholds.
func TestQAGoAdapterCommandUnsupportedCheckMatchesSentinel(t *testing.T) {
	a := NewGoAdapter(&fakeLintDriver{})
	_, err := a.Command(CheckLint)
	if !errors.Is(err, ErrUnsupportedCheck) {
		t.Fatalf("Command(lint): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", err)
	}
}
