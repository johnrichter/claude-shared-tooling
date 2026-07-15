package bh

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAddFeedbackAppendsAndDerives is the acceptance-criteria core: a schema-valid entry is
// appended, id is generated FB<n>, criticality is derived (impact*urgency per the documented
// rule), and the caller cannot forge either field.
func TestAddFeedbackAppendsAndDerives(t *testing.T) {
	reg := FeedbackRegister{}
	in := FeedbackInput{
		Title:            "Slow retrieve",
		SourceTaskID:     "M1.P1.T1",
		Feedback:         "retrieve is slow on big plans",
		ProposedSolution: "index by id",
		WhyItMatters:     "blocks fast iteration",
		Impact:           4,
		Urgency:          3,
	}
	out, err := AddFeedback(reg, in, "2026-07-04T00:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(out.Entries))
	}
	e := out.Entries[0]
	if e.ID != "FB1" {
		t.Errorf("id = %q, want FB1", e.ID)
	}
	if e.Criticality != 12 {
		t.Errorf("criticality = %d, want 12 (impact 4 * urgency 3)", e.Criticality)
	}
	if out.Schema != FeedbackSchema {
		t.Errorf("schema = %q, want %q (must be stamped on first write)", out.Schema, FeedbackSchema)
	}
	// original register must not be mutated (AddFeedback returns a new value).
	if len(reg.Entries) != 0 {
		t.Fatalf("input register was mutated: len(reg.Entries) = %d, want 0", len(reg.Entries))
	}
}

// TestAddFeedbackIDsAreMonotonic guards the FB<n> generation rule across repeated adds within
// the same register — the id must track register length, not be content-derived or reused.
func TestAddFeedbackIDsAreMonotonic(t *testing.T) {
	reg := FeedbackRegister{}
	var err error
	for i := 1; i <= 3; i++ {
		reg, err = AddFeedback(reg, FeedbackInput{Title: "t", Feedback: "f", Impact: 1, Urgency: 1}, "")
		if err != nil {
			t.Fatalf("add %d: unexpected error: %v", i, err)
		}
	}
	wantIDs := []string{"FB1", "FB2", "FB3"}
	for i, e := range reg.Entries {
		if e.ID != wantIDs[i] {
			t.Errorf("entry %d id = %q, want %q", i, e.ID, wantIDs[i])
		}
	}
}

// TestAddFeedbackRejectsOutOfRangeScores enumerates the boundary and adjacent-to-boundary cases
// for impact/urgency: 0, 6, negative, and the valid boundary values 1 and 5 (which must NOT be
// rejected). A caller-supplied criticality-adjacent value must never slip through.
func TestAddFeedbackRejectsOutOfRangeScores(t *testing.T) {
	cases := []struct {
		name    string
		impact  int
		urgency int
		wantErr bool
	}{
		{"impact zero", 0, 3, true},
		{"impact six", 6, 3, true},
		{"impact negative", -1, 3, true},
		{"urgency zero", 3, 0, true},
		{"urgency six", 3, 6, true},
		{"urgency negative", 3, -5, true},
		{"both out of range", 0, 99, true},
		{"boundary low valid", 1, 1, false},
		{"boundary high valid", 5, 5, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := FeedbackRegister{}
			out, err := AddFeedback(reg, FeedbackInput{Title: "t", Feedback: "f", Impact: c.impact, Urgency: c.urgency}, "")
			if c.wantErr {
				if err == nil {
					t.Fatalf("impact=%d urgency=%d: want error, got none", c.impact, c.urgency)
				}
				if len(out.Entries) != 0 {
					t.Fatalf("rejected add must not mutate register: got %d entries", len(out.Entries))
				}
			} else if err != nil {
				t.Fatalf("impact=%d urgency=%d: unexpected error: %v", c.impact, c.urgency, err)
			}
		})
	}
}

// TestAddFeedbackRequiresTitleAndFeedback: empty/whitespace-only title or feedback body is
// rejected — these are the two fields with no downstream default.
func TestAddFeedbackRequiresTitleAndFeedback(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		feedback string
	}{
		{"empty title", "", "body"},
		{"whitespace title", "   ", "body"},
		{"empty feedback", "Title", ""},
		{"whitespace feedback", "Title", "\t\n "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := AddFeedback(FeedbackRegister{}, FeedbackInput{Title: c.title, Feedback: c.feedback, Impact: 3, Urgency: 3}, "")
			if err == nil {
				t.Fatalf("title=%q feedback=%q: want error, got none", c.title, c.feedback)
			}
		})
	}
}

