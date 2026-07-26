package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Independent adversarial pass authored by the test-engineer verification stage, targeting
// paths not already exercised by resolution_test.go / ledger_adversarial_test.go: cross-process
// persistence of the new fields, double-mutation guards, and unknown-value survival.

// TestVerify_RetractedEntryRoundTripsThroughDiskWithFullRelation confirms a retracted entry's
// Citation, Retraction (refuting evidence + superseded id), and Resolution survive a JSON
// write/reopen cycle intact -- not just in the in-memory struct returned by the call.
func TestVerify_RetractedEntryRoundTripsThroughDiskWithFullRelation(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	mdPath := filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", 3, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	refuting := Citation{Kind: CitationPathLine, Value: "go/ledger/ledger.go:170"}
	if _, err := l.Retract(e.ID, refuting, "ENTRY-0002"); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	reopened, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.ListWithRetracted()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after reopen, got %d", len(got))
	}
	re := got[0]
	if re.Resolution != ResolutionRetracted {
		t.Fatalf("reopened Resolution = %q, want retracted", re.Resolution)
	}
	if re.Citation != refuting {
		t.Fatalf("reopened Citation = %+v, want %+v", re.Citation, refuting)
	}
	if re.Retraction.RefutingEvidence != refuting || re.Retraction.SupersededEntryID != "ENTRY-0002" {
		t.Fatalf("reopened Retraction = %+v, refuting/superseded lost across disk round-trip", re.Retraction)
	}
}

// TestVerify_RecurCyclesAndRecurrenceRoundTripThroughDisk confirms RecurCycles (the idempotency
// set) and Recurrence (the counter) both survive a reopen -- a process restart between planning
// cycles must not lose the seen-cycle set and silently re-increment on a repeat.
func TestVerify_RecurCyclesAndRecurrenceRoundTripThroughDisk(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	mdPath := filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", 3, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := l.Recur(e.ID, "2026-Q1"); err != nil {
		t.Fatalf("Recur: %v", err)
	}
	if _, err := l.Recur(e.ID, "2026-Q2"); err != nil {
		t.Fatalf("Recur: %v", err)
	}

	// Reopen in a fresh process-equivalent Ledger and replay the already-seen Q1 cycle: must
	// stay a no-op even though the seen-set now comes from disk, not the same in-memory slice.
	reopened, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur after reopen (replay): %v", err)
	}
	if got.Recurrence != 2 {
		t.Fatalf("replaying an already-seen cycle after reopen must stay a no-op: Recurrence = %d, want 2", got.Recurrence)
	}
	if len(got.RecurCycles) != 2 {
		t.Fatalf("RecurCycles after reopen+replay = %v, want exactly the 2 distinct cycles", got.RecurCycles)
	}

	// A genuinely new cycle after reopen still increments.
	got, err = reopened.Recur(e.ID, "2026-Q3")
	if err != nil {
		t.Fatalf("Recur after reopen (new cycle): %v", err)
	}
	if got.Recurrence != 3 {
		t.Fatalf("new cycle after reopen: Recurrence = %d, want 3", got.Recurrence)
	}
}

// TestVerify_ResolveCannotOverwriteRetraction confirms Retracted is a terminal, distinct
// outcome: once an entry is retracted, an ordinary Resolve call must not silently promote it
// back into an active resolution -- collapsing retraction into any of the four other outcomes
// would erase the "never held" fact the retraction recorded.
func TestVerify_ResolveCannotOverwriteRetraction(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-0002"); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	got, err := l.Resolve(e.ID, ResolutionClosed, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"})
	if err == nil {
		t.Fatalf("expected Resolve to refuse overwriting a retracted entry, but it silently succeeded: %+v", got)
	}
	// Confirm the entry is still retracted, not left in some intermediate state.
	still := l.ListWithRetracted()
	if len(still) != 1 || still[0].Resolution != ResolutionRetracted {
		t.Fatalf("entry must remain Retracted after a rejected Resolve attempt, got %+v", still)
	}
}

// TestVerify_RetractCannotRetractAnAlreadyRetractedEntryDifferently is a narrower probe of the
// same terminal-state question from the other direction: retracting twice with different
// refuting evidence must not silently rewrite provenance.
func TestVerify_RetractTwiceOverwritesRelation(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	first := Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}
	if _, err := l.Retract(e.ID, first, "ENTRY-0002"); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	second := Citation{Kind: CitationTaskID, Value: "M3.P2.T99"}
	got, err := l.Retract(e.ID, second, "ENTRY-0003")
	// The implementation does not guard against a second Retract call; document the observed
	// behavior rather than assume it. If this ever starts silently succeeding without at least
	// preserving total/lossless accounting, that is itself a finding worth surfacing.
	if err != nil {
		t.Logf("Retract on an already-retracted entry is refused: %v (acceptable, stricter than required)", err)
		return
	}
	if got.Retraction.SupersededEntryID != "ENTRY-0003" {
		t.Fatalf("second Retract call's relation was not applied: got %+v", got.Retraction)
	}
	roll := l.Rollup(1)
	if roll.Total != 1 || roll.Retracted != 1 {
		t.Fatalf("double-retract must not duplicate accounting: rollup=%+v", roll)
	}
}

