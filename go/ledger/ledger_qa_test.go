package ledger

import (
	"path/filepath"
	"testing"
)

// Independent adversarial verification for M3.P2.T7 (SC-FEEDBACK resolution vocabulary),
// authored separately from the implementer's own test files to avoid mirroring the
// implementation rather than the acceptance criteria.

// AC1: closed enum, Known(), typed citation kinds, Retraction relation shape, Recurrence is int.
func TestQA_UnknownResolutionStringNeverSlipsInAsFreeString(t *testing.T) {
	for _, bad := range []Resolution{"", "Closed", "CLOSED", "closed ", "wontfix", "duplicate", "obsolete"} {
		if bad.Known() {
			t.Fatalf("Resolution(%q).Known() = true, want false", bad)
		}
	}
}

func TestQA_RecurrenceFieldIsAPlainInt(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	got, err := l.Recur(e.ID, "cycle-1")
	if err != nil {
		t.Fatalf("Recur: %v", err)
	}
	var _ int = got.Recurrence // compile-time assertion the field is int
	if got.Recurrence != 1 {
		t.Fatalf("Recurrence = %d, want 1", got.Recurrence)
	}
}

// AC4: prose-only or missing citation refused for every resolution path, adversarially.
func TestQA_ResolveRefusesEveryProseShapedCitation(t *testing.T) {
	prose := []Citation{
		{Kind: CitationTaskID, Value: "fixed this properly, see PR"},
		{Kind: CitationPathLine, Value: "we changed the file"},
		{Kind: CitationReleaseTag, Value: "release from last sprint"},
		{Kind: CitationTaskID, Value: ""},
		{Kind: "", Value: "go/ledger/ledger.go:42"},
		{Kind: CitationTaskID, Value: "M3.P2.T7\twith a tab"},
		{Kind: CitationTaskID, Value: "M3.P2.T7\nwith a newline"},
	}
	for _, res := range []Resolution{ResolutionClosed, ResolutionFixedLive, ResolutionCarried, ResolutionStopgap} {
		for _, c := range prose {
			l, e := newLedgerWithEntry(t, 3, 3)
			if _, err := l.Resolve(e.ID, res, c); err == nil {
				t.Fatalf("Resolve(%s, %+v) accepted a prose/empty citation, want refusal", res, c)
			}
			// Confirm nothing was mutated: entry still unresolved.
			live := l.List()
			if len(live) != 1 || live[0].Resolution.Known() {
				t.Fatalf("entry mutated despite refused citation: %+v", live)
			}
		}
	}
}

func TestQA_PathLineRejectsNonNumericAndNegativeLine(t *testing.T) {
	for _, val := range []string{"go/ledger/ledger.go:", ":42", "go/ledger/ledger.go:abc", "go/ledger/ledger.go:-1", "go/ledger/ledger.go:4.2"} {
		c := Citation{Kind: CitationPathLine, Value: val}
		if err := c.Validate(); err == nil {
			t.Fatalf("Citation{PathLine, %q}.Validate() = nil, want error", val)
		}
	}
}