// TestCriticalityRuleIsDocumented pins the exact formula (impact*urgency) against regression —
// the design doc and schema both commit to this rule; changing it silently would desync them.
func TestCriticalityRuleIsDocumented(t *testing.T) {
	cases := []struct{ impact, urgency, want int }{
		{1, 1, 1},
		{5, 5, 25},
		{1, 5, 5},
		{5, 1, 5},
		{3, 3, 9},
	}
	for _, c := range cases {
		if got := Criticality(c.impact, c.urgency); got != c.want {
			t.Errorf("Criticality(%d,%d) = %d, want %d", c.impact, c.urgency, got, c.want)
		}
	}
}

// TestRenderFeedbackMirrorsCanonical is the SC13 mirror-parity acceptance test: every field in
// every canonical entry (JSON round-trip through the same register RenderFeedback consumes) must
// surface in the rendered Markdown — id, title, source task, impact, urgency, criticality in the
// table, and feedback/proposed_solution/why_it_matters in the detail section. A field present in
// JSON but absent from the render is exactly the divergence SC13 forbids.
func TestRenderFeedbackMirrorsCanonical(t *testing.T) {
	reg := FeedbackRegister{}
	reg, err := AddFeedback(reg, FeedbackInput{
		Title:            "Unique Title Marker",
		SourceTaskID:     "M9.P2.T3",
		Feedback:         "Unique feedback body marker",
		ProposedSolution: "Unique proposed solution marker",
		WhyItMatters:     "Unique why-it-matters marker",
		Impact:           5,
		Urgency:          4,
	}, "2026-07-04T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	// Round-trip through JSON to prove the render works off the same canonical shape a real
	// `feedback add` writer would persist and reload, not an in-memory-only convenience.
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded FeedbackRegister
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}

	md := RenderFeedback(reloaded)
	e := reloaded.Entries[0]
	for _, want := range []string{
		e.ID,
		e.Title,
		e.SourceTaskID,
		e.Feedback,
		e.ProposedSolution,
		e.WhyItMatters,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("feedback.md missing %q from canonical entry:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "5") || !strings.Contains(md, "4") || !strings.Contains(md, "20") {
		t.Errorf("feedback.md missing impact(5)/urgency(4)/criticality(20) numerals:\n%s", md)
	}
}

// TestRenderFeedbackDeterministic: rendering the same register twice must byte-for-byte match —
// required for the "faithful mirror" claim and for the add-then-diff test strategy to be
// meaningful at all.
func TestRenderFeedbackDeterministic(t *testing.T) {
	reg := FeedbackRegister{}
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "A", Feedback: "a", Impact: 2, Urgency: 2}, "t1")
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "B", Feedback: "b", Impact: 3, Urgency: 3}, "t2")

	a := RenderFeedback(reg)
	b := RenderFeedback(reg)
	if a != b {
		t.Fatalf("RenderFeedback is not deterministic:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

// TestRenderFeedbackEmptyRegister: zero entries must not crash and must say so plainly rather
// than emit a headerless or malformed table.
func TestRenderFeedbackEmptyRegister(t *testing.T) {
	md := RenderFeedback(FeedbackRegister{})
	if !strings.Contains(md, "no feedback entries") {
		t.Errorf("empty register render should state no entries exist, got:\n%s", md)
	}
	if strings.Contains(md, "| ID |") {
		t.Errorf("empty register should not emit a table header:\n%s", md)
	}
}

// TestRenderFeedbackEscapesTablePipes: a title/feedback body containing a literal "|" would
// break the Markdown table if not escaped/handled, silently corrupting every column after it —
// an adversarial input a real "why it matters" free-text field will eventually contain.
func TestRenderFeedbackEscapesTablePipes(t *testing.T) {
	reg := FeedbackRegister{}
	reg, err := AddFeedback(reg, FeedbackInput{
		Title:    "Weird | Title",
		Feedback: "body",
		Impact:   1,
		Urgency:  1,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	md := RenderFeedback(reg)
	lines := strings.Split(md, "\n")
	var tableRow string
	for _, l := range lines {
		if strings.HasPrefix(l, "| FB1") {
			tableRow = l
			break
		}
	}
	if tableRow == "" {
		t.Fatalf("could not find FB1 table row in:\n%s", md)
	}
	// The literal "|" in Title must survive escaped (as "\|"), not as a bare unescaped "|" that
	// would split the Markdown table into an extra column. Strip every escaped pipe first, then
	// any "|" remaining outside the 7 structural delimiters is an escaping failure.
	unescaped := strings.ReplaceAll(tableRow, `\|`, "")
	cells := strings.Split(unescaped, "|")
	if len(cells) != 8 {
		t.Errorf("table row has %d structural pipe-delimited cells after stripping escaped pipes (want 8, i.e. 6 data columns) — a literal \"|\" in free text broke the table: %q", len(cells), tableRow)
	}
	if !strings.Contains(tableRow, `Weird \| Title`) {
		t.Errorf("table row does not contain the escaped title %q: %q", `Weird \| Title`, tableRow)
	}
}

// TestListFeedbackFiltersComposeAndRank is the M13.P1.T2 sanity check: --by-task, --min-impact,
// --min-urgency each narrow correctly, compose with AND, and the survivors rank by criticality
// descending with a deterministic id-ascending tiebreak. Full adversarial coverage is the
// test-engineer's stage; this pins the core contract.
func TestListFeedbackFiltersComposeAndRank(t *testing.T) {
	reg := FeedbackRegister{}
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "A", SourceTaskID: "M1.P1.T1", Feedback: "f", Impact: 2, Urgency: 2}, "") // FB1 crit 4
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "B", SourceTaskID: "M1.P1.T2", Feedback: "f", Impact: 5, Urgency: 5}, "") // FB2 crit 25
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "C", SourceTaskID: "M1.P1.T1", Feedback: "f", Impact: 4, Urgency: 1}, "") // FB3 crit 4
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "D", SourceTaskID: "M1.P1.T3", Feedback: "f", Impact: 1, Urgency: 1}, "") // FB4 crit 1

	// no filter: list-all, ranked by criticality desc, tie (FB1/FB3 both 4) breaks by id asc.
	all := ListFeedback(reg, FeedbackFilter{})
	wantAll := []string{"FB2", "FB1", "FB3", "FB4"}
	if got := idsOf(all); !equalStrings(got, wantAll) {
		t.Errorf("no-filter order = %v, want %v", got, wantAll)
	}

	// --by-task alone.
	byTask := ListFeedback(reg, FeedbackFilter{SourceTaskID: "M1.P1.T1"})
	if got := idsOf(byTask); !equalStrings(got, []string{"FB1", "FB3"}) {
		t.Errorf("by-task filter = %v, want [FB1 FB3]", got)
	}

	// --min-impact alone.
	minImpact := ListFeedback(reg, FeedbackFilter{MinImpact: 4})
	if got := idsOf(minImpact); !equalStrings(got, []string{"FB2", "FB3"}) {
		t.Errorf("min-impact filter = %v, want [FB2 FB3]", got)
	}

	// --min-urgency alone.
	minUrgency := ListFeedback(reg, FeedbackFilter{MinUrgency: 2})
	if got := idsOf(minUrgency); !equalStrings(got, []string{"FB2", "FB1"}) {
		t.Errorf("min-urgency filter = %v, want [FB2 FB1]", got)
	}

	// composed: by-task AND min-impact — only FB3 (FB1 fails min-impact).
	composed := ListFeedback(reg, FeedbackFilter{SourceTaskID: "M1.P1.T1", MinImpact: 3})
	if got := idsOf(composed); !equalStrings(got, []string{"FB3"}) {
		t.Errorf("composed filter = %v, want [FB3]", got)
	}

	// empty result: a filter no entry satisfies.
	if got := ListFeedback(reg, FeedbackFilter{SourceTaskID: "M9.P9.T9"}); len(got) != 0 {
		t.Errorf("nonexistent by-task = %v, want empty", idsOf(got))
	}
}

