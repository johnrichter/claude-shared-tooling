package toolchain

import (
	"context"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// TestAdversarialCacheDoesNotReplayAcrossWarningPolicyReversed checks the
// policy guard symmetrically: AllowWarnings=true first (success, cached),
// then a plain default-policy call on the same unchanged target must
// reclassify as gate_negative (execute), not replay the cached success.
func TestAdversarialCacheDoesNotReplayAcrossWarningPolicyReversed(t *testing.T) {
	dir := writeCrate(t, "cratepolicyfliprev", warningLib)
	cacheDir := t.TempDir()
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}

	first, err := Run(context.Background(), target, Options{LogDir: t.TempDir(), CacheDir: cacheDir, AllowWarnings: true})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Status != clikit.StatusSuccess {
		t.Fatalf("first Status = %q, want success under AllowWarnings", first.Status)
	}

	second, err := Run(context.Background(), target, Options{LogDir: t.TempDir(), CacheDir: cacheDir})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Impact != ImpactExecuted {
		t.Fatalf("second Impact = %q, want executed (policy reverted to default, cached success must not be replayed)", second.Impact)
	}
	if second.Status != clikit.StatusGateNegative {
		t.Fatalf("second Status = %q, want gate_negative under default policy", second.Status)
	}
}

// TestAdversarialSameContentSamePolicyStillReplays is the control for the
// two policy-flip tests above: confirms the guard only breaks the cache hit
// on an actual policy change, not on every call (a broken guard that always
// misses would make TestAdversarialCacheDoesNotReplayAcrossWarningPolicy
// pass for the wrong reason).
func TestAdversarialSameContentSamePolicyStillReplays(t *testing.T) {
	dir := writeCrate(t, "cratepolicystable", warningLib)
	cacheDir := t.TempDir()
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}
	opts := Options{LogDir: t.TempDir(), CacheDir: cacheDir, AllowWarnings: true}

	first, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Impact != ImpactExecuted {
		t.Fatalf("first Impact = %q, want executed", first.Impact)
	}

	second, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Impact != ImpactSkippedNoChange {
		t.Fatalf("second Impact = %q, want skipped_no_change under an unchanged policy and content", second.Impact)
	}
	if second.Status != first.Status {
		t.Fatalf("cached verdict Status = %q, want %q", second.Status, first.Status)
	}
}

// TestAdversarialDiagnosticsExactlyAtCapNoOverflow checks the boundary at
// precisely MaxDiagnostics: no overflow should be reported when the count
// equals, not exceeds, the cap.
func TestAdversarialDiagnosticsExactlyAtCapNoOverflow(t *testing.T) {
	dir := writeCrate(t, "cratecapboundary", manyWarningsLib(MaxDiagnostics))
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: t.TempDir(), AllowWarnings: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Overflow != 0 {
		t.Fatalf("Overflow = %d, want 0 at exactly the cap", res.Overflow)
	}
	if len(res.Diagnostics) != MaxDiagnostics {
		t.Fatalf("len(Diagnostics) = %d, want %d", len(res.Diagnostics), MaxDiagnostics)
	}
}
