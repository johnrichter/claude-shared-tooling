package gate

import "testing"

// TestVerdictStringNamesEachKnownValue checks each defined Verdict stringifies to its own name
// (not just the unknown-value fallback exercised elsewhere), since these branches are otherwise
// only reached inside failing-test error formatting.
func TestVerdictStringNamesEachKnownValue(t *testing.T) {
	cases := map[Verdict]string{
		VerdictAbort:  "abort",
		VerdictSilent: "silent",
		VerdictWarn:   "warn",
	}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", int(v), got, want)
		}
	}
}

// TestPartitionNegativeThresholdAndRanks checks Partition handles negative thresholds and
// negative ranks correctly — the threshold and rank function are caller-supplied with no
// non-negativity assumption baked into the implementation.
func TestPartitionNegativeThresholdAndRanks(t *testing.T) {
	items := []int{-10, -3, 0, 3, 10}
	below, atOrAbove := Partition(items, -3, func(i int) int { return i })
	if len(below) != 1 || below[0] != -10 {
		t.Errorf("below = %v, want only -10 (strictly below threshold -3)", below)
	}
	if len(atOrAbove) != 4 {
		t.Errorf("atOrAbove = %v, want the remaining 4 items", atOrAbove)
	}
}

// TestBandNegativeRankAndBounds checks Band's comparisons hold for negative ranks and bounds,
// confirming no implicit non-negative assumption.
func TestBandNegativeRankAndBounds(t *testing.T) {
	if got := Band(-11, -10, -5); got != VerdictAbort {
		t.Errorf("Band(-11,-10,-5) = %s, want abort", got)
	}
	if got := Band(-7, -10, -5); got != VerdictSilent {
		t.Errorf("Band(-7,-10,-5) = %s, want silent", got)
	}
	if got := Band(-4, -10, -5); got != VerdictWarn {
		t.Errorf("Band(-4,-10,-5) = %s, want warn", got)
	}
}

// TestRung2DeclarationsCarryFailDirectionFromSeedRegistry checks the FailDirection field is
// populated (open or closed) for every declared rung-2 entry, since declared-vs-actual
// verification needs it alongside the trigger.
func TestRung2DeclarationsCarryFailDirectionFromSeedRegistry(t *testing.T) {
	decls, err := Rung2Declarations()
	if err != nil {
		t.Fatalf("Rung2Declarations: %v", err)
	}
	for _, d := range decls {
		if d.FailDirection != "open" && d.FailDirection != "closed" {
			t.Errorf("entry %s has fail_direction %q, want open or closed", d.ID, d.FailDirection)
		}
	}
}

// TestCheckCompletenessEmptyRegistryYieldsNoViolations checks an invariants-less registry
// (distinct from a nil registry, which would panic a naive implementation) produces no
// violations rather than panicking.
func TestCheckCompletenessEmptyRegistryYieldsNoViolations(t *testing.T) {
	v := CheckCompleteness(&Registry{})
	if len(v) != 0 {
		t.Errorf("violations = %+v, want none for an empty registry", v)
	}
}
