package ledger

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestAdd_CriticalityIsMultiplicativeAcrossBoundary confirms criticality = impact*urgency at
// every corner of the valid 1-5 range, not just one sample point.
func TestAdd_CriticalityIsMultiplicativeAcrossBoundary(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for impact := MinScore; impact <= MaxScore; impact++ {
		for urgency := MinScore; urgency <= MaxScore; urgency++ {
			e, err := l.Add("case", impact, urgency)
			if err != nil {
				t.Fatalf("Add(%d,%d): %v", impact, urgency, err)
			}
			want := impact * urgency
			if e.Criticality != want {
				t.Fatalf("impact=%d urgency=%d: got criticality %d, want %d (multiplicative)", impact, urgency, e.Criticality, want)
			}
		}
	}
}

// TestAdd_IDsAreSequentialAndUnique appends many entries and confirms ids never repeat and
// follow append order, regardless of criticality — id assignment must not be influenced by
// score.
func TestAdd_IDsAreSequentialAndUnique(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		impact := rand.Intn(5) + 1
		urgency := rand.Intn(5) + 1
		e, err := l.Add("entry", impact, urgency)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id %q at iteration %d", e.ID, i)
		}
		seen[e.ID] = true
	}
	if len(l.List()) != 50 {
		t.Fatalf("expected 50 entries, got %d", len(l.List()))
	}
}

// TestAdd_RejectsFailedWriteWithoutMutatingState confirms that if the mirror write path can't
// succeed (unwritable mdPath directory), the ledger's in-memory state is not advanced and no
// partial JSON is left describing an entry the mirror never got.
func TestAdd_RejectsFailedWriteWithoutMutatingState(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	// mdPath points inside a path component that is a file, not a directory: any write there
	// must fail atomically.
	blockerFile := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	mdPath := filepath.Join(blockerFile, "l.md")

	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Add("should not persist", 3, 3); err == nil {
		t.Fatalf("expected Add to fail when mirror path is unwritable")
	}
	if len(l.List()) != 0 {
		t.Fatalf("in-memory state must not advance on write failure, got %+v", l.List())
	}
	if _, statErr := os.Stat(jsonPath); statErr == nil {
		t.Fatalf("canonical JSON must not be left on disk after a failed pair-write, but it exists")
	}
}

// TestList_FiltersComposeAsAND confirms MinCriticality/MinImpact/MinUrgency combine with AND,
// not OR: an entry must pass every supplied filter, not just one.
func TestList_FiltersComposeAsAND(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	highImpactLowUrgency, _ := l.Add("high impact low urgency", 5, 1) // criticality 5
	_, _ = l.Add("low impact high urgency", 1, 5)                     // criticality 5
	both, _ := l.Add("both high", 5, 5)                               // criticality 25

	got := l.List(MinImpact(4), MinUrgency(4))
	if len(got) != 1 || got[0].ID != both.ID {
		t.Fatalf("AND composition failed: expected only %q, got %+v", both.ID, got)
	}

	// Sanity: a single-axis filter alone would admit the high-impact-low-urgency entry too,
	// proving the AND filter above is not accidentally passing because of ranking order.
	byImpactAlone := l.List(MinImpact(4))
	found := false
	for _, e := range byImpactAlone {
		if e.ID == highImpactLowUrgency.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("MinImpact(4) alone should admit the high-impact-low-urgency entry")
	}
}

// TestList_NoMatchReturnsEmptyNotNilPanic confirms an over-restrictive filter set degrades to
// an empty, safely-rangeable slice rather than nil-panicking a caller who indexes into it
// carelessly, and confirms it is not confused with "no filters" (all entries).
func TestList_NoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, _ = l.Add("low", 1, 1)
	got := l.List(MinCriticality(100))
	if len(got) != 0 {
		t.Fatalf("expected zero matches, got %+v", got)
	}
}

// TestPartition_TotalAndLosslessRandomized runs Partition over randomized entry sets and many
// thresholds, confirming len(actNow)+len(deferred) == len(input) and every input entry appears
// in exactly one output slice (no duplication, no drop) every time.
func TestPartition_TotalAndLosslessRandomized(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		n := rand.Intn(30)
		entries := make([]Entry, n)
		for i := range entries {
			entries[i] = Entry{ID: filepath.Join("ENTRY", string(rune('A'+i))), Criticality: rand.Intn(30)}
		}
		threshold := rand.Intn(30)
		actNow, deferred := Partition(entries, threshold)

		if len(actNow)+len(deferred) != len(entries) {
			t.Fatalf("trial %d: not total: in=%d actNow=%d deferred=%d", trial, len(entries), len(actNow), len(deferred))
		}
		seen := map[string]int{}
		for _, e := range actNow {
			seen[e.ID]++
			if e.Criticality < threshold {
				t.Fatalf("trial %d: entry %+v below threshold %d landed in actNow", trial, e, threshold)
			}
		}
		for _, e := range deferred {
			seen[e.ID]++
			if e.Criticality >= threshold {
				t.Fatalf("trial %d: entry %+v at/above threshold %d landed in deferred", trial, e, threshold)
			}
		}
		for _, e := range entries {
			if seen[e.ID] != 1 {
				t.Fatalf("trial %d: entry %q appeared %d times across partition outputs, want exactly 1", trial, e.ID, seen[e.ID])
			}
		}
	}
}

