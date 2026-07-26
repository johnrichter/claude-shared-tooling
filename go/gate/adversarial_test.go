package gate

import (
	"errors"
	"strings"
	"testing"
)

// --- Band / Verdict edge cases not covered by the implementer's own suite ---

// TestVerdictStringUnknownValue checks an out-of-range Verdict stringifies to name its int.
func TestVerdictStringUnknownValue(t *testing.T) {
	v := Verdict(99)
	if got := v.String(); !strings.Contains(got, "99") {
		t.Errorf("Verdict(99).String() = %q, want it to name the unknown int", got)
	}
}

// TestBandInvertedFloorCeilingNeverSilent checks an inverted band (floor > ceiling) never
// yields silent for any rank.
func TestBandInvertedFloorCeilingNeverSilent(t *testing.T) {
	// floor > ceiling: doc says the collapsed range is empty, so nothing can land silently.
	// rank must resolve to abort or warn, never silent, regardless of caller misconfiguration.
	for rank := -5; rank <= 15; rank++ {
		if got := Band(rank, 10, 5); got == VerdictSilent {
			t.Errorf("Band(%d, 10, 5) = silent, want abort or warn for an inverted/empty band", rank)
		}
	}
}

// TestBandExactFloorEqualsCeiling checks a single-point band: match is silent, either side
// aborts or warns.
func TestBandExactFloorEqualsCeiling(t *testing.T) {
	if got := Band(7, 7, 7); got != VerdictSilent {
		t.Errorf("Band(7,7,7) = %s, want silent (single-point band, rank matches)", got)
	}
	if got := Band(6, 7, 7); got != VerdictAbort {
		t.Errorf("Band(6,7,7) = %s, want abort", got)
	}
	if got := Band(8, 7, 7); got != VerdictWarn {
		t.Errorf("Band(8,7,7) = %s, want warn", got)
	}
}

// --- Partition adversarial cases ---

// TestPartitionEmptyInput checks an empty input yields two empty groups.
func TestPartitionEmptyInput(t *testing.T) {
	below, atOrAbove := Partition([]int{}, 5, func(i int) int { return i })
	if len(below) != 0 || len(atOrAbove) != 0 {
		t.Fatalf("empty input produced below=%v atOrAbove=%v, want both empty", below, atOrAbove)
	}
}

// TestPartitionAllItemsAtExactThreshold checks items exactly at the threshold land in
// atOrAbove, not below.
func TestPartitionAllItemsAtExactThreshold(t *testing.T) {
	items := []int{5, 5, 5}
	below, atOrAbove := Partition(items, 5, func(i int) int { return i })
	if len(below) != 0 {
		t.Errorf("items at threshold went to below: %v, want atOrAbove (>= is at-or-above)", below)
	}
	if len(atOrAbove) != 3 {
		t.Errorf("atOrAbove = %v, want all 3 items", atOrAbove)
	}
}

// TestPartitionDuplicateRanksAllPreserved checks duplicate-valued items are each preserved,
// not deduped.
func TestPartitionDuplicateRanksAllPreserved(t *testing.T) {
	// Lossless means duplicates by value must each be preserved independently (not
	// deduped by a naive set-based implementation).
	items := []string{"a", "a", "b", "b", "b"}
	rankOf := map[string]int{"a": 1, "b": 9}
	below, atOrAbove := Partition(items, 5, func(s string) int { return rankOf[s] })
	if len(below) != 2 || len(atOrAbove) != 3 {
		t.Fatalf("below=%v atOrAbove=%v, want 2 'a's below and 3 'b's at-or-above", below, atOrAbove)
	}
}

// --- Registry error-surface adversarial cases ---