// TestListFeedbackEmptyRegister covers the zero-entries edge: list-all and any filter on an empty
// register must both return an empty result, never nil-panic or a spurious entry.
func TestListFeedbackEmptyRegister(t *testing.T) {
	reg := FeedbackRegister{Schema: FeedbackSchema}
	if got := ListFeedback(reg, FeedbackFilter{}); len(got) != 0 {
		t.Errorf("list-all on empty register = %v, want empty", idsOf(got))
	}
	if got := ListFeedback(reg, FeedbackFilter{SourceTaskID: "M1.P1.T1", MinImpact: 3, MinUrgency: 3}); len(got) != 0 {
		t.Errorf("filtered list on empty register = %v, want empty", idsOf(got))
	}
}

// TestListFeedbackThreeWayTieStableByID exercises the tiebreak with more than two equal-criticality
// entries added out of monotonic order in the slice (via SourceTaskID filter isolating a subset),
// asserting id-ascending recovers append order even when three entries tie, not just two.
func TestListFeedbackThreeWayTieStableByID(t *testing.T) {
	reg := FeedbackRegister{}
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "A", Feedback: "f", Impact: 1, Urgency: 4}, "") // FB1 crit 4
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "B", Feedback: "f", Impact: 4, Urgency: 1}, "") // FB2 crit 4
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "C", Feedback: "f", Impact: 2, Urgency: 2}, "") // FB3 crit 4
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "D", Feedback: "f", Impact: 2, Urgency: 2}, "") // FB4 crit 4

	got := idsOf(ListFeedback(reg, FeedbackFilter{}))
	want := []string{"FB1", "FB2", "FB3", "FB4"}
	if !equalStrings(got, want) {
		t.Errorf("four-way tie order = %v, want %v (id-ascending stable tiebreak)", got, want)
	}
}