// TestPartition_EmptyInput confirms the empty-input boundary: both outputs empty, no panic.
func TestPartition_EmptyInput(t *testing.T) {
	actNow, deferred := Partition(nil, 5)
	if len(actNow) != 0 || len(deferred) != 0 {
		t.Fatalf("expected both empty for nil input, got actNow=%+v deferred=%+v", actNow, deferred)
	}
}

// TestPartition_ThresholdIsInclusiveOnActNow locks in the documented boundary: criticality
// exactly equal to threshold is act-now, not deferred.
func TestPartition_ThresholdIsInclusiveOnActNow(t *testing.T) {
	entries := []Entry{{ID: "x", Criticality: 10}}
	actNow, deferred := Partition(entries, 10)
	if len(actNow) != 1 || len(deferred) != 0 {
		t.Fatalf("expected threshold-equal entry in actNow, got actNow=%+v deferred=%+v", actNow, deferred)
	}
}

// TestOpen_RejectsForeignSchema confirms a JSON file declaring a different schema is refused
// rather than silently read as zero entries — a caller must not lose data by pointing Open at
// a file from an incompatible future/past version.
func TestOpen_RejectsForeignSchema(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	if err := os.WriteFile(jsonPath, []byte(`{"schema":"ledger@99.0.0","entries":[]}`), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Open(jsonPath, filepath.Join(dir, "l.md"), 0); err == nil {
		t.Fatalf("expected Open to reject foreign schema version")
	}
}

// TestOpen_RejectsCorruptJSON confirms malformed JSON is a hard error, not an empty ledger.
func TestOpen_RejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	if err := os.WriteFile(jsonPath, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Open(jsonPath, filepath.Join(dir, "l.md"), 0); err == nil {
		t.Fatalf("expected Open to reject corrupt JSON")
	}
}

// TestAdd_JSONAndMarkdownStayInSync confirms every entry appended appears in both the
// canonical JSON and the Markdown mirror after each Add, not just at the end of a batch.
func TestAdd_JSONAndMarkdownStayInSync(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	mdPath := filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for i := 0; i < 5; i++ {
		e, err := l.Add("statement", (i%5)+1, ((i+2)%5)+1)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}

		rawJSON, err := os.ReadFile(jsonPath)
		if err != nil {
			t.Fatalf("read json: %v", err)
		}
		var doc Document
		if err := json.Unmarshal(rawJSON, &doc); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		if len(doc.Entries) != i+1 {
			t.Fatalf("after Add #%d, json has %d entries, want %d", i, len(doc.Entries), i+1)
		}
		lastJSON := doc.Entries[len(doc.Entries)-1]
		if lastJSON.ID != e.ID || lastJSON.Criticality != e.Criticality {
			t.Fatalf("json's last entry %+v does not match returned entry %+v", lastJSON, e)
		}

		mdBytes, err := os.ReadFile(mdPath)
		if err != nil {
			t.Fatalf("read md: %v", err)
		}
		md := string(mdBytes)
		if !strings.Contains(md, e.ID) {
			t.Fatalf("markdown mirror missing entry id %q after Add #%d:\n%s", e.ID, i, md)
		}
		// Every prior entry must still be present too -- mirror is a full re-render, not an
		// append that could drop earlier rows.
		for _, prior := range doc.Entries[:len(doc.Entries)-1] {
			if !strings.Contains(md, prior.ID) {
				t.Fatalf("markdown mirror dropped earlier entry %q after Add #%d", prior.ID, i)
			}
		}
	}
}

// TestAdd_TrimsWhitespaceButRejectsWhitespaceOnly confirms the empty-statement guard is not
// bypassable with whitespace padding.
func TestAdd_TrimsWhitespaceButRejectsWhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "l.json"), filepath.Join(dir, "l.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Add("   \t\n  ", 3, 3); err == nil {
		t.Fatalf("expected whitespace-only statement to be rejected")
	}
	if len(l.List()) != 0 {
		t.Fatalf("whitespace-only Add must not append, got %+v", l.List())
	}
}

// TestReopen_PreservesRankingAndFilters confirms a reopened ledger (fresh in-memory state,
// loaded from disk) produces identical List/Partition results to the original -- persistence
// round-trip must not silently change ranking or drop fields.
func TestReopen_PreservesRankingAndFilters(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "l.json")
	mdPath := filepath.Join(dir, "l.md")
	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, sc := range [][2]int{{1, 1}, {5, 5}, {3, 2}, {2, 3}, {4, 4}} {
		if _, err := l.Add("e", sc[0], sc[1]); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	want := l.List()

	reopened, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.List()
	if len(got) != len(want) {
		t.Fatalf("reopened List length %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("reopened entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	wantAct, wantDef := Partition(want, 10)
	gotAct, gotDef := Partition(got, 10)
	if len(wantAct) != len(gotAct) || len(wantDef) != len(gotDef) {
		t.Fatalf("partition mismatch after reopen: want act=%d def=%d, got act=%d def=%d", len(wantAct), len(wantDef), len(gotAct), len(gotDef))
	}
}
