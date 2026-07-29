package compliance

import (
	"os"
	"testing"
	"time"
)

// trials builds n TrialResult values, the first h of them honored -- a compact way to author a
// declared-vs-actual trial set for a test.
func trials(h, n int) []TrialResult {
	out := make([]TrialResult, n)
	for i := 0; i < n; i++ {
		out[i] = TrialResult{Honored: i < h}
	}
	return out
}

func floorPtr(v float64) *float64 { return &v }

func testDoc() *Document {
	return &Document{
		Schema: "invariant-registry@1.0.0",
		Discovery: Discovery{
			Roots: []DiscoveryRoot{
				{Repo: "marketplace", Strategy: "claude-plugin-hooks", InScopeOwners: []string{"example-plugin"}},
			},
		},
		Invariants: []Entry{
			{
				ID:                "example-cli.dispatch.commit-per-task",
				Statement:         "a completed task is committed before the next dispatch, so no finished work sits only in a working tree",
				Rung:              4,
				FailDirection:     "open",
				BlastRadius:       "an ignored advisory leaves work uncommitted but recoverable",
				Owner:             "example-cli",
				Status:            "shipped",
				ComplianceFloors:  map[string]*float64{"claude-opus-5": nil, "claude-sonnet-5": nil},
				MeasurementStatus: StatusDeclaredUnmeasured,
				RegisterEntryID:   "rp-example-commit-per-task",
			},
		},
	}
}

// TestFirstCalibrationSetsFloor confirms the interim state (a declared, null floor) resolves into
// a real one from the first measurement, never from a value the test pre-declared.
func TestFirstCalibrationSetsFloor(t *testing.T) {
	doc := testDoc()
	results := &ResultsDocument{Schema: ResultsSchema, Results: []InvariantResult{
		{InvariantID: "example-cli.dispatch.commit-per-task", Model: "claude-opus-5", Mechanism: "dispatch-log-classifier", Trials: trials(7, 8)},
	}}

	outcome := MeasureRegistry(doc, results, time.Unix(0, 0))

	if len(outcome.Calibrated) != 1 || outcome.Calibrated[0].Rate != 0.875 {
		t.Fatalf("Calibrated = %+v, want one 0.875 finding", outcome.Calibrated)
	}
	entry := doc.EntryByID("example-cli.dispatch.commit-per-task")
	if got := entry.ComplianceFloors["claude-opus-5"]; got == nil || *got != 0.875 {
		t.Fatalf("floor = %v, want 0.875 set by this run", got)
	}
	if entry.MeasuredRates["claude-opus-5"] != 0.875 {
		t.Fatalf("measured rate = %v, want 0.875", entry.MeasuredRates["claude-opus-5"])
	}
	// The second model is still unmeasured, so the entry as a whole is not yet StatusMeasured.
	if entry.MeasurementStatus != StatusDeclaredUnmeasured {
		t.Fatalf("measurement_status = %q, want %q (one of two models still unmeasured)", entry.MeasurementStatus, StatusDeclaredUnmeasured)
	}
}

// TestBelowFloorOpensPauseAndDefectWithoutTouchingTheEntry confirms a below-floor rate opens both
// hand-offs, and that doing so never mutates the invariant's own status field -- the registry
// entry the shipping milestone recorded stays "shipped", never retroactively failed.
func TestBelowFloorOpensPauseAndDefectWithoutTouchingTheEntry(t *testing.T) {
	doc := testDoc()
	doc.Invariants[0].ComplianceFloors["claude-sonnet-5"] = floorPtr(0.9)
	doc.Invariants[0].MeasuredRates = map[string]float64{"claude-opus-5": 0.95}

	results := &ResultsDocument{Schema: ResultsSchema, Results: []InvariantResult{
		{InvariantID: "example-cli.dispatch.commit-per-task", Model: "claude-sonnet-5", Mechanism: "dispatch-log-classifier", Trials: trials(2, 8)},
	}}
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	outcome := MeasureRegistry(doc, results, now)

	if len(outcome.BelowFloor) != 1 {
		t.Fatalf("BelowFloor = %+v, want one finding", outcome.BelowFloor)
	}
	f := outcome.BelowFloor[0]
	if f.Owner != "example-cli" || f.MeasuredRate != 0.25 || f.DeclaredFloor != 0.9 {
		t.Fatalf("finding = %+v, want owner example-cli, rate 0.25, floor 0.9", f)
	}

	entry := doc.EntryByID(f.InvariantID)
	if entry.Status != "shipped" {
		t.Fatalf("status = %q, want unchanged %q -- a below-floor rate must never retroactively fail the shipping entry", entry.Status, "shipped")
	}

	pause := PauseRegister{Schema: PauseSchema}
	fb := FeedbackRegister{Schema: FeedbackSchema}
	pause, fb = ApplyBelowFloor(pause, fb, outcome.BelowFloor, doc.OwnerKind, now)

	if len(pause.Entries) != 1 || pause.Entries[0].Status != PauseStatusOpen || pause.Entries[0].Owner != "example-cli" {
		t.Fatalf("pause register = %+v, want one open entry against example-cli", pause.Entries)
	}
	if pause.Entries[0].OwnerKind != "cli" {
		t.Fatalf("owner_kind = %q, want %q (example-cli is not in any claude-plugin-hooks scope)", pause.Entries[0].OwnerKind, "cli")
	}
	if len(fb.Entries) != 1 || fb.Entries[0].Impact != belowFloorImpact {
		t.Fatalf("feedback register = %+v, want one below-floor defect", fb.Entries)
	}

	// A second measurement of the identical miss refreshes both rows rather than duplicating them.
	pause2, fb2 := ApplyBelowFloor(pause, fb, outcome.BelowFloor, doc.OwnerKind, now.Add(time.Hour))
	if len(pause2.Entries) != 1 || len(fb2.Entries) != 1 {
		t.Fatalf("repeat apply duplicated rows: pause=%d feedback=%d, want 1 each", len(pause2.Entries), len(fb2.Entries))
	}
	if pause2.Entries[0].DefectID != fb.Entries[0].ID {
		t.Fatalf("defect_id = %q, want it to keep naming the original feedback entry %q", pause2.Entries[0].DefectID, fb.Entries[0].ID)
	}
}