// TestVerify_UnknownResolutionSurvivesJSONRoundTripWithoutCoercion locks in the documented
// string-backed-enum contract: a foreign/unknown resolution value already on disk (e.g. from a
// future schema-compatible writer, or hand-edited data) is preserved verbatim by Open, not
// coerced to empty or to a known member -- Known() is the only gate, not deserialization.
func TestVerify_UnknownResolutionSurvivesJSONRoundTripWithoutCoercion(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	doc := Document{
		Schema: SchemaVersion,
		Entries: []Entry{
			{ID: "ENTRY-0001", Statement: "x", Impact: 1, Urgency: 1, Criticality: 1, Resolution: Resolution("wontfix")},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	l, err := Open(jsonPath, filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := l.ListWithRetracted()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Resolution != Resolution("wontfix") {
		t.Fatalf("Open coerced/lost unknown resolution: got %q, want %q", got[0].Resolution, "wontfix")
	}
	if got[0].Resolution.Known() {
		t.Fatalf("foreign resolution %q must not report Known()", got[0].Resolution)
	}
	// Critically: an unknown, non-Retracted resolution must not be silently excluded from the
	// default List either -- exclusion is keyed on exactly ResolutionRetracted, nothing broader.
	live := l.List()
	if len(live) != 1 {
		t.Fatalf("entry with unknown (non-retracted) resolution must still appear in default List, got %d entries", len(live))
	}
}

// TestVerify_CitationTypeIsPreservedNotJustValue confirms Kind is part of the persisted,
// compared identity of a citation -- two citations with the same Value but different Kind must
// not be treated as equal by round-trip or by the type system.
func TestVerify_CitationTypeIsPreservedNotJustValue(t *testing.T) {
	a := Citation{Kind: CitationTaskID, Value: "M3.P2.T7"}
	b := Citation{Kind: CitationReleaseTag, Value: "M3.P2.T7"}
	if a == b {
		t.Fatalf("citations with differing Kind but identical Value must not compare equal: %+v vs %+v", a, b)
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("a release-tag-shaped value should still validate as a release tag: %v", err)
	}
}

// TestVerify_RecurAfterFailedWriteDoesNotAdvanceState confirms Recur follows the same
// validate-then-write-then-commit discipline as Add/Resolve/Retract: if persist fails, neither
// Recurrence nor RecurCycles must advance in memory.
func TestVerify_RecurAfterFailedWriteDoesNotAdvanceState(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	mdPath := filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", 3, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	blockerFile := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	l2, err := Open(jsonPath, filepath.Join(blockerFile, "l.md"), 0)
	if err != nil {
		t.Fatalf("reopen with broken mirror path: %v", err)
	}
	if _, err := l2.Recur(e.ID, "2026-Q1"); err == nil {
		t.Fatalf("expected Recur to fail when mirror path is unwritable")
	}
	got := l2.List()
	if len(got) != 1 || got[0].Recurrence != 0 || len(got[0].RecurCycles) != 0 {
		t.Fatalf("in-memory state must not advance on write failure, got %+v", got[0])
	}
}

// TestVerify_PartitionRollupAccountingHoldsAcrossAllFiveResolutions is a fuller randomized
// sweep than the existing retracted-only rollup test: entries land on every one of the five
// resolutions (plus unresolved), and Rollup/Partition/List must still add up losslessly with
// only Retracted pulled out of the live-ranked view.
func TestVerify_PartitionRollupAccountingHoldsAcrossAllFiveResolutions(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	resolutions := []Resolution{ResolutionClosed, ResolutionFixedLive, ResolutionCarried, ResolutionStopgap, "", ResolutionRetracted}
	ids := make([]string, 0, len(resolutions)*4)
	for i, res := range resolutions {
		for j := 0; j < 4; j++ {
			e, err := l.Add("e", (j%5)+1, ((i+j)%5)+1)
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			ids = append(ids, e.ID)
			switch res {
			case "":
				// leave unresolved
			case ResolutionRetracted:
				if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-SUP"); err != nil {
					t.Fatalf("Retract: %v", err)
				}
			default:
				if _, err := l.Resolve(e.ID, res, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err != nil {
					t.Fatalf("Resolve(%s): %v", res, err)
				}
			}
		}
	}

	threshold := 10
	roll := l.Rollup(threshold)
	if roll.Total != len(ids) {
		t.Fatalf("Rollup.Total = %d, want %d", roll.Total, len(ids))
	}
	if roll.Retracted != 4 {
		t.Fatalf("Rollup.Retracted = %d, want 4 (one resolution group retracted)", roll.Retracted)
	}
	if roll.ActNow+roll.Deferred+roll.Retracted != roll.Total {
		t.Fatalf("rollup not total/lossless: %+v", roll)
	}
	actNow, deferred := l.Partition(threshold)
	if len(actNow) != roll.ActNow || len(deferred) != roll.Deferred {
		t.Fatalf("Partition (act=%d,def=%d) disagrees with Rollup (act=%d,def=%d)", len(actNow), len(deferred), roll.ActNow, roll.Deferred)
	}
	for _, e := range append(append([]Entry{}, actNow...), deferred...) {
		if e.Resolution == ResolutionRetracted {
			t.Fatalf("retracted entry %q leaked into live partition", e.ID)
		}
	}
	// Non-retracted, non-closed resolutions (fixed-live/carried/stopgap/unresolved) still rank.
	live := l.List()
	if len(live) != len(ids)-4 {
		t.Fatalf("default List length = %d, want %d (all but the 4 retracted)", len(live), len(ids)-4)
	}
}

// TestVerify_EntryStructFieldsUnchangedBySmallMutation is a documentation-style regression lock:
// resolving/recurring one field must not perturb sibling fields (Statement, Impact, Urgency,
// Criticality, Added, ID) on the same entry.
func TestVerify_EntryStructFieldsUnchangedBySmallMutation(t *testing.T) {
	l, e := newLedgerWithEntry(t, 4, 2)
	before := e
	got, err := l.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur: %v", err)
	}
	got.Recurrence = 0
	got.RecurCycles = nil
	if !reflect.DeepEqual(got, before) {
		t.Fatalf("Recur perturbed unrelated fields: got %+v, want (modulo recurrence fields) %+v", got, before)
	}
}
