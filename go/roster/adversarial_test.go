package roster

import (
	"errors"
	"testing"
)

// Test-engineer adversarial suite: targets acceptance criteria and failure modes not already
// exercised by roster_test.go / sync_test.go. Written against the contract, not the
// implementation — every case is derivable from model-roster.schema.json's invariants or the
// task's acceptance criteria without reading roster.go's internals.

func TestCompareSameFamilyPrefixRanksBelowExtension(t *testing.T) {
	// schema invariant (2): [5] outranks [4,8]; also check the direct prefix case [5] vs [5,1].
	c, err := Compare("claude-sonnet-5", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("Compare(sonnet-5, sonnet-4-6): %v", err)
	}
	if c != 1 {
		t.Errorf("Compare(sonnet-5, sonnet-4-6) = %d, want 1", c)
	}
	// Reversed args invert the sign.
	c2, err := Compare("claude-sonnet-4-6", "claude-sonnet-5")
	if err != nil {
		t.Fatalf("Compare(sonnet-4-6, sonnet-5): %v", err)
	}
	if c2 != -1 {
		t.Errorf("Compare(sonnet-4-6, sonnet-5) = %d, want -1", c2)
	}
}

func TestCompareSameIDIsEqual(t *testing.T) {
	c, err := Compare("claude-opus-5", "claude-opus-5")
	if err != nil {
		t.Fatalf("Compare(x, x): %v", err)
	}
	if c != 0 {
		t.Errorf("Compare(x, x) = %d, want 0", c)
	}
}

func TestCompareCrossFamilyBothDeclaredOrders(t *testing.T) {
	// claude-sonnet-4-6 (cross_family_rank 1) vs claude-haiku-4-5 (rank 0): declared on both
	// sides, sourced from _cross_family_order.inherited_pairs.
	c, err := Compare("claude-sonnet-4-6", "claude-haiku-4-5")
	if err != nil {
		t.Fatalf("Compare(sonnet-4-6, haiku-4-5): %v", err)
	}
	if c != 1 {
		t.Errorf("Compare(sonnet-4-6, haiku-4-5) = %d, want 1 (rank 1 > rank 0)", c)
	}
}

func TestCompareCrossFamilyBothUndeclaredErrors(t *testing.T) {
	// claude-sonnet-4-5 and claude-opus-4-5 both declare cross_family_rank: null.
	_, err := Compare("claude-sonnet-4-5", "claude-opus-4-5")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Compare(sonnet-4-5, opus-4-5) error = %v, want *StaleError", err)
	}
}

func TestCompareOneSideDeclaredOneUndeclaredErrors(t *testing.T) {
	// claude-opus-5 has no cross_family_rank; claude-sonnet-5 does. One-sided declaration must
	// still be roster-stale per invariant (3): "defined only when BOTH rows declare one".
	_, err := Compare("claude-opus-5", "claude-sonnet-5")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Compare(opus-5, sonnet-5) error = %v, want *StaleError", err)
	}
}

func TestCompareUnknownIDPropagatesStaleNotDefaultOrdering(t *testing.T) {
	_, err := Compare("claude-nonexistent-9", "claude-opus-5")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Compare(unknown, known) error = %v, want *StaleError", err)
	}
	_, err2 := Compare("claude-opus-5", "claude-nonexistent-9")
	if !errors.As(err2, &stale) {
		t.Fatalf("Compare(known, unknown) error = %v, want *StaleError", err2)
	}
}

func TestEffortAvailableUnknownIDIsRosterStale(t *testing.T) {
	_, err := EffortAvailable("claude-nonexistent-9")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("EffortAvailable(unknown) error = %v, want *StaleError", err)
	}
}

func TestEffortAvailableNonExemptRowReturnsLiteralList(t *testing.T) {
	// claude-haiku-4-5 is not effort_exempt and lists only low/medium/high.
	efforts, err := EffortAvailable("claude-haiku-4-5")
	if err != nil {
		t.Fatalf("EffortAvailable(haiku-4-5): %v", err)
	}
	if len(efforts) != 3 {
		t.Fatalf("EffortAvailable(haiku-4-5) = %v, want 3 levels (low/medium/high)", efforts)
	}
	for _, e := range efforts {
		if e == EffortXHigh || e == EffortMax {
			t.Errorf("EffortAvailable(haiku-4-5) contains %q, want narrower than AllEfforts", e)
		}
	}
}

