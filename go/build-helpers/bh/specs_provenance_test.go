package bh

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// specs_as_of + build_helpers_sha are carried on Accounting, populated by
// SetAccounting, and round-trip through execution.json's plain encoding/json marshal/unmarshal.

func TestSpecsAsOf_ExtractsAsOfKey(t *testing.T) {
	got := SpecsAsOf([]byte(`{"_as_of":"2026-07-03","pricing":{"list":{}}}`))
	if got != "2026-07-03" {
		t.Fatalf("SpecsAsOf = %q, want %q", got, "2026-07-03")
	}
}

func TestSpecsAsOf_MissingKeyYieldsEmpty(t *testing.T) {
	got := SpecsAsOf([]byte(`{"pricing":{"list":{}}}`))
	if got != "" {
		t.Fatalf("SpecsAsOf with no _as_of = %q, want \"\"", got)
	}
}

func TestSpecsAsOf_MalformedJSONYieldsEmptyNotError(t *testing.T) {
	// Best-effort per accounting.go doc comment: accounting must never block a run over a
	// provenance field, so invalid/truncated specs bytes must degrade to "" rather than panic.
	cases := [][]byte{
		[]byte(`not json at all`),
		[]byte(``),
		[]byte(`{"_as_of": 12345}`), // wrong type for the field
		nil,
	}
	for i, c := range cases {
		got := SpecsAsOf(c)
		if got != "" {
			t.Errorf("case %d: SpecsAsOf(%q) = %q, want \"\"", i, c, got)
		}
	}
}

// TestSetAccounting_PopulatesSpecsAsOfAndBuildHelpersSHA pins acceptance criteria 1+2: both fields
// land on RunConfig.Accounting exactly as passed, independent of the rest of the pricing math.
func TestSetAccounting_PopulatesSpecsAsOfAndBuildHelpersSHA(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t0")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "abc123deadbeef")

	got := ex.RunConfig.Accounting
	if got == nil {
		t.Fatal("SetAccounting did not persist RunConfig.Accounting")
	}
	if got.SpecsAsOf != "2026-07-03" {
		t.Errorf("SpecsAsOf = %q, want %q", got.SpecsAsOf, "2026-07-03")
	}
	if got.BuildHelpersSHA != "abc123deadbeef" {
		t.Errorf("BuildHelpersSHA = %q, want %q", got.BuildHelpersSHA, "abc123deadbeef")
	}
}

// TestSetAccounting_EmptyProvenanceIsPreservedNotDropped guards against a future edit that
// mistakes "" (best-effort unavailable, per doc comments on SpecsAsOf/buildHelpersSHA) for "leave
// the prior value untouched" — SetAccounting must overwrite unconditionally, matching acct.At's
// unconditional-overwrite behavior on the same line.
func TestSetAccounting_EmptyProvenanceIsPreservedNotDropped(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t0")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "", "")
	if ex.RunConfig.Accounting.SpecsAsOf != "" || ex.RunConfig.Accounting.BuildHelpersSHA != "" {
		t.Fatalf("expected empty provenance to be set as empty, got specs_as_of=%q build_helpers_sha=%q",
			ex.RunConfig.Accounting.SpecsAsOf, ex.RunConfig.Accounting.BuildHelpersSHA)
	}
}

// TestAccounting_RoundTripsThroughExecutionJSON pins acceptance criterion 3: SpecsAsOf and
// BuildHelpersSHA survive a plain json.Marshal/json.Unmarshal cycle of ExecState — the same
// mechanism main.go's printJSON/readExec use for execution.json — with no custom
// (Un)MarshalJSON on Accounting to bypass.
func TestAccounting_RoundTripsThroughExecutionJSON(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t0")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "abc123deadbeef")

	raw, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ExecState
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RunConfig.Accounting == nil {
		t.Fatal("round-trip dropped RunConfig.Accounting entirely")
	}
	if got.RunConfig.Accounting.SpecsAsOf != "2026-07-03" {
		t.Errorf("round-tripped SpecsAsOf = %q, want %q", got.RunConfig.Accounting.SpecsAsOf, "2026-07-03")
	}
	if got.RunConfig.Accounting.BuildHelpersSHA != "abc123deadbeef" {
		t.Errorf("round-tripped BuildHelpersSHA = %q, want %q", got.RunConfig.Accounting.BuildHelpersSHA, "abc123deadbeef")
	}

	// The wire format itself must use the snake_case keys the acceptance criteria name.
	if !containsAll(raw, []string{`"specs_as_of"`, `"build_helpers_sha"`}) {
		t.Errorf("marshaled execution.json missing expected keys, got: %s", raw)
	}
}

func containsAll(haystack []byte, needles []string) bool {
	s := string(haystack)
	for _, n := range needles {
		if !jsonContains(s, n) {
			return false
		}
	}
	return true
}

func jsonContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestReadExec_ToleratesPreUpgradeExecutionJSON pins that an execution.json written before this
// change (no specs_as_of/build_helpers_sha keys at all) still unmarshals cleanly — omitempty means
// they're absent on old files, and a plain struct field addition must not break old readers. This
// is the "every execution.json reader tolerates the new fields" seam the task flags.
func TestReadExec_ToleratesPreUpgradeExecutionJSON(t *testing.T) {
	preUpgrade := `{
		"run_config": {
			"accounting": {
				"cost_usd": 1.23,
				"at": "2026-06-01T00:00:00Z"
			}
		}
	}`
	var ex ExecState
	if err := json.Unmarshal([]byte(preUpgrade), &ex); err != nil {
		t.Fatalf("Unmarshal pre-upgrade execution.json: %v", err)
	}
	if ex.RunConfig.Accounting == nil {
		t.Fatal("pre-upgrade accounting object was dropped")
	}
	if ex.RunConfig.Accounting.SpecsAsOf != "" || ex.RunConfig.Accounting.BuildHelpersSHA != "" {
		t.Fatalf("pre-upgrade file should decode new fields as zero value, got specs_as_of=%q build_helpers_sha=%q",
			ex.RunConfig.Accounting.SpecsAsOf, ex.RunConfig.Accounting.BuildHelpersSHA)
	}
	if ex.RunConfig.Accounting.CostUSD != 1.23 {
		t.Fatalf("pre-upgrade cost_usd = %v, want 1.23", ex.RunConfig.Accounting.CostUSD)
	}
}
