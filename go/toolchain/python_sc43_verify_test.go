package toolchain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// TestVerifySC43TruncatedZeroExitStillWarns is an adversarial probe beyond
// the implementer's own suite: SC43 part one names only "the truncation flag
// reads true and the output fails to parse" — it says nothing about exit
// code. A truncated, unparseable stdout with ExitCode 0 must still warn
// (never silently drop, never hit the error fallback), proving
// banditDiagnostics checks StdoutTruncated before ExitCode, not as a
// tiebreaker gated on a non-zero exit.
func TestVerifySC43TruncatedZeroExitStillWarns(t *testing.T) {
	res := &sysops.Result{
		ExitCode:        0,
		Stdout:          []byte(`{"results":[{"filename":"a.py"`),
		StdoutTruncated: true,
	}
	diags := banditDiagnostics(res)
	if len(diags) != 1 {
		t.Fatalf("banditDiagnostics(truncated, exit=0) = %+v, want exactly 1 diagnostic", diags)
	}
	if diags[0].Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning even at exit 0", diags[0].Severity)
	}
}

// TestVerifySC43EmptyTruncatedStdoutWarnsOnce guards against a panic or a
// doubled diagnostic on the degenerate empty-capture case: nothing captured
// at all (Stdout is empty) but the runner still reports truncation.
func TestVerifySC43EmptyTruncatedStdoutWarnsOnce(t *testing.T) {
	res := &sysops.Result{ExitCode: 1, Stdout: nil, StdoutTruncated: true}
	diags := banditDiagnostics(res)
	if len(diags) != 1 || diags[0].Severity != SeverityWarning {
		t.Fatalf("banditDiagnostics(empty truncated stdout) = %+v, want exactly 1 warning", diags)
	}
}

// TestVerifySC43ExcludeDoesNotOverreachOnNearMissNames adversarially checks
// the base-name globs are anchored to bandit's actual naming convention and
// don't accidentally swallow a source file that merely contains "test" as a
// substring rather than matching test_*.py / *_test.py exactly (e.g. a file
// named "latest.py" or "attest.py" must stay reachable by bandit).
func TestVerifySC43ExcludeDoesNotOverreachOnNearMissNames(t *testing.T) {
	requirePythonTool(t, "bandit")
	dir := t.TempDir()
	const insecure = "import subprocess\nsubprocess.call(\"echo hi\", shell=True)\n"
	for _, rel := range []string{"latest.py", "attest.py"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(insecure), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	flagged := banditFlaggedFiles(t, dir, banditExcludeDirs)
	for _, rel := range []string{"./latest.py", "./attest.py"} {
		if !flagged[rel] {
			t.Errorf("banditExcludeDirs excluded %s, want a near-miss name (not test_*.py or *_test.py) to stay flagged", rel)
		}
	}
}