func TestPriceNoSourcedRateIsRosterStale(t *testing.T) {
	// claude-sonnet-4-5: contract and list both null (carried for allowlist parity only).
	_, err := Price("claude-sonnet-4-5")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Price(sonnet-4-5) error = %v, want *StaleError", err)
	}
}

func TestSelectableLegacyPinOnly(t *testing.T) {
	sel, err := Selectable("claude-opus-4-5")
	if err != nil {
		t.Fatalf("Selectable(opus-4-5): %v", err)
	}
	if sel != SelectableLegacyPinOnly {
		t.Errorf("Selectable(opus-4-5) = %q, want legacy-pin-only", sel)
	}
}

func TestLifecycleAndSelectableAreIndependentAxes(t *testing.T) {
	// claude-opus-4-8 is vendor-legacy-tabled but still lifecycle "active" and selectable
	// "new-work": schema invariant (6), lifecycle != selectable derivation.
	life, err := Lifecycle("claude-opus-4-8")
	if err != nil {
		t.Fatalf("Lifecycle(opus-4-8): %v", err)
	}
	sel, err := Selectable("claude-opus-4-8")
	if err != nil {
		t.Fatalf("Selectable(opus-4-8): %v", err)
	}
	if life != LifecycleActive || sel != SelectableNewWork {
		t.Errorf("Lifecycle/Selectable(opus-4-8) = %q/%q, want active/new-work", life, sel)
	}
}

func TestLookupSentinelNeverFallsBackToStaleOrModel(t *testing.T) {
	_, err := Lookup("inherit")
	var stale *StaleError
	if errors.As(err, &stale) {
		t.Fatalf("Lookup(inherit) = *StaleError, must be *SentinelError — collapsing 'sentinel' into 'stale' is the exact defect this task guards against")
	}
	var sentinel *SentinelError
	if !errors.As(err, &sentinel) {
		t.Fatalf("Lookup(inherit) error = %v, want *SentinelError", err)
	}
}

func TestUnknownAliasIsNotTreatedAsSentinel(t *testing.T) {
	// "opus" (bare family alias, no generation) is neither a row nor a declared sentinel: must
	// resolve StaleError, not SentinelError and not a silent pass.
	_, err := Lookup("opus")
	var sentinel *SentinelError
	if errors.As(err, &sentinel) {
		t.Fatalf("Lookup(opus) = *SentinelError, want *StaleError (alias is not a declared sentinel)")
	}
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Lookup(opus) error = %v, want *StaleError", err)
	}
}

func TestEmptyStringIDIsRosterStaleNotPanic(t *testing.T) {
	_, err := Lookup("")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Lookup(\"\") error = %v, want *StaleError", err)
	}
}

func TestSentinelErrorMessageNamesSentinel(t *testing.T) {
	err := &SentinelError{ID: "inherit"}
	if !contains(err.Error(), "inherit") {
		t.Errorf("SentinelError.Error() = %q, must name the sentinel", err.Error())
	}
}

func TestPackagingDefectErrorUnwrapsAndMessages(t *testing.T) {
	inner := errors.New("boom")
	err := &PackagingDefectError{Err: inner}
	if !errors.Is(err, inner) {
		t.Error("PackagingDefectError does not unwrap to its inner error")
	}
	if !contains(err.Error(), "boom") {
		t.Errorf("PackagingDefectError.Error() = %q, must surface the inner cause", err.Error())
	}
}

func TestPackagingDefectNeverSurfacesAsStaleOrSentinel(t *testing.T) {
	// Guards the acceptance criterion directly: a load failure must not be reachable as either
	// of the two legitimate "no answer" outcomes.
	origDoc, origErr := rosterDoc, rosterLoadErr
	defer func() { rosterDoc, rosterLoadErr = origDoc, origErr }()
	rosterDoc, rosterLoadErr = nil, errors.New("simulated load failure")

	_, err := Lookup("claude-opus-5")
	var stale *StaleError
	var sentinel *SentinelError
	if errors.As(err, &stale) {
		t.Fatal("packaging defect surfaced as *StaleError — a build defect must never be mistaken for a decision")
	}
	if errors.As(err, &sentinel) {
		t.Fatal("packaging defect surfaced as *SentinelError")
	}
	var defect *PackagingDefectError
	if !errors.As(err, &defect) {
		t.Fatalf("Lookup with failed load error = %v, want *PackagingDefectError", err)
	}
}