// TestListFeedbackByTaskExactMatchNotSubstring guards against a by-task filter that accidentally
// does substring/prefix matching instead of exact equality — a real risk with hierarchical task
// ids like M1.P1.T1 vs M1.P1.T1X or M1.P1.T10, where naive prefix logic would over-match.
func TestListFeedbackByTaskExactMatchNotSubstring(t *testing.T) {
	reg := FeedbackRegister{}
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "A", SourceTaskID: "M1.P1.T1", Feedback: "f", Impact: 3, Urgency: 3}, "")
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "B", SourceTaskID: "M1.P1.T10", Feedback: "f", Impact: 3, Urgency: 3}, "")
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "C", SourceTaskID: "M1.P1.T1X", Feedback: "f", Impact: 3, Urgency: 3}, "")

	got := idsOf(ListFeedback(reg, FeedbackFilter{SourceTaskID: "M1.P1.T1"}))
	if !equalStrings(got, []string{"FB1"}) {
		t.Errorf("by-task exact match = %v, want [FB1] (must not substring-match T10 or T1X)", got)
	}
}

// TestListFeedbackDoesNotMutateRegister asserts ListFeedback is read-only: the input register's
// Entries slice (order, contents) must be identical before and after a call, including when a
// filter narrows the result — the CLI relies on this to guarantee `feedback list` never writes.
func TestListFeedbackDoesNotMutateRegister(t *testing.T) {
	reg := FeedbackRegister{}
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "A", SourceTaskID: "M1.P1.T1", Feedback: "f", Impact: 2, Urgency: 2}, "")
	reg, _ = AddFeedback(reg, FeedbackInput{Title: "B", SourceTaskID: "M1.P1.T2", Feedback: "f", Impact: 5, Urgency: 5}, "")
	before, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}

	_ = ListFeedback(reg, FeedbackFilter{MinImpact: 4})

	after, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("register mutated by ListFeedback:\nbefore=%s\nafter=%s", before, after)
	}
}

func idsOf(es []FeedbackEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFeedbackInputCannotForgeIDOrCriticality is a compile-time-adjacent structural guard:
// FeedbackInput has no ID or Criticality field, so a caller (main.go's flag parsing) has no way
// to set either directly — AddFeedback is the sole producer. This test documents/enforces the
// invariant via reflection-free means: attempting to unmarshal a caller-supplied id/criticality
// into FeedbackInput must not silently populate a same-named field, because none exists.
func TestFeedbackInputCannotForgeIDOrCriticality(t *testing.T) {
	raw := []byte(`{"id":"FB999","criticality":25,"title":"t","feedback":"f","impact":1,"urgency":1}`)
	var in FeedbackInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	out, err := AddFeedback(FeedbackRegister{}, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Entries[0].ID == "FB999" {
		t.Fatal("caller-supplied id leaked through into the generated entry")
	}
	if out.Entries[0].Criticality == 25 && (in.Impact*in.Urgency) != 25 {
		t.Fatal("caller-supplied criticality leaked through — should be derived from impact*urgency only")
	}
}
