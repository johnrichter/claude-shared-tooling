package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: opens a fresh ledger and adds one entry, returning both.
func newLedgerWithEntry(t *testing.T, impact, urgency int) (*Ledger, Entry) {
	t.Helper()
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e, err := l.Add("case", impact, urgency)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return l, e
}

// --- Resolution.Known() ---

func TestResolution_KnownAcceptsOnlyTheFiveCanonicalValues(t *testing.T) {
	known := []Resolution{ResolutionClosed, ResolutionFixedLive, ResolutionCarried, ResolutionRetracted, ResolutionStopgap}
	for _, r := range known {
		if !r.Known() {
			t.Fatalf("%q should be Known()", r)
		}
	}
}

func TestResolution_KnownRejectsUnknownStringAndZeroValue(t *testing.T) {
	for _, r := range []Resolution{"", "wontfix", "closed ", "Closed", "done", "invalid"} {
		if Resolution(r).Known() {
			t.Fatalf("%q must not be Known()", r)
		}
	}
}

// --- Citation.Validate() ---

func TestCitation_ValidatesEachKnownKind(t *testing.T) {
	cases := []Citation{
		{Kind: CitationPathLine, Value: "go/ledger/ledger.go:42"},
		{Kind: CitationReleaseTag, Value: "v1.2.3"},
		{Kind: CitationTaskID, Value: "M3.P2.T7"},
	}
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate(%+v): unexpected error %v", c, err)
		}
	}
}

func TestCitation_RejectsUnknownKind(t *testing.T) {
	c := Citation{Kind: "prose-note", Value: "we fixed it in the meeting"}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected unknown citation kind to be refused")
	}
}

func TestCitation_RejectsEmptyValue(t *testing.T) {
	c := Citation{Kind: CitationTaskID, Value: ""}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected empty citation value to be refused")
	}
	c2 := Citation{Kind: CitationTaskID, Value: "   "}
	if err := c2.Validate(); err == nil {
		t.Fatalf("expected whitespace-only citation value to be refused")
	}
}

func TestCitation_RejectsZeroValueCitation(t *testing.T) {
	var c Citation
	if err := c.Validate(); err == nil {
		t.Fatalf("expected zero-value citation (no kind, no value) to be refused")
	}
}

func TestCitation_RejectsProseValueRegardlessOfDeclaredKind(t *testing.T) {
	// A caller cannot dodge the prose ban by mislabeling a sentence as a task-id or tag.
	prose := []Citation{
		{Kind: CitationTaskID, Value: "fixed during the retro on this one"},
		{Kind: CitationReleaseTag, Value: "released in v1 after the meeting"},
		{Kind: CitationPathLine, Value: "somewhere in the file we discussed"},
	}
	for _, c := range prose {
		if err := c.Validate(); err == nil {
			t.Fatalf("expected prose citation %+v to be refused", c)
		}
	}
}

func TestCitation_PathLineRequiresColonAndNumericLine(t *testing.T) {
	bad := []Citation{
		{Kind: CitationPathLine, Value: "go/ledger/ledger.go"},     // no line
		{Kind: CitationPathLine, Value: "go/ledger/ledger.go:"},    // empty line
		{Kind: CitationPathLine, Value: ":42"},                     // empty path
		{Kind: CitationPathLine, Value: "go/ledger/ledger.go:foo"}, // non-numeric line
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Fatalf("expected malformed path:line citation %+v to be refused", c)
		}
	}
}

// --- Retraction.Validate() ---

func TestRetraction_RequiresBothRefutingEvidenceAndSupersededID(t *testing.T) {
	validCitation := Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}

	// missing superseded id
	r1 := Retraction{RefutingEvidence: validCitation, SupersededEntryID: ""}
	if err := r1.Validate(); err == nil {
		t.Fatalf("expected retraction without superseded id to be refused")
	}

	// missing/invalid refuting evidence
	r2 := Retraction{RefutingEvidence: Citation{}, SupersededEntryID: "ENTRY-0002"}
	if err := r2.Validate(); err == nil {
		t.Fatalf("expected retraction without valid refuting evidence to be refused")
	}

	// both present and valid
	r3 := Retraction{RefutingEvidence: validCitation, SupersededEntryID: "ENTRY-0002"}
	if err := r3.Validate(); err != nil {
		t.Fatalf("expected complete retraction to validate, got %v", err)
	}
}

