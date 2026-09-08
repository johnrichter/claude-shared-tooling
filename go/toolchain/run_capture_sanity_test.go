package toolchain

import (
	"context"
	"strings"
	"testing"
)

// captureAdapter is a fake in-process adapter that spawns one real command
// through runTool — writing to both stdout and stderr and exiting non-zero —
// then returns one error diagnostic. It stands in for the multi-tool adapters
// (cargo, python, shell, workflow) that route in-process and parse their own
// tools' output: what matters here is only that its work goes through runTool,
// the one spawn point Run's inProcessCapture records.
type captureAdapter struct{ language string }

func (a captureAdapter) Language() string  { return a.language }
func (a captureAdapter) Route(Check) Route { return RouteInProcess }
func (a captureAdapter) Tool(Check) string { return "capture-analysis" }
func (captureAdapter) Command(Check) ([]string, error) {
	return nil, errUnsupportedCheck("capture-analysis", CheckLint)
}
func (captureAdapter) Parse(int, []byte, []byte) ([]Diagnostic, error) { return nil, nil }

func (a captureAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	if _, err := runTool(ctx, target.Dir, "sh", []string{"-c", "printf 'OUT-MARKER\\n'; printf 'ERR-MARKER\\n' >&2; exit 3"}); err != nil {
		return nil, err
	}
	return []Diagnostic{{Severity: SeverityError, Message: "capture probe finding"}}, nil
}

// TestSanityInProcessLogCarriesSpawnedToolOutput proves the capture fix: an
// in-process check's log record carries the raw stdout and stderr its spawned
// tool produced, rather than the empty strings the route wrote before. This is
// the class the failure-path log exists to triage — a check that failed with a
// tool's output the adapter turned into no per-line diagnostic.
func TestSanityInProcessLogCarriesSpawnedToolOutput(t *testing.T) {
	a := captureAdapter{language: "capture-sanity"}
	Register(a)

	res, err := Run(context.Background(), Target{Language: a.language, Check: CheckLint, Dir: t.TempDir()},
		Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	logged := readLogDetail(t, res.LogRef)
	if !strings.Contains(logged.Stdout, "OUT-MARKER") {
		t.Errorf("log stdout = %q, want it to carry the tool's stdout", logged.Stdout)
	}
	if !strings.Contains(logged.Stderr, "ERR-MARKER") {
		t.Errorf("log stderr = %q, want it to carry the tool's stderr", logged.Stderr)
	}
	// The argv is the only record of what ran, since the command field stays
	// empty on the in-process route.
	if !strings.Contains(logged.Stderr, "$ sh -c") {
		t.Errorf("log stderr = %q, want it headed by the spawned argv", logged.Stderr)
	}
}
