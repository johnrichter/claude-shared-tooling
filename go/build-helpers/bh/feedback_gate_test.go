package bh

import (
	"encoding/json"
	"sort"
	"testing"
)

// gateFixture builds a register whose criticalities straddle any reasonable threshold, added out
// of rank order so the ranked-emit and stable-tiebreak assertions are meaningful. Criticalities:
// FB1=4 (2×2), FB2=25 (5×5), FB3=12 (4×3), FB4=1 (1×1), FB5=12 (3×4 — ties FB3), FB6=6 (2×3).
func gateFixture(t *testing.T) FeedbackRegister {
	t.Helper()
	reg := FeedbackRegister{}
	adds := []FeedbackInput{
		{Title: "A", Feedback: "fa", Impact: 2, Urgency: 2},
		{Title: "B", Feedback: "fb", Impact: 5, Urgency: 5},
		{Title: "C", Feedback: "fc", ProposedSolution: "fix c", WhyItMatters: "matters c", Impact: 4, Urgency: 3},
		{Title: "D", Feedback: "fd", Impact: 1, Urgency: 1},
		{Title: "E", Feedback: "fe", Impact: 3, Urgency: 4},
		{Title: "F", Feedback: "ff", Impact: 2, Urgency: 3},
	}
	var err error
	for i, in := range adds {
		if reg, err = AddFeedback(reg, in, ""); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	return reg
}

// basePlan is a minimal schema-valid plan (one real milestone) that generated feedback-review
// tasks are appended to and validated against.
func basePlan() Plan {
	return Plan{
		Goal:            "base",
		SuccessCriteria: []string{"sc1"},
		Milestones: []Milestone{{
			ID:   "M1",
			Name: "Base milestone",
			Phases: []Phase{{
				ID:   "M1.P1",
				Name: "Base phase",
				Tasks: []Task{{
					ID: "M1.P1.T1", Name: "Base task", Summary: "s", Deliverable: "d",
					Model: ModelOpus48, Effort: EffortHigh, TestStrategy: "ts", Acceptance: []string{"a"},
				}},
			}},
		}},
	}
}

// TestGatePartitionTotalLosslessExactlyOnce is the core acceptance invariant: at any threshold,
// every register entry appears in exactly one bucket — none dropped, none double-routed.
func TestGatePartitionTotalLosslessExactlyOnce(t *testing.T) {
	reg := gateFixture(t)
	all := idsOf(ListFeedback(reg, FeedbackFilter{}))
	sort.Strings(all)
	for _, threshold := range []int{-5, 0, 1, 2, 4, 6, 12, 13, 25, 26, 1000} {
		g := GateFeedback(reg, threshold)
		seen := map[string]int{}
		for _, e := range g.AmendNow {
			seen[e.ID]++
		}
		for _, e := range g.Deferred {
			seen[e.ID]++
		}
		got := make([]string, 0, len(seen))
		for id, n := range seen {
			if n != 1 {
				t.Errorf("threshold %d: entry %s routed %d times (want exactly 1)", threshold, id, n)
			}
			got = append(got, id)
		}
		sort.Strings(got)
		if !equalStrings(got, all) {
			t.Errorf("threshold %d: routed set %v != register %v (entry dropped or invented)", threshold, got, all)
		}
	}
}

// TestGateBoundaryRoutesToAmend pins the documented boundary rule: criticality == threshold is an
// inclusive floor -> amend-now, deterministically.
func TestGateBoundaryRoutesToAmend(t *testing.T) {
	reg := gateFixture(t)
	g := GateFeedback(reg, 12) // FB3 and FB5 have criticality exactly 12.
	amend := idsOf(g.AmendNow)
	for _, id := range []string{"FB2", "FB3", "FB5"} {
		if !contains(amend, id) {
			t.Errorf("threshold 12: %s (criticality >= 12) must be in amend_now, got %v", id, amend)
		}
	}
	for _, id := range []string{"FB1", "FB4", "FB6"} {
		if contains(amend, id) {
			t.Errorf("threshold 12: %s (criticality < 12) must NOT be in amend_now, got %v", id, amend)
		}
	}
}

// TestGateEmitsRankedOrder: both buckets are in criticality-desc, id-asc order (ListFeedback's
// order), so amend_now is ranked reconcile-exec amendment input and deferred is ranked too.
func TestGateEmitsRankedOrder(t *testing.T) {
	reg := gateFixture(t)
	g := GateFeedback(reg, 6)
	// crit>=6: FB2(25),FB3(12),FB5(12),FB6(6) -> ranked desc, FB3<FB5 id tiebreak.
	if got := idsOf(g.AmendNow); !equalStrings(got, []string{"FB2", "FB3", "FB5", "FB6"}) {
		t.Errorf("amend_now order = %v, want [FB2 FB3 FB5 FB6]", got)
	}
	// crit<6: FB1(4),FB4(1) -> ranked desc.
	if got := idsOf(g.Deferred); !equalStrings(got, []string{"FB1", "FB4"}) {
		t.Errorf("deferred order = %v, want [FB1 FB4]", got)
	}
}

// TestClassifyFeedbackCriticalityDeterministic pins the parameterized floor: below defers, equal
// and above amend.
func TestClassifyFeedbackCriticalityDeterministic(t *testing.T) {
	cases := []struct {
		crit, threshold int
		want            string
	}{
		{11, 12, RouteFeedbackReview},
		{12, 12, RouteFeedbackAmend},
		{13, 12, RouteFeedbackAmend},
		{1, 1, RouteFeedbackAmend},
		{0, 1, RouteFeedbackReview},
	}
	for _, c := range cases {
		if got := ClassifyFeedbackCriticality(c.crit, c.threshold); got != c.want {
			t.Errorf("ClassifyFeedbackCriticality(%d,%d) = %q, want %q", c.crit, c.threshold, got, c.want)
		}
	}
}

// TestFeedbackReviewTasksAreSchemaValid: sub-threshold entries become tasks that ValidatePlanBytes
// accepts when embedded in a plan, and each carries id + name + acceptance (the SC15/reconcile-exec
// requirement).
func TestFeedbackReviewTasksAreSchemaValid(t *testing.T) {
	reg := gateFixture(t)
	res := GatePlanFeedback(basePlan(), reg, 13) // only FB2(25) amends; FB1,FB3,FB4,FB5,FB6 defer.
	raw, err := json.Marshal(res.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if v := ValidatePlanBytes(raw); !v.OK {
		t.Fatalf("gated plan failed validation: %v", v.Errors)
	}
	// locate the feedback-review milestone and check each task's required fields.
	var frm *Milestone
	for i := range res.Plan.Milestones {
		if res.Plan.Milestones[i].ID == FeedbackReviewMilestoneID {
			frm = &res.Plan.Milestones[i]
		}
	}
	if frm == nil {
		t.Fatal("feedback-review milestone absent from gated plan")
	}
	tasks := frm.Phases[0].Tasks
	if len(tasks) != len(res.Deferred) {
		t.Fatalf("feedback-review has %d tasks, want %d (one per deferred entry)", len(tasks), len(res.Deferred))
	}
	for _, tk := range tasks {
		if tk.ID == "" || tk.Name == "" || len(tk.Acceptance) == 0 {
			t.Errorf("feedback-review task missing id/name/acceptance: %+v", tk)
		}
	}
}

// TestFeedbackReviewTaskIDMapsToEntry: FB<n> -> M999.P1.T<n>, a stable 1:1 map.
func TestFeedbackReviewTaskIDMapsToEntry(t *testing.T) {
	e := FeedbackEntry{ID: "FB7", Title: "t", Feedback: "f"}
	if got := FeedbackReviewTask(e).ID; got != "M999.P1.T7" {
		t.Errorf("task id = %q, want M999.P1.T7", got)
	}
}

// TestGateIdempotentRerun: re-running the gate on unchanged inputs is byte-identical, AND feeding
// the gate's own output plan back in (which already contains the feedback-review milestone) does
// not duplicate it — the milestone is regenerated wholesale each run.
func TestGateIdempotentRerun(t *testing.T) {
	reg := gateFixture(t)
	first := GatePlanFeedback(basePlan(), reg, 13)
	second := GatePlanFeedback(basePlan(), reg, 13)
	if a, b := mustJSON(t, first), mustJSON(t, second); a != b {
		t.Fatalf("gate not deterministic across identical runs:\n%s\n!=\n%s", a, b)
	}
	// feed the already-gated plan back in: must not append a second feedback-review milestone.
	rerun := GatePlanFeedback(first.Plan, reg, 13)
	count := 0
	for _, m := range rerun.Plan.Milestones {
		if m.ID == FeedbackReviewMilestoneID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("re-gating an already-gated plan yielded %d feedback-review milestones, want 1", count)
	}
	if a, b := mustJSON(t, first.Plan), mustJSON(t, rerun.Plan); a != b {
		t.Fatalf("re-gating an already-gated plan is not idempotent:\n%s\n!=\n%s", a, b)
	}
}

// TestApplyFeedbackReviewEmptyDeferredRemovesMilestone: with no sub-threshold entries the milestone
// is omitted entirely (an empty milestone/phase is schema-invalid), and any pre-existing one is
// stripped — never left hollow.
func TestApplyFeedbackReviewEmptyDeferredRemovesMilestone(t *testing.T) {
	reg := gateFixture(t)
	// threshold 0: inclusive floor -> everything amends, nothing defers.
	res := GatePlanFeedback(basePlan(), reg, 0)
	if len(res.Deferred) != 0 {
		t.Fatalf("threshold 0: want empty deferred, got %v", idsOf(res.Deferred))
	}
	for _, m := range res.Plan.Milestones {
		if m.ID == FeedbackReviewMilestoneID {
			t.Fatal("empty deferred set must not emit a feedback-review milestone")
		}
	}
	// a plan that already carries the milestone gets it stripped when deferred is empty.
	withMilestone := GatePlanFeedback(basePlan(), reg, 13).Plan
	stripped := ApplyFeedbackReview(withMilestone, nil)
	for _, m := range stripped.Milestones {
		if m.ID == FeedbackReviewMilestoneID {
			t.Fatal("empty deferred must strip a pre-existing feedback-review milestone, not leave it hollow")
		}
	}
	raw, _ := json.Marshal(stripped)
	if v := ValidatePlanBytes(raw); !v.OK {
		t.Fatalf("stripped plan failed validation: %v", v.Errors)
	}
}

// TestApplyFeedbackReviewDoesNotMutateInput: ApplyFeedbackReview must not alter the caller's plan
// or its milestone backing array.
func TestApplyFeedbackReviewDoesNotMutateInput(t *testing.T) {
	p := basePlan()
	before := mustJSON(t, p)
	_ = ApplyFeedbackReview(p, GateFeedback(gateFixture(t), 13).Deferred)
	if after := mustJSON(t, p); before != after {
		t.Errorf("input plan mutated:\nbefore=%s\nafter=%s", before, after)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