// --- Resolve: every non-retract resolution requires a valid citation ---

func TestResolve_RequiresValidCitationForEveryNonRetractResolution(t *testing.T) {
	for _, res := range []Resolution{ResolutionClosed, ResolutionFixedLive, ResolutionCarried, ResolutionStopgap} {
		l, e := newLedgerWithEntry(t, 3, 3)

		// prose-only citation is refused
		if _, err := l.Resolve(e.ID, res, Citation{Kind: CitationTaskID, Value: "we fixed it, trust us"}); err == nil {
			t.Fatalf("resolution %q: expected prose-only citation to be refused", res)
		}
		// no citation (zero value) is refused
		if _, err := l.Resolve(e.ID, res, Citation{}); err == nil {
			t.Fatalf("resolution %q: expected empty citation to be refused", res)
		}
		// valid citation succeeds
		got, err := l.Resolve(e.ID, res, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"})
		if err != nil {
			t.Fatalf("resolution %q: expected valid citation to succeed, got %v", res, err)
		}
		if got.Resolution != res {
			t.Fatalf("resolution %q: entry.Resolution = %q, want %q", res, got.Resolution, res)
		}
	}
}

func TestResolve_RejectsUnknownResolutionString(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Resolve(e.ID, Resolution("wontfix"), Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err == nil {
		t.Fatalf("expected unknown resolution string to be refused")
	}
}

func TestResolve_RefusesRetractedAsABareResolution(t *testing.T) {
	// Retracted must go through Retract, which carries the extra relation -- Resolve must not
	// silently accept it as a fifth ordinary outcome.
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Resolve(e.ID, ResolutionRetracted, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err == nil {
		t.Fatalf("expected Resolve to refuse ResolutionRetracted")
	}
}

func TestResolve_UnknownIDIsRefused(t *testing.T) {
	l, _ := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Resolve("ENTRY-9999", ResolutionClosed, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err == nil {
		t.Fatalf("expected unknown id to be refused")
	}
}

func TestResolve_FailedWriteDoesNotMutateState(t *testing.T) {
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

	// Break the mirror path after the entry exists, then attempt Resolve.
	blockerFile := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	l2, err := Open(jsonPath, filepath.Join(blockerFile, "l.md"), 0)
	if err != nil {
		t.Fatalf("reopen with broken mirror path: %v", err)
	}
	if _, err := l2.Resolve(e.ID, ResolutionClosed, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err == nil {
		t.Fatalf("expected Resolve to fail when mirror path is unwritable")
	}
	got := l2.List()
	if len(got) != 1 || got[0].Resolution != "" {
		t.Fatalf("in-memory state must not advance on write failure, got %+v", got)
	}
}

// --- stopgap is not closure ---

func TestResolve_StopgapIsRepresentableAndDistinctFromClosed(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	got, err := l.Resolve(e.ID, ResolutionStopgap, Citation{Kind: CitationTaskID, Value: "M3.P3.T1"})
	if err != nil {
		t.Fatalf("Resolve(stopgap): %v", err)
	}
	if got.Resolution != ResolutionStopgap {
		t.Fatalf("got.Resolution = %q, want stopgap", got.Resolution)
	}
	if got.Resolution == ResolutionClosed {
		t.Fatalf("stopgap must never equal or collapse into closed")
	}
	// A stopgap-resolved entry still ranks among live work (it is not retracted); it must NOT
	// be excluded from the default List the way a retracted entry is.
	found := false
	for _, le := range l.List() {
		if le.ID == e.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("stopgap-resolved entry must still appear in default List (stopgap is not closure and not retraction)")
	}
}

// --- Retract: exclusion from List and Partition, readability under explicit flag ---

func TestRetract_ExcludedFromDefaultListAndActNowPartitionButReadableExplicitly(t *testing.T) {
	l, e := newLedgerWithEntry(t, 5, 5) // criticality 25, well above any reasonable threshold
	refuting := Citation{Kind: CitationPathLine, Value: "go/ledger/ledger.go:100"}

	if _, err := l.Retract(e.ID, refuting, "ENTRY-9999-superseder"); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	// absent from default List
	for _, le := range l.List() {
		if le.ID == e.ID {
			t.Fatalf("retracted entry %q must be absent from default List", e.ID)
		}
	}

	// absent from ledger-backed Partition's act-now bucket (and deferred, since it's excluded
	// entirely from the live-ranked source)
	actNow, deferred := l.Partition(1) // threshold 1: everything live would be act-now
	for _, e2 := range append(append([]Entry{}, actNow...), deferred...) {
		if e2.ID == e.ID {
			t.Fatalf("retracted entry %q must be absent from ledger-backed Partition entirely", e.ID)
		}
	}

	// readable under explicit include-retracted read
	found := false
	for _, le := range l.ListWithRetracted() {
		if le.ID == e.ID {
			found = true
			if le.Resolution != ResolutionRetracted {
				t.Fatalf("expected retracted entry's Resolution to be %q, got %q", ResolutionRetracted, le.Resolution)
			}
		}
	}
	if !found {
		t.Fatalf("retracted entry %q must remain readable via ListWithRetracted", e.ID)
	}
}

func TestRetract_RequiresRefutingEvidencePlusSupersededID(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	validCitation := Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}

	if _, err := l.Retract(e.ID, validCitation, ""); err == nil {
		t.Fatalf("expected Retract to refuse missing superseded entry id")
	}
	if _, err := l.Retract(e.ID, Citation{}, "ENTRY-0002"); err == nil {
		t.Fatalf("expected Retract to refuse missing/invalid refuting evidence")
	}
	if _, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "prose not a citation"}, "ENTRY-0002"); err == nil {
		t.Fatalf("expected Retract to refuse prose-only refuting evidence")
	}
}

func TestRetract_UnknownIDIsRefused(t *testing.T) {
	l, _ := newLedgerWithEntry(t, 3, 3)
	validCitation := Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}
	if _, err := l.Retract("ENTRY-9999", validCitation, "ENTRY-0002"); err == nil {
		t.Fatalf("expected unknown id to be refused")
	}
}

func TestRetract_IsDistinctFromClosed(t *testing.T) {
	// Retracting must not be representable as, or confusable with, a Closed resolution -- the
	// two carry different information and must never collapse.
	l, e := newLedgerWithEntry(t, 3, 3)
	got, err := l.Retract(e.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-0002")
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if got.Resolution == ResolutionClosed {
		t.Fatalf("retracted entry must never carry ResolutionClosed")
	}
	if got.Resolution != ResolutionRetracted {
		t.Fatalf("got.Resolution = %q, want retracted", got.Resolution)
	}
}

// --- Recur: idempotent per cycle, never resets, refuses resolved entries ---

func TestRecur_IncrementsOncePerDistinctCycleAndIsIdempotentWithinACycle(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)

	got, err := l.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur: %v", err)
	}
	if got.Recurrence != 1 {
		t.Fatalf("after first Recur, Recurrence = %d, want 1", got.Recurrence)
	}

	// replaying the same cycle must not inflate the count
	got, err = l.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur (replay): %v", err)
	}
	if got.Recurrence != 1 {
		t.Fatalf("replaying same cycle must be idempotent: Recurrence = %d, want 1", got.Recurrence)
	}
	got, err = l.Recur(e.ID, "2026-Q1")
	if err != nil {
		t.Fatalf("Recur (replay again): %v", err)
	}
	if got.Recurrence != 1 {
		t.Fatalf("replaying same cycle a third time must still be idempotent: Recurrence = %d, want 1", got.Recurrence)
	}

	// a distinct cycle always increments
	got, err = l.Recur(e.ID, "2026-Q2")
	if err != nil {
		t.Fatalf("Recur (new cycle): %v", err)
	}
	if got.Recurrence != 2 {
		t.Fatalf("after second distinct cycle, Recurrence = %d, want 2", got.Recurrence)
	}
}