// TestUnmeasuredAtReleaseOpensItsOwnDefectAndNeverPauses confirms an entry still declared-
// unmeasured at its owner's release is treated as its own finding, not as a passing enforcement,
// and that the pause-register row it produces never carries the "open" status the release gate
// pauses on.
func TestUnmeasuredAtReleaseOpensItsOwnDefectAndNeverPauses(t *testing.T) {
	doc := testDoc() // measurement_status is StatusDeclaredUnmeasured, both floors still null

	pending := UnmeasuredAtRelease(doc, "example-cli")
	if len(pending) != 1 || pending[0].ID != doc.Invariants[0].ID {
		t.Fatalf("UnmeasuredAtRelease = %+v, want the one still-unmeasured entry", pending)
	}

	pause := PauseRegister{Schema: PauseSchema}
	fb := FeedbackRegister{Schema: FeedbackSchema}
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	pause, fb = ApplyUnmeasured(pause, fb, pending, doc.OwnerKind, now)

	if len(fb.Entries) != 1 {
		t.Fatalf("feedback register = %+v, want one unmeasured-at-release defect", fb.Entries)
	}
	if len(pause.Entries) != 1 || pause.Entries[0].Status != PauseStatusUnmeasured {
		t.Fatalf("pause register = %+v, want one declared-unmeasured entry", pause.Entries)
	}
	// Unmeasured is not enforcement: nothing here is the "open" status release-transaction's gate
	// reads as a reason to refuse a release.
	for _, e := range pause.Entries {
		if e.Status == PauseStatusOpen {
			t.Fatalf("an unmeasured entry must never be recorded as open: %+v", e)
		}
	}

	// A repeat check opens no second defect.
	pause2, fb2 := ApplyUnmeasured(pause, fb, pending, doc.OwnerKind, now.Add(time.Hour))
	if len(fb2.Entries) != 1 || len(pause2.Entries) != 1 {
		t.Fatalf("repeat check duplicated rows: pause=%d feedback=%d, want 1 each", len(pause2.Entries), len(fb2.Entries))
	}
}

// TestLoadResultsRefusesASingleShotProbe confirms no path through this loader accepts a
// measurement resting on fewer than MinTrials -- there is no single-shot probe path.
func TestLoadResultsRefusesASingleShotProbe(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/results.json"
	writeFile(t, path, `{
		"schema": "compliance-measurement-input/v1",
		"results": [{"invariant_id": "x.y", "model": "claude-sonnet-5", "mechanism": "probe", "trials": [{"honored": true}]}]
	}`)
	if _, err := LoadResults(path); err == nil {
		t.Fatal("LoadResults accepted a single-trial result, want a refusal")
	}
}

// TestLoadResultsRefusesAnUnnamedMechanism confirms a result naming no mechanism -- an
// unexplained number rather than a declared-vs-actual comparison -- is rejected.
func TestLoadResultsRefusesAnUnnamedMechanism(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/results.json"
	writeFile(t, path, `{
		"schema": "compliance-measurement-input/v1",
		"results": [{"invariant_id": "x.y", "model": "claude-sonnet-5", "trials": [{"honored": true}, {"honored": false}]}]
	}`)
	if _, err := LoadResults(path); err == nil {
		t.Fatal("LoadResults accepted a result naming no mechanism, want a refusal")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
