package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
)

// LintDriver performs an in-process Go lint over the packages rooted at a
// target's directory and returns its findings as normalized diagnostics.
// go/toolchain declares this interface naming only its own types (Target,
// Diagnostic) plus the standard library, so the module itself takes on no
// analyzer dependency; language-tools builds a LintDriver on
// golang.org/x/tools/go/analysis and passes it to NewGoAdapter.
type LintDriver interface {
	// Lint analyzes target.Dir and returns its diagnostics, or an error if
	// the analysis itself could not run — a problem it found in the code is
	// a Diagnostic, never an error, the same contract
	// Adapter.RunInProcess documents.
	Lint(ctx context.Context, target Target) ([]Diagnostic, error)
}

// goLintTool is the fixed name RunResult.Tool carries for the Go lint check:
// its analysis runs inside this process (RouteInProcess), so there is no
// spawned binary to name instead.
const goLintTool = "go-analysis"

// goAdapter is the Adapter for Go modules. build, test and vet run through
// the go tool; format runs through gofmt directly, never through `go fmt` —
// `go fmt` forwards to `gofmt -l -w`, which rewrites the tree rather than
// just reporting what needs it. lint runs in-process through driver,
// because every analyzer NewGoAdapter's caller registers ships as a Go
// library rather than a binary.
type goAdapter struct {
	driver LintDriver
}

// NewGoAdapter returns the Adapter for Go modules, routing its lint check
// through driver and every other check through gofmt or the go tool
// directly. go/toolchain registers no Go adapter on its own — the caller
// that supplies driver also calls Register with the result. driver is
// required: an adapter built with a nil one answers every other check
// normally and reports an error from the lint check.
func NewGoAdapter(driver LintDriver) Adapter {
	return goAdapter{driver: driver}
}

func (goAdapter) Language() string { return "go" }

// Route reports the in-process route for lint (its analyzers are Go
// libraries, not binaries) and the subprocess route for every other check.
func (goAdapter) Route(check Check) Route {
	if check == CheckLint {
		return RouteInProcess
	}
	return RouteSubprocess
}

// Tool names gofmt for format, the fixed in-process label for lint, and the
// go tool for build, test and vet.
func (goAdapter) Tool(check Check) string {
	switch check {
	case CheckFormat:
		return "gofmt"
	case CheckLint:
		return goLintTool
	default:
		return "go"
	}
}

// Command returns gofmt's or go's argv for check. format lists rather than
// rewrites (-l, never -w, per Tool's doc above); build, test and vet run
// against the whole module (./... for test and vet) rather than a single
// package.
func (a goAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckFormat:
		return []string{"-l", "."}, nil
	case CheckBuild:
		return []string{"build"}, nil
	case CheckTest:
		return []string{"test", "./..."}, nil
	case CheckVet:
		return []string{"vet", "./..."}, nil
	default:
		return nil, errUnsupportedCheck(a.Tool(check), check)
	}
}

// RunInProcess performs target's lint through driver. Every other check is
// unreachable here, because Route sends it through the subprocess path
// instead. A nil driver — the one way to build this adapter wrong, since
// NewGoAdapter's pinned signature returns no error to reject it at
// construction — is reported as an error here rather than left to panic
// inside Run.
func (a goAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	if target.Check != CheckLint {
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}
	if a.driver == nil {
		return nil, errors.New("toolchain: go adapter has no lint driver: NewGoAdapter requires a non-nil LintDriver")
	}
	return a.driver.Lint(ctx, target)
}

// gofmtUnformattedPathRE matches one line of `gofmt -l .` output: a bare
// relative file path, with none of the "file:line: message" shape a build
// or vet diagnostic carries. The whitespace-free shape is deliberate: it is
// what keeps a prose line ending in a .go word — anything a test under `go
// test ./...` chooses to print — from reading as an unformatted path. The
// cost is a path that itself contains a space, legal but vanishingly rare in
// a Go tree; loosening the shape to admit one would admit that prose too.
var gofmtUnformattedPathRE = regexp.MustCompile(`^\S+\.go$`)

// Parse turns gofmt's raw stdout into diagnostics: gofmt -l prints one
// unformatted path per line and exits 0 regardless of what it found, so
// each printed path becomes its own error diagnostic — without this step an
// unformatted tree would read as a clean run. Output from build, test or
// vet that isn't shaped like a gofmt path falls back to one synthetic
// diagnostic on a non-zero exit, the same fallback every subprocess-routed
// adapter in this package uses for a shape it doesn't specifically parse.
// That fallback names both binaries this adapter fronts, because Parse is
// handed one invocation's output and not the check behind it — gofmt reaches
// it too, exiting non-zero on a file it cannot even parse.
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
			}
		}
	}
	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("gofmt or go exited %d with no parsed diagnostics; see log_ref for raw output", exitCode),
		})
	}
	return diags, nil
}
