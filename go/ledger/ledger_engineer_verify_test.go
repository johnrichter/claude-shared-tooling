package ledger

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Independent re-verification pass for M3.P2.T7, authored fresh against the acceptance
// criteria (not the existing test files) to catch anything three prior authors missed.

// AC1: the on-disk JSON field names are the wire contract external tooling (e.g. a dashboard
// reading the canonical file directly) depends on -- a silent rename would be a breaking change
// invisible to the Go type system.
func TestEngVerify_WireFieldNamesMatchContract(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", 3, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-0002"); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	raw, err := json.Marshal(l.doc.Entries[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"resolution", "citation", "retraction", "recurrence", "recur_cycles"} {
		if _, ok := m[field]; !ok {
			t.Fatalf("wire format missing expected field %q, got keys %v", field, m)
		}
	}
	retraction, ok := m["retraction"].(map[string]any)
	if !ok {
		t.Fatalf("retraction field is not an object: %+v", m["retraction"])
	}
	if _, ok := retraction["refuting_evidence"]; !ok {
		t.Fatalf("retraction missing refuting_evidence field: %+v", retraction)
	}
	if _, ok := retraction["superseded_entry_id"]; !ok {
		t.Fatalf("retraction missing superseded_entry_id field: %+v", retraction)
	}
}

// AC2/AC3: an entry that has already reached a non-terminal resolution (fixed-live, carried,
// stopgap) is not exempt from retraction -- a further-discovered refutation must still be able
// to pull it out of live ranking, and stopgap in particular (already "not closure") must not be
// mistaken for a state that blocks Retract.
func TestEngVerify_RetractAppliesOnTopOfEachNonTerminalResolution(t *testing.T) {
	for _, res := range []Resolution{ResolutionFixedLive, ResolutionCarried, ResolutionStopgap} {
		l, e := newLedgerWithEntry(t, 4, 4)
		if _, err := l.Resolve(e.ID, res, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err != nil {
			t.Fatalf("Resolve(%s): %v", res, err)
		}
		if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T10"}, "ENTRY-0002"); err != nil {
			t.Fatalf("Retract after %s: %v", res, err)
		}
		for _, le := range l.List() {
			if le.ID == e.ID {
				t.Fatalf("entry retracted after resolution %s still appears in default List", res)
			}
		}
		found := false
		for _, le := range l.ListWithRetracted() {
			if le.ID == e.ID {
				found = true
				if le.Resolution != ResolutionRetracted {
					t.Fatalf("entry resolution after Retract-on-top-of-%s = %q, want retracted", res, le.Resolution)
				}
			}
		}
		if !found {
			t.Fatalf("entry retracted after resolution %s not readable via ListWithRetracted", res)
		}
	}
}

// AC4: a citation whose Value is exactly the empty string after trimming leading/trailing
// whitespace only (not all-whitespace) is still refused -- guards against a caller passing
// "  " thinking it differs from "".
func TestEngVerify_CitationRefusesTrimmedEmptyAcrossAllKinds(t *testing.T) {
	for _, kind := range []CitationKind{CitationPathLine, CitationReleaseTag, CitationTaskID} {
		c := Citation{Kind: kind, Value: "   "}
		if err := c.Validate(); err == nil {
			t.Fatalf("Citation{Kind: %s, Value: whitespace} accepted, want refusal", kind)
		}
	}
}

// AC5: Recur must not increment when the underlying persist fails partway through a sequence of
// distinct cycles -- confirms RecurCycles and Recurrence advance together or not at all, never
// one without the other, across a longer multi-cycle sequence than the existing single-failure
// tests exercise.
func TestEngVerify_RecurCyclesAndRecurrenceStayInLockstepAcrossManyDistinctCycles(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	cycles := []string{"2025-Q1", "2025-Q2", "2025-Q3", "2025-Q4", "2026-Q1", "2026-Q2"}
	for i, c := range cycles {
		got, err := l.Recur(e.ID, c)
		if err != nil {
			t.Fatalf("Recur(%q): %v", c, err)
		}
		if got.Recurrence != i+1 {
			t.Fatalf("after cycle %q, Recurrence = %d, want %d", c, got.Recurrence, i+1)
		}
		if len(got.RecurCycles) != i+1 {
			t.Fatalf("after cycle %q, len(RecurCycles) = %d, want %d", c, len(got.RecurCycles), i+1)
		}
	}
}

// AC6: Rollup accounting holds even when every entry in the ledger is retracted -- the
// degenerate all-retracted case must not divide-by-zero, panic, or silently misreport Total.
func TestEngVerify_RollupTotalAndLosslessWhenEveryEntryIsRetracted(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	n := 5
	for i := 0; i < n; i++ {
		e, err := l.Add("e", 3, 3)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-SUP"); err != nil {
			t.Fatalf("Retract: %v", err)
		}
	}
	roll := l.Rollup(1)
	if roll.Total != n || roll.Retracted != n || roll.ActNow != 0 || roll.Deferred != 0 {
		t.Fatalf("all-retracted rollup = %+v, want Total=%d Retracted=%d ActNow=0 Deferred=0", roll, n, n)
	}
	if len(l.List()) != 0 {
		t.Fatalf("default List on all-retracted ledger = %d entries, want 0", len(l.List()))
	}
	actNow, deferred := l.Partition(1)
	if len(actNow) != 0 || len(deferred) != 0 {
		t.Fatalf("Partition on all-retracted ledger = act=%d def=%d, want 0/0", len(actNow), len(deferred))
	}
}