// TestPackagingDefectErrorMessageAndUnwrap checks the error names a packaging defect, wraps
// the inner error, and unwraps for errors.Is.
func TestPackagingDefectErrorMessageAndUnwrap(t *testing.T) {
	inner := errors.New("disk exploded")
	e := &PackagingDefectError{Err: inner}
	if !strings.Contains(e.Error(), "packaging defect") {
		t.Errorf("Error() = %q, want it to name a packaging defect", e.Error())
	}
	if !strings.Contains(e.Error(), "disk exploded") {
		t.Errorf("Error() = %q, want it to wrap the inner error", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is(e, inner) = false, want true via Unwrap")
	}
}

// TestParseRegistryMissingSchemaField checks a registry with no schema field errors rather
// than parsing silently.
func TestParseRegistryMissingSchemaField(t *testing.T) {
	_, err := parseRegistry([]byte(`{"gates":{},"invariants":[{"id":"x","rung":2}]}`))
	if err == nil {
		t.Fatal("registry with no schema field: want an error, not a silent parse")
	}
}

// --- CheckCompleteness adversarial cases: rung-below-2 (>=3) missing reason field must fail,
// rung-4/rung-5 field-by-field, and rungs 1/2 must NOT be flagged for a missing reason field
// (only rung >= 3 requires it).

func buildEntry(id string, rung int, overrides func(*RegistryEntry)) RegistryEntry {
	e := RegistryEntry{ID: id, Rung: rung, ReasonLowerRung: "because"}
	if overrides != nil {
		overrides(&e)
	}
	return e
}

// TestCheckCompletenessRung3MissingReasonFails checks a rung-3 entry missing reason_lower_rung
// yields one violation.
func TestCheckCompletenessRung3MissingReasonFails(t *testing.T) {
	entries := []RegistryEntry{
		buildEntry("r3-bad", 3, func(e *RegistryEntry) { e.ReasonLowerRung = "" }),
	}
	v := CheckCompleteness(&Registry{Invariants: entries})
	if len(v) != 1 {
		t.Fatalf("violations = %+v, want exactly 1 for missing reason_lower_rung on a rung-3 entry", v)
	}
}

// TestCheckCompletenessRung1And2NotRequiredToHaveReason checks rung-1 and rung-2 entries are
// not flagged for a missing reason_lower_rung.
func TestCheckCompletenessRung1And2NotRequiredToHaveReason(t *testing.T) {
	entries := []RegistryEntry{
		{ID: "r1", Rung: 1, ReasonLowerRung: ""},
		{ID: "r2", Rung: 2, ReasonLowerRung: "", Trigger: "t", GateID: "g", Condition: "c"},
	}
	v := CheckCompleteness(&Registry{Invariants: entries})
	if len(v) != 0 {
		t.Errorf("violations = %+v, want none — rung < 3 does not require reason_lower_rung", v)
	}
}

// TestCheckCompletenessRung4EachMissingFieldFlagged checks each missing rung-4 field is
// flagged and a fully populated entry passes.
func TestCheckCompletenessRung4EachMissingFieldFlagged(t *testing.T) {
	floors := map[string]*float64{"x": floatPtr(0.9)}

	cases := []struct {
		name  string
		entry RegistryEntry
	}{
		{"no compliance_floors", buildEntry("r4a", 4, func(e *RegistryEntry) {
			e.MeasurementStatus = "measured"
			e.RegisterEntryID = "reg-1"
		})},
		{"no measurement_status", buildEntry("r4b", 4, func(e *RegistryEntry) {
			e.ComplianceFloors = floors
			e.RegisterEntryID = "reg-1"
		})},
		{"no register_entry_id", buildEntry("r4c", 4, func(e *RegistryEntry) {
			e.ComplianceFloors = floors
			e.MeasurementStatus = "measured"
		})},
	}
	for _, c := range cases {
		v := CheckCompleteness(&Registry{Invariants: []RegistryEntry{c.entry}})
		if len(v) == 0 {
			t.Errorf("%s: no violation reported, want one for the missing rung-4 field", c.name)
		}
	}

	complete := buildEntry("r4-good", 4, func(e *RegistryEntry) {
		e.ComplianceFloors = floors
		e.MeasurementStatus = "measured"
		e.RegisterEntryID = "reg-1"
	})
	if v := CheckCompleteness(&Registry{Invariants: []RegistryEntry{complete}}); len(v) != 0 {
		t.Errorf("fully populated rung-4 entry flagged: %+v, want none", v)
	}
}

// TestCheckCompletenessRung5MissingDocPathFails checks a rung-5 entry missing doc_path fails
// and a populated one passes.
func TestCheckCompletenessRung5MissingDocPathFails(t *testing.T) {
	bad := buildEntry("r5-bad", 5, nil)
	v := CheckCompleteness(&Registry{Invariants: []RegistryEntry{bad}})
	if len(v) != 1 {
		t.Fatalf("violations = %+v, want exactly 1 for missing doc_path on a rung-5 entry", v)
	}
	good := buildEntry("r5-good", 5, func(e *RegistryEntry) { e.DocPath = "docs/x.md" })
	if v := CheckCompleteness(&Registry{Invariants: []RegistryEntry{good}}); len(v) != 0 {
		t.Errorf("fully populated rung-5 entry flagged: %+v, want none", v)
	}
}

func floatPtr(f float64) *float64 { return &f }

// --- Rung2Declarations must exclude rung-1 and rung-3 entries (they route to other
// checkers, not this library) and never claim their trigger.

// TestRung2DeclarationsExcludesOtherRungsFromSeedRegistry checks no non-rung-2 entry is
// exposed by Rung2Declarations.
func TestRung2DeclarationsExcludesOtherRungsFromSeedRegistry(t *testing.T) {
	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	decls, err := Rung2Declarations()
	if err != nil {
		t.Fatalf("Rung2Declarations: %v", err)
	}
	declIDs := map[string]bool{}
	for _, d := range decls {
		declIDs[d.ID] = true
	}
	for _, entry := range reg.Invariants {
		if entry.Rung != 2 && declIDs[entry.ID] {
			t.Errorf("entry %s has rung %d but was exposed by Rung2Declarations", entry.ID, entry.Rung)
		}
	}
}

// --- Seed registry itself must be free of completeness violations per acceptance criterion 6
// (rung-4/5 schema-completeness-checked only) and criterion 7 semantics on the real embed.

// TestSeedRegistryRung4And5EntriesExistAndPass checks the seed registry's rung-4/5 entries
// pass completeness, skipping if none exist.
func TestSeedRegistryRung4And5EntriesExistAndPass(t *testing.T) {
	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	var sawRung4, sawRung5 bool
	for _, e := range reg.Invariants {
		if e.Rung == 4 {
			sawRung4 = true
		}
		if e.Rung == 5 {
			sawRung5 = true
		}
	}
	if !sawRung4 && !sawRung5 {
		t.Skip("seed registry has no rung-4 or rung-5 entries to exercise CheckCompleteness against")
	}
	violations := CheckCompleteness(reg)
	if len(violations) != 0 {
		t.Errorf("seed registry rung-4/5 entries fail completeness: %+v", violations)
	}
}
