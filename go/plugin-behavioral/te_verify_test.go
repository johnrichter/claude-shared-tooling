package plugin_behavioral

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// Test-engineer re-verification pass, targeting the fix applied since the prior review:
// AssertThrowaway wired into runProbeCase immediately before capture. These tests exercise the
// PRODUCTION path (Run -> runProbeCase), not AssertThrowaway in isolation -- sanity_test.go
// already covers the unit itself; this proves the wiring is real and load-bearing on the
// production path, not merely present as dead code.

// TestRun_CwdSeedRemovingMarkerAbortsRunLoud proves a CwdSeed that strips the throwaway marker
// (simulating a seed, or a path swapped out from under the Throwaway, that widens a mutating
// probe's blast radius) is caught right before capture: Run returns an error naming the missing
// marker, never a graded record and never a silent pass.
func TestRun_CwdSeedRemovingMarkerAbortsRunLoud(t *testing.T) {
	opts := baseOptions(t)
	opts.Cases = []Case{{
		ID:     "case-marker-stripped",
		Kind:   KindProbe,
		Prompt: "hello",
		CwdSeed: func(dir string) error {
			return os.Remove(filepath.Join(dir, ThrowawayMarker))
		},
		Classify: ToolInvoked("Bash"),
	}}

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatalf("Run: got nil error, want a loud failure when the throwaway marker is missing before capture")
	}
	if !strings.Contains(err.Error(), ThrowawayMarker) {
		t.Fatalf("Run error = %q, want it to name the missing marker %q", err.Error(), ThrowawayMarker)
	}
}

// TestRun_CwdSeedRemovingMarkerNeverInvokesProbe proves the abort happens strictly before the
// probe subprocess seam is ever called -- the defense-in-depth guard must sit ahead of spend, not
// merely ahead of grading. Without the wiring fix, Provision's own guarantee already keeps the
// marker present at seed time in the non-adversarial case, so this proves the re-check catches a
// seed that actively de-marks the directory, something Provision alone cannot catch since the
// marker write happens before seed runs.
func TestRun_CwdSeedRemovingMarkerNeverInvokesProbe(t *testing.T) {
	invoked := false
	opts := baseOptions(t)
	opts.Run = func(_ context.Context, _ []string, _ sysops.Options) (*sysops.Result, error) {
		invoked = true
		return &sysops.Result{ExitCode: 0, Stdout: []byte(`{}`)}, nil
	}
	opts.Cases = []Case{{
		ID:     "case-marker-stripped-2",
		Kind:   KindProbe,
		Prompt: "hello",
		CwdSeed: func(dir string) error {
			return os.Remove(filepath.Join(dir, ThrowawayMarker))
		},
		Classify: ToolInvoked("Bash"),
	}}

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatalf("Run: got nil error, want a loud failure before the probe ever runs")
	}
	if invoked {
		t.Fatalf("probe runner was invoked despite a missing throwaway marker -- the guard did not fire ahead of spend")
	}
}

// TestRun_HealthyThrowawayStillRunsNormally is the control: a case whose CwdSeed leaves the
// marker intact still runs and grades normally through the AssertThrowaway re-check -- the guard
// must not be a false-positive tripwire on the ordinary path.
func TestRun_HealthyThrowawayStillRunsNormally(t *testing.T) {
	invoked := false
	opts := baseOptions(t)
	opts.Run = func(_ context.Context, args []string, o sysops.Options) (*sysops.Result, error) {
		invoked = true
		if !IsThrowaway(o.Dir) {
			t.Fatalf("probe invoked against a non-throwaway dir %q", o.Dir)
		}
		return fakeProbeRunner("sess-ok", 0.01, "done")(context.Background(), args, o)
	}
	opts.Cases = []Case{{
		ID:       "case-healthy",
		Kind:     KindProbe,
		Prompt:   "hello",
		Classify: ToolInvoked("Bash"),
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v, want a healthy throwaway to run without incident", err)
	}
	if !invoked {
		t.Fatalf("probe runner was never invoked for a healthy throwaway")
	}
	if len(report.Records) != 1 {
		t.Fatalf("Records = %+v, want exactly one", report.Records)
	}
}
