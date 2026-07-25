package roster

import (
	"errors"
	"testing"
)

// Sanity coverage for the load-time invariants and the public API's happy paths. The
// adversarial/CI suite lives with the test-engineer's stage.

func TestKnownIDResolvesEveryField(t *testing.T) {
	m, err := Lookup("claude-opus-5")
	if err != nil {
		t.Fatalf("Lookup(claude-opus-5): %v", err)
	}
	if m.Family != "opus" {
		t.Errorf("Family = %q, want opus", m.Family)
	}
	if len(m.Generation) != 1 || m.Generation[0] != 5 {
		t.Errorf("Generation = %v, want [5]", m.Generation)
	}
	if m.Selectable != SelectableNewWork {
		t.Errorf("Selectable = %q, want new-work", m.Selectable)
	}
	if m.Lifecycle != LifecycleActive {
		t.Errorf("Lifecycle = %q, want active", m.Lifecycle)
	}
	if m.Price.List == nil || m.Price.List.Input != 5.0 {
		t.Errorf("Price.List = %+v, want input 5.0", m.Price.List)
	}
}

func TestUnknownIDIsRosterStaleNamingRefresh(t *testing.T) {
	_, err := Lookup("claude-nonexistent-9")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Lookup(unknown) error = %v, want *StaleError", err)
	}
	if !containsRefreshAction(stale.Error()) {
		t.Errorf("StaleError message %q does not name the refresh action", stale.Error())
	}
}

func containsRefreshAction(msg string) bool {
	return contains(msg, "refresh") && contains(msg, "model-roster.json")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestInheritResolvesAsSentinel(t *testing.T) {
	efforts, err := EffortAvailable("inherit")
	if err != nil {
		t.Fatalf("EffortAvailable(inherit): %v", err)
	}
	if len(efforts) != len(AllEfforts) {
		t.Errorf("EffortAvailable(inherit) = %v, want AllEfforts", efforts)
	}
	_, lookupErr := Lookup("inherit")
	var sentinel *SentinelError
	if !errors.As(lookupErr, &sentinel) {
		t.Errorf("Lookup(inherit) error = %v, want *SentinelError", lookupErr)
	}
}

func TestDatedTranscriptIDNormalizesToBareForm(t *testing.T) {
	dated, err := Lookup("claude-haiku-4-5-20251001")
	if err != nil {
		t.Fatalf("Lookup(dated): %v", err)
	}
	bare, err := Lookup("claude-haiku-4-5")
	if err != nil {
		t.Fatalf("Lookup(bare): %v", err)
	}
	if dated.ID != bare.ID {
		t.Errorf("dated resolved to %q, want %q", dated.ID, bare.ID)
	}
}

func TestWindowSelectorNormalizes(t *testing.T) {
	m, err := Lookup("claude-sonnet-5[1m]")
	if err != nil {
		t.Fatalf("Lookup(windowed): %v", err)
	}
	if m.ID != "claude-sonnet-5" {
		t.Errorf("ID = %q, want claude-sonnet-5", m.ID)
	}
}

func TestCompareWithinFamilyOrdersNewGeneration(t *testing.T) {
	// claude-opus-5 (generation [5]) outranks claude-opus-4-8 (generation [4,8]): the first
	// differing element decides regardless of length.
	c, err := Compare("claude-opus-5", "claude-opus-4-8")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if c != 1 {
		t.Errorf("Compare(opus-5, opus-4-8) = %d, want 1", c)
	}
}

func TestCompareCrossFamilyMissingRankErrors(t *testing.T) {
	// claude-opus-5 declares no cross_family_rank, so any cross-family pair involving it is
	// roster-stale.
	_, err := Compare("claude-opus-5", "claude-fable-5")
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("Compare(opus-5, fable-5) error = %v, want *StaleError", err)
	}
}

func TestAbsentEmbeddedRosterIsPackagingDefect(t *testing.T) {
	if _, err := parseRoster(nil); err == nil {
		t.Error("parseRoster(nil) = nil error, want a defect")
	}
}

func TestEmptyEmbeddedRosterIsPackagingDefect(t *testing.T) {
	if _, err := parseRoster([]byte{}); err == nil {
		t.Error("parseRoster([]) = nil error, want a defect")
	}
}

func TestCorruptEmbeddedRosterIsPackagingDefect(t *testing.T) {
	if _, err := parseRoster([]byte("{not json")); err == nil {
		t.Error("parseRoster(corrupt) = nil error, want a defect")
	}
	if _, err := parseRoster([]byte(`{"_schema_version":1,"effort_exempt_sentinels":["inherit"],"models":{}}`)); err == nil {
		t.Error("parseRoster(no models) = nil error, want a defect")
	}
}

func TestPackagingDefectSurfacesFromEveryAPI(t *testing.T) {
	origDoc, origErr := rosterDoc, rosterLoadErr
	defer func() { rosterDoc, rosterLoadErr = origDoc, origErr }()
	rosterDoc, rosterLoadErr = nil, errors.New("simulated load failure")

	checks := map[string]error{}
	_, checks["Lookup"] = Lookup("claude-opus-5")
	_, checks["Compare"] = Compare("claude-opus-5", "claude-sonnet-5")
	_, checks["EffortAvailable"] = EffortAvailable("claude-opus-5")
	_, checks["Selectable"] = Selectable("claude-opus-5")
	_, checks["Lifecycle"] = Lifecycle("claude-opus-5")
	_, checks["Price"] = Price("claude-opus-5")

	for name, err := range checks {
		var defect *PackagingDefectError
		if !errors.As(err, &defect) {
			t.Errorf("%s() with a failed load = %v, want *PackagingDefectError", name, err)
		}
	}
}

func TestSelectableAndLifecycleAndPriceHappyPath(t *testing.T) {
	sel, err := Selectable("claude-opus-5")
	if err != nil || sel != SelectableNewWork {
		t.Errorf("Selectable(opus-5) = %v, %v; want new-work, nil", sel, err)
	}
	life, err := Lifecycle("claude-opus-5")
	if err != nil || life != LifecycleActive {
		t.Errorf("Lifecycle(opus-5) = %v, %v; want active, nil", life, err)
	}
	price, err := Price("claude-opus-5")
	if err != nil || price.Basis != "list" || price.Input != 5.0 {
		t.Errorf("Price(opus-5) = %+v, %v; want basis list, input 5.0", price, err)
	}
}

func TestEffortAvailableHonoursExemptRow(t *testing.T) {
	// claude-fable-5 is effort_exempt: true, so every level resolves available regardless of
	// its literal effort_available list.
	efforts, err := EffortAvailable("claude-fable-5")
	if err != nil || len(efforts) != len(AllEfforts) {
		t.Errorf("EffortAvailable(fable-5) = %v, %v; want AllEfforts, nil", efforts, err)
	}
}

func TestForwardSchemaVersionIsPackagingDefect(t *testing.T) {
	future := `{"_schema_version":2,"effort_exempt_sentinels":["inherit"],"models":{"claude-opus-5":{}}}`
	if _, err := parseRoster([]byte(future)); err == nil {
		t.Error("parseRoster(future schema version) = nil error, want a defect")
	}
}
