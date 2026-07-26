package gate

import (
	"errors"
	"testing"
)

// TestBandBoundaries checks the five band positions: below floor, at floor, mid-band, at
// ceiling, above ceiling.
func TestBandBoundaries(t *testing.T) {
	cases := []struct {
		rank int
		want Verdict
	}{
		{4, VerdictAbort},
		{5, VerdictSilent},
		{7, VerdictSilent},
		{10, VerdictSilent},
		{11, VerdictWarn},
	}
	for _, c := range cases {
		if got := Band(c.rank, 5, 10); got != c.want {
			t.Errorf("Band(%d, 5, 10) = %s, want %s", c.rank, got, c.want)
		}
	}
}

// TestPartitionIsTotalLosslessExactlyOnce checks every item lands in exactly one group,
// counts are conserved, and each group's members respect the threshold.
func TestPartitionIsTotalLosslessExactlyOnce(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	below, atOrAbove := Partition(items, 5, func(i int) int { return i })
	if len(below)+len(atOrAbove) != len(items) {
		t.Fatalf("lost or duplicated items: below=%v atOrAbove=%v", below, atOrAbove)
	}
	seen := map[int]int{}
	for _, i := range append(append([]int{}, below...), atOrAbove...) {
		seen[i]++
	}
	for _, i := range items {
		if seen[i] != 1 {
			t.Errorf("item %d placed %d times, want exactly once", i, seen[i])
		}
	}
	for _, i := range below {
		if i >= 5 {
			t.Errorf("below contains %d, which is not below threshold 5", i)
		}
	}
	for _, i := range atOrAbove {
		if i < 5 {
			t.Errorf("atOrAbove contains %d, which is below threshold 5", i)
		}
	}
}

// TestEvaluateDimensionUndetectableWarnsLoudly checks a nil-rank dimension returns warn with
// ok=false and warning text, never a silent pass.
func TestEvaluateDimensionUndetectableWarnsLoudly(t *testing.T) {
	verdict, ok, warning := EvaluateDimension(Dimension{Name: "coverage", Rank: nil}, 5, 10)
	if ok {
		t.Fatalf("ok = true for an undetectable dimension, want false")
	}
	if verdict != VerdictWarn {
		t.Errorf("verdict = %s, want %s", verdict, VerdictWarn)
	}
	if warning == "" {
		t.Errorf("undetectable dimension produced no warning text")
	}
}

// TestEvaluateDimensionDetectableRoutesThroughBand checks a measured rank resolves through
// Band with ok=true and no warning.
func TestEvaluateDimensionDetectableRoutesThroughBand(t *testing.T) {
	rank := 3
	verdict, ok, warning := EvaluateDimension(Dimension{Name: "coverage", Rank: &rank}, 5, 10)
	if !ok || warning != "" {
		t.Fatalf("detectable dimension reported ok=%v warning=%q, want ok=true, no warning", ok, warning)
	}
	if verdict != VerdictAbort {
		t.Errorf("verdict = %s, want %s", verdict, VerdictAbort)
	}
}

// TestLoadRegistryRung2DeclarationsAndCompleteness checks the seed registry yields rung-2
// declarations with trigger and gate_id and passes completeness with zero violations.
func TestLoadRegistryRung2DeclarationsAndCompleteness(t *testing.T) {
	decls, err := Rung2Declarations()
	if err != nil {
		t.Fatalf("Rung2Declarations: %v", err)
	}
	if len(decls) == 0 {
		t.Fatal("expected at least one rung-2 declaration in the seed registry")
	}
	for _, d := range decls {
		if d.Trigger == "" || d.GateID == "" {
			t.Errorf("rung-2 entry %s missing trigger or gate_id", d.ID)
		}
	}

	violations, err := CheckRegistryCompleteness()
	if err != nil {
		t.Fatalf("CheckRegistryCompleteness: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("seed registry has %d completeness violation(s), want 0: %+v", len(violations), violations)
	}
}

// TestParseRegistryReportsPackagingDefectNeverAVerdict checks empty, corrupt, and
// invariant-less inputs error out and a simulated load failure surfaces as a
// PackagingDefectError from every entry point, never a pass or fail.
func TestParseRegistryReportsPackagingDefectNeverAVerdict(t *testing.T) {
	if _, err := parseRegistry(nil); err == nil {
		t.Fatal("empty registry data: want an error")
	}
	if _, err := parseRegistry([]byte("{not json")); err == nil {
		t.Fatal("corrupt registry data: want an error")
	}
	if _, err := parseRegistry([]byte(`{"schema":"x","gates":{},"invariants":[]}`)); err == nil {
		t.Fatal("registry with no invariants: want an error")
	}

	registryDoc, registryLoadErr = nil, errors.New("simulated packaging defect")
	defer func() { registryDoc, registryLoadErr = parseRegistry(embeddedRegistryJSON) }()

	if _, err := LoadRegistry(); err == nil {
		t.Fatal("LoadRegistry with a simulated load error: want an error")
	} else if _, ok := err.(*PackagingDefectError); !ok {
		t.Errorf("LoadRegistry error type = %T, want *PackagingDefectError", err)
	}
	if _, err := Rung2Declarations(); err == nil {
		t.Fatal("Rung2Declarations after a packaging defect: want an error, not a verdict")
	}
	if _, err := CheckRegistryCompleteness(); err == nil {
		t.Fatal("CheckRegistryCompleteness after a packaging defect: want an error, not a verdict")
	}
}