func TestRecur_NeverSilentlyResets(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	cycles := []string{"2026-Q1", "2026-Q2", "2026-Q3", "2026-Q1", "2026-Q4"}
	prev := 0
	for _, c := range cycles {
		got, err := l.Recur(e.ID, c)
		if err != nil {
			t.Fatalf("Recur(%q): %v", c, err)
		}
		if got.Recurrence < prev {
			t.Fatalf("Recurrence decreased: cycle %q gave %d, previous was %d", c, got.Recurrence, prev)
		}
		prev = got.Recurrence
	}
	// 2026-Q1 repeats (no-op), so distinct cycles = Q1, Q2, Q3, Q4 = 4 increments.
	if prev != 4 {
		t.Fatalf("final Recurrence = %d, want 4 (one per distinct cycle)", prev)
	}
}

func TestRecur_RefusesAlreadyResolvedEntry(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Resolve(e.ID, ResolutionClosed, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := l.Recur(e.ID, "2026-Q1"); err == nil {
		t.Fatalf("expected Recur to refuse an already-resolved entry")
	}
}

func TestRecur_RefusesEmptyCycle(t *testing.T) {
	l, e := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Recur(e.ID, "   "); err == nil {
		t.Fatalf("expected Recur to refuse a whitespace-only cycle")
	}
}

func TestRecur_UnknownIDIsRefused(t *testing.T) {
	l, _ := newLedgerWithEntry(t, 3, 3)
	if _, err := l.Recur("ENTRY-9999", "2026-Q1"); err == nil {
		t.Fatalf("expected unknown id to be refused")
	}
}

// --- Partition/Rollup: total, lossless, retracted accounted for ---

func TestRollup_TotalAndLosslessAcrossActNowDeferredRetracted(t *testing.T) {
	l, _ := newLedgerWithEntry(t, 5, 5) // criticality 25 -> act-now
	low, err := l.Add("low", 1, 1)      // criticality 1 -> deferred
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	toRetract, err := l.Add("to retract", 4, 4) // criticality 16
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := l.Retract(toRetract.ID, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-0099"); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	roll := l.Rollup(10)
	if roll.Total != 3 {
		t.Fatalf("Rollup.Total = %d, want 3", roll.Total)
	}
	if roll.Retracted != 1 {
		t.Fatalf("Rollup.Retracted = %d, want 1", roll.Retracted)
	}
	if roll.ActNow+roll.Deferred+roll.Retracted != roll.Total {
		t.Fatalf("rollup not total/lossless: actNow=%d deferred=%d retracted=%d total=%d", roll.ActNow, roll.Deferred, roll.Retracted, roll.Total)
	}
	// low (criticality 1) below threshold 10 -> deferred; the un-retracted high one -> act-now.
	if roll.ActNow != 1 || roll.Deferred != 1 {
		t.Fatalf("expected actNow=1 deferred=1, got actNow=%d deferred=%d", roll.ActNow, roll.Deferred)
	}
	_ = low
}

func TestPartitionRollup_RandomizedTotalAndLosslessWithRetractions(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	n := 40
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		impact := (i % 5) + 1
		urgency := ((i * 3) % 5) + 1
		e, err := l.Add("e", impact, urgency)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		ids = append(ids, e.ID)
	}
	// retract every third entry
	retractedCount := 0
	for i, id := range ids {
		if i%3 == 0 {
			if _, err := l.Retract(id, Citation{Kind: CitationTaskID, Value: "M3.P2.T9"}, "ENTRY-SUP"); err != nil {
				t.Fatalf("Retract(%q): %v", id, err)
			}
			retractedCount++
		}
	}

	threshold := 10
	roll := l.Rollup(threshold)
	if roll.Total != n {
		t.Fatalf("Rollup.Total = %d, want %d", roll.Total, n)
	}
	if roll.Retracted != retractedCount {
		t.Fatalf("Rollup.Retracted = %d, want %d", roll.Retracted, retractedCount)
	}
	if roll.ActNow+roll.Deferred+roll.Retracted != roll.Total {
		t.Fatalf("rollup not total/lossless: actNow=%d deferred=%d retracted=%d total=%d", roll.ActNow, roll.Deferred, roll.Retracted, roll.Total)
	}

	actNow, deferred := l.Partition(threshold)
	if len(actNow) != roll.ActNow || len(deferred) != roll.Deferred {
		t.Fatalf("Partition output (act=%d,def=%d) does not match Rollup (act=%d,def=%d)", len(actNow), len(deferred), roll.ActNow, roll.Deferred)
	}
	for _, e := range append(append([]Entry{}, actNow...), deferred...) {
		if e.Resolution == ResolutionRetracted {
			t.Fatalf("retracted entry %q leaked into live Partition output", e.ID)
		}
	}
}