// AC2 + AC6: retracted entries excluded from default List/act-now partition, readable on
// explicit opt-in, and still counted in Rollup so the tri-partition is total and lossless —
// this is the FB13/FB15 worked case: a refuted entry must never rank among live work again.
func TestQA_RetractedNeverReappearsInLiveRankingEvenWhenHighestCriticality(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	hot, err := l.Add("highest criticality entry, later refuted", 5, 5)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := l.Add("lower criticality survivor", 1, 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := l.Retract(hot.ID, Citation{Kind: CitationTaskID, Value: "FB15"}, "ENTRY-0002"); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	live := l.List()
	for _, e := range live {
		if e.ID == hot.ID {
			t.Fatalf("retracted entry %q present in default List despite being the highest-criticality entry: %+v", hot.ID, live)
		}
	}

	actNow, _ := l.Partition(1)
	for _, e := range actNow {
		if e.ID == hot.ID {
			t.Fatalf("retracted entry %q present in act-now partition: %+v", hot.ID, actNow)
		}
	}

	withRetracted := l.ListWithRetracted()
	found := false
	for _, e := range withRetracted {
		if e.ID == hot.ID {
			found = true
			if e.Resolution != ResolutionRetracted {
				t.Fatalf("entry %q resolution = %s, want retracted", hot.ID, e.Resolution)
			}
		}
	}
	if !found {
		t.Fatalf("retracted entry %q not readable via ListWithRetracted", hot.ID)
	}

	roll := l.Rollup(1)
	if roll.Total != 2 || roll.Retracted != 1 || roll.ActNow+roll.Deferred+roll.Retracted != roll.Total {
		t.Fatalf("Rollup not total/lossless: %+v", roll)
	}
}

// AC3: stopgap is representable and distinct from closure -- it must carry a successor task id
// citation and must never be conflated with Closed at the type/value level.
func TestQA_StopgapCarriesSuccessorAndIsNeverEqualToClosed(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	got, err := l.Resolve(e.ID, ResolutionStopgap, Citation{Kind: CitationTaskID, Value: "M3.P2.T99"})
	if err != nil {
		t.Fatalf("Resolve stopgap: %v", err)
	}
	if got.Resolution == ResolutionClosed {
		t.Fatalf("stopgap resolved as Closed")
	}
	if got.Resolution != ResolutionStopgap {
		t.Fatalf("Resolution = %s, want stopgap", got.Resolution)
	}
	if got.Citation.Kind != CitationTaskID || got.Citation.Value != "M3.P2.T99" {
		t.Fatalf("stopgap citation not preserved: %+v", got.Citation)
	}
	// A stopgap entry still ranks among live work -- it is not closure and not retraction.
	live := l.List()
	if len(live) != 1 || live[0].ID != e.ID {
		t.Fatalf("stopgap entry excluded from live List, want present: %+v", live)
	}
}

// AC5: recurrence increments on a further, unconsumed planning cycle; is idempotent within a
// cycle; and never resets, including across ledger reopen (i.e. it survives persistence, not
// just in-memory state).
func TestQA_RecurrenceSurvivesReopenAndNeverResets(t *testing.T) {
	dir := t.TempDir()
	jsonPath, mdPath := filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", 3, 3)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, cycle := range []string{"2026-Q1", "2026-Q1", "2026-Q2"} {
		if _, err := l.Recur(e.ID, cycle); err != nil {
			t.Fatalf("Recur(%s): %v", cycle, err)
		}
	}
	reopened, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.List()[0]
	if got.Recurrence != 2 {
		t.Fatalf("Recurrence after reopen = %d, want 2 (idempotent within 2026-Q1, incremented once more for 2026-Q2)", got.Recurrence)
	}
	// Revisiting an earlier cycle after reopen must still be a no-op, not a reset or re-increment.
	again, err := reopened.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur revisit: %v", err)
	}
	if again.Recurrence != 2 {
		t.Fatalf("Recurrence after revisiting a seen cycle post-reopen = %d, want unchanged 2", again.Recurrence)
	}
}

// AC1 (retract relation): retract relation must carry both refuting evidence and superseded id;
// a citation-shaped-but-empty superseded id is refused just like a missing citation.
func TestQA_RetractRefusesEmptySupersededIDEvenWithValidCitation(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "FB13"}, ""); err == nil {
		t.Fatalf("Retract with empty superseded id succeeded, want refusal")
	}
	if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "FB13"}, "   "); err == nil {
		t.Fatalf("Retract with whitespace-only superseded id succeeded, want refusal")
	}
	live := l.List()
	if len(live) != 1 || live[0].Resolution == ResolutionRetracted {
		t.Fatalf("entry retracted despite refused relation: %+v", live)
	}
}

func TestQA_RetractRefusesUnknownCitationKindEvenWithSupersededID(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Retract(e.ID, Citation{Kind: "hearsay", Value: "someone said so"}, "ENTRY-0002"); err == nil {
		t.Fatalf("Retract with unknown citation kind succeeded, want refusal")
	}
}

// Cross-cutting: retracting an entry does not corrupt sibling entries' ranking or accounting.
func TestQA_RetractingOneEntryLeavesSiblingsRankedCorrectly(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	low, _ := l.Add("low", 1, 1)
	mid, _ := l.Add("mid", 3, 3)
	high, _ := l.Add("high, will be retracted", 5, 5)
	if _, err := l.Retract(high.ID, Citation{Kind: CitationTaskID, Value: "FB13"}, low.ID); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	live := l.List()
	if len(live) != 2 || live[0].ID != mid.ID || live[1].ID != low.ID {
		t.Fatalf("ranking corrupted after retraction: %+v", live)
	}
}
