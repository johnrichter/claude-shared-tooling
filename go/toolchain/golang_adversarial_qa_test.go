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

// TestQAGoAdapterParseAttributesBuildErrorLinesWithFileAndLine checks a
// build-shaped error line (file:line:col: message) is not mistaken for a
// bare gofmt -l path — it carries a similar .go-looking prefix but with
// trailing diagnostic text and a space — and is instead parsed into its own
// diagnostic naming the failing file and line, one per line, rather than
// collapsing into the generic fallback (AC2: a build failure Parse can
// recognize is never an opaque "see log_ref" placeholder). The "# package"
// header line matches neither shape and is skipped.
func TestQAGoAdapterParseAttributesBuildErrorLinesWithFileAndLine(t *testing.T) {
	a := NewGoAdapter()
	stdout := []byte("./main.go:10:2: undefined: foo\n# example.com/pkg\n./other.go:3:1: missing return\n")
	diags, err := a.Parse(1, stdout, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, d := range diags {
		if d.Message == "not gofmt-formatted" {
			t.Fatalf("Parse misclassified a build error line as a gofmt path: %+v", diags)
		}
	}
	if len(diags) != 2 {
		t.Fatalf("Parse = %+v, want exactly two build-error diagnostics (the header line matches neither shape)", diags)
	}
	if diags[0].File != "./main.go" || diags[0].Line != 10 {
		t.Errorf("diags[0] = %+v, want File=./main.go Line=10", diags[0])
	}
	if diags[1].File != "./other.go" || diags[1].Line != 3 {
		t.Errorf("diags[1] = %+v, want File=./other.go Line=3", diags[1])
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