func TestPackagingDefectMissingModelsKeyIsDefect(t *testing.T) {
	// Structurally valid JSON, correct schema version, but the required "models" key is absent
	// entirely (not just empty) — still must not silently decode to a zero-value empty map that
	// then reads as "every ID is stale" without ever surfacing the defect.
	if _, err := parseRoster([]byte(`{"_schema_version":1,"effort_exempt_sentinels":["inherit"]}`)); err == nil {
		t.Error("parseRoster(no models key) = nil error, want a defect")
	}
}

func TestPackagingDefectWrongTopLevelShapeIsDefect(t *testing.T) {
	// Valid JSON but not an object at all — must fail to decode, not silently zero-value.
	if _, err := parseRoster([]byte(`[]`)); err == nil {
		t.Error("parseRoster([]) = nil error, want a defect")
	}
	if _, err := parseRoster([]byte(`"a string"`)); err == nil {
		t.Error("parseRoster(string) = nil error, want a defect")
	}
}

func TestBuiltSchemaVersionExactMatchLoads(t *testing.T) {
	doc, err := parseRoster([]byte(`{"_schema_version":1,"effort_exempt_sentinels":["inherit"],"models":{"claude-x-1":{"family":"x","generation":[1],"selectable":"new-work","lifecycle":"active"}}}`))
	if err != nil {
		t.Fatalf("parseRoster(schema version == built version): %v", err)
	}
	if doc == nil || len(doc.Models) != 1 {
		t.Errorf("parseRoster did not load the one declared model row")
	}
}

func TestPriceContractPreferredOverList(t *testing.T) {
	doc, err := parseRoster([]byte(`{
		"_schema_version":1,
		"effort_exempt_sentinels":["inherit"],
		"models":{
			"claude-x-1":{
				"family":"x","generation":[1],"selectable":"new-work","lifecycle":"active",
				"price":{
					"contract":{"input":1,"output":2,"cache_write_5m":0,"cache_write_1h":0,"cache_read":0},
					"list":{"input":99,"output":99,"cache_write_5m":0,"cache_write_1h":0,"cache_read":0}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parseRoster: %v", err)
	}
	origDoc, origErr := rosterDoc, rosterLoadErr
	defer func() { rosterDoc, rosterLoadErr = origDoc, origErr }()
	rosterDoc, rosterLoadErr = doc, nil

	price, err := Price("claude-x-1")
	if err != nil {
		t.Fatalf("Price(claude-x-1): %v", err)
	}
	if price.Basis != "contract" || price.Input != 1 {
		t.Errorf("Price(claude-x-1) = %+v, want basis contract, input 1 (contract must be preferred over list)", price)
	}
}

func TestNormalizeDatedIDPrecedingWindowSelectorOrder(t *testing.T) {
	// Adversarial: a dated ID with the [1m] selector appended AFTER the date
	// (id-YYYYMMDD[1m]) — the documented order in this library's own doc comment (strip [1m]
	// first, then the date) handles this; confirm it round-trips to the bare key.
	m, err := Lookup("claude-sonnet-5-20260724[1m]")
	if err != nil {
		t.Fatalf("Lookup(dated-then-windowed): %v", err)
	}
	if m.ID != "claude-sonnet-5" {
		t.Errorf("Lookup(dated-then-windowed).ID = %q, want claude-sonnet-5", m.ID)
	}
}

func TestNormalizeWindowThenDateOrderDoesNotResolve(t *testing.T) {
	// Adversarial, informational: the opposite ordering (id[1m]-YYYYMMDD, window selector
	// BEFORE the date suffix) is not stripped by a single trim-then-regex pass, since
	// TrimSuffix(id, "[1m]") only matches when "[1m]" is the literal string end. This id form is
	// not attested by either the schema doc or the vendor's dated-ID convention (dated IDs are
	// vendor snapshot names with no window marker), so this is not a spec violation — recorded
	// so a future dated+windowed convention change is caught here first.
	_, err := Lookup("claude-sonnet-5[1m]-20260724")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Lookup(windowed-then-dated) error = %v, want *StaleError (documents current non-support for this ordering)", err)
	}
}
