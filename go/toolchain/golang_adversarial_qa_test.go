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
	a := NewGoAdapter()
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
// in-process and Tool answers the fixed golangci-lint+goimports composite
// there, independent of what target it is asked about.
func TestQAGoAdapterLintReportsInProcessRouteAndFixedTool(t *testing.T) {
	a := NewGoAdapter()
	if got := a.Route(CheckLint); got != RouteInProcess {
		t.Fatalf("Route(lint) = %q, want in-process", got)
	}
	if got := a.Tool(CheckLint); got != lintResultTool {
		t.Fatalf("Tool(lint) = %q, want %q", got, lintResultTool)
	}
}

// TestQAGoAdapterParseDoesNotFalsePositiveOnVetOrBuildErrorLines checks a
// vet/build-shaped error line (file:line:col: message) is not mistaken for
// a bare gofmt -l path, since it carries a similar .go-looking prefix but
// with trailing diagnostic text and a space.
func TestQAGoAdapterParseDoesNotFalsePositiveOnVetOrBuildErrorLines(t *testing.T) {
	a := NewGoAdapter()
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
	a := NewGoAdapter()
	diags, err := a.Parse(0, []byte("dirty.go\n"), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(diags) != 1 || diags[0].Severity != SeverityError {
		t.Fatalf("Parse(exit=0, dirty.go) = %+v, want one error diagnostic despite exit 0", diags)
	}
}

// TestQAGoAdapterRunInProcessRejectsSubprocessOnlyChecks checks the two
// checks Route sends through the subprocess path (build, format) are
// rejected by RunInProcess with the ErrUnsupportedCheck sentinel — a direct
// caller bypassing Route must still be refused correctly rather than
// silently getting an empty result.
func TestQAGoAdapterRunInProcessRejectsSubprocessOnlyChecks(t *testing.T) {
	a := NewGoAdapter()
	for _, c := range []Check{CheckFormat, CheckBuild} {
		_, err := a.RunInProcess(context.Background(), Target{Check: c})
		if err == nil {
			t.Fatalf("RunInProcess(%s) = nil error, want ErrUnsupportedCheck", c)
		}
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Fatalf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestQAGoAdapterCommandUnsupportedCheckMatchesSentinel checks Command's
// error for a check routed in-process (never reached through Run's
// subprocess path) satisfies errors.Is against ErrUnsupportedCheck, matching
// the contract every other adapter's Command upholds.
func TestQAGoAdapterCommandUnsupportedCheckMatchesSentinel(t *testing.T) {
	a := NewGoAdapter()
	_, err := a.Command(CheckLint)
	if !errors.Is(err, ErrUnsupportedCheck) {
		t.Fatalf("Command(lint): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", err)
	}
}
