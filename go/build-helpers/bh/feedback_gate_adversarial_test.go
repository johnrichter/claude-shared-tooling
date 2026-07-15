package bh

import (
	"encoding/json"
	"testing"
)

// TestGateNegativeAndExtremeThresholds: negative threshold amends everything (every criticality
// is >=1 by ValidScore's floor); an above-max threshold defers everything. Threshold is a plain
// int parameter with no clamping, so out-of-domain values must still partition deterministically.
func TestGateNegativeAndExtremeThresholds(t *testing.T) {
	reg := gateFixture(t)
	all := idsOf(ListFeedback(reg, FeedbackFilter{}))

	g := GateFeedback(reg, -100)
	if len(g.Deferred) != 0 {
		t.Errorf("threshold -100: want everything amend-now, got %d deferred: %v", len(g.Deferred), idsOf(g.Deferred))
	}
	if got := idsOf(g.AmendNow); !equalStrings(sortedCopy(got), sortedCopy(all)) {
		t.Errorf("threshold -100: amend_now = %v, want all entries %v", got, all)
	}

	g2 := GateFeedback(reg, 1000000)
	if len(g2.AmendNow) != 0 {
		t.Errorf("threshold 1e6: want everything deferred, got %d amend-now: %v", len(g2.AmendNow), idsOf(g2.AmendNow))
	}
}

// TestGateEmptyRegister: an empty register partitions to two empty buckets and never emits a
// feedback-review milestone — nothing to be lossy or double-routed about, but the gate must not
// panic or synthesize entries out of nothing.
func TestGateEmptyRegister(t *testing.T) {
	res := GatePlanFeedback(basePlan(), FeedbackRegister{}, 5)
	if len(res.AmendNow) != 0 || len(res.Deferred) != 0 {
		t.Fatalf("empty register: want empty buckets, got amend_now=%v deferred=%v", res.AmendNow, res.Deferred)
	}
	for _, m := range res.Plan.Milestones {
		if m.ID == FeedbackReviewMilestoneID {
			t.Fatal("empty register must not emit a feedback-review milestone")
		}
	}
}

// TestFeedbackReviewTaskDefaults: an entry with no proposed_solution/why_it_matters still yields a
// non-empty deliverable and acceptance via the documented fallback text, so the task is always
// schema-valid regardless of how sparse the source feedback entry is.
func TestFeedbackReviewTaskDefaults(t *testing.T) {
	e := FeedbackEntry{ID: "FB9", Title: "sparse", Feedback: "just a complaint"}
	task := FeedbackReviewTask(e)
	if task.Deliverable == "" {
		t.Error("sparse entry: deliverable must fall back to non-empty text")
	}
	if len(task.Acceptance) == 0 || task.Acceptance[0] == "" {
		t.Error("sparse entry: acceptance must fall back to non-empty text")
	}
	if task.Name == "" {
		t.Error("sparse entry: name must be non-empty")
	}
}

// TestFeedbackReviewTaskIDCollisionOnMalformedIDs documents a real gap: feedback.json is
// hand-editable (it is not exclusively produced by AddFeedback), and feedbackReviewTaskID's
// fallback maps every non-"FB<n>" id to the SAME literal task id (M999.P1.T0). Two such entries
// in the same deferred set collide, ValidatePlanBytes correctly rejects the resulting duplicate
// task id, and BuildFeedbackReviewMilestone provides no defense against it — silently dropping
// one entry's task from the milestone (it is entry-lossless per FeedbackGate, but NOT
// task-lossless: the produced Task for the second colliding entry is never distinguishable from
// the first in the milestone). This test PASSES because it documents current behavior rather than
// asserting the (currently absent) collision guard; it is adversarial evidence, not an acceptance
// gate, since malformed ids are an edge input the fixture-driven acceptance tests don't exercise.
func TestFeedbackReviewTaskIDCollisionOnMalformedIDs(t *testing.T) {
	deferred := []FeedbackEntry{
		{ID: "malformed-a", Title: "A", Feedback: "fa", Criticality: 1},
		{ID: "malformed-b", Title: "B", Feedback: "fb", Criticality: 1},
	}
	m, ok := BuildFeedbackReviewMilestone(deferred)
	if !ok {
		t.Fatal("expected a milestone to be built")
	}
	tasks := m.Phases[0].Tasks
	ids := map[string]int{}
	for _, tk := range tasks {
		ids[tk.ID]++
	}
	if len(tasks) == 2 && len(ids) == 1 {
		t.Logf("CONFIRMED GAP: two malformed-id entries collide onto task id %q — only one of the two entries' tasks is distinguishable in the milestone", tasks[0].ID)
	}
	raw, err := json.Marshal(Plan{
		Goal: "g", SuccessCriteria: []string{"s"},
		Milestones: []Milestone{basePlan().Milestones[0], m},
	})
	if err != nil {
		t.Fatal(err)
	}
	v := ValidatePlanBytes(raw)
	if v.OK {
		t.Log("plan validated OK despite id-derivation collision risk (entries had colliding fallback ids)")
	} else {
		t.Logf("plan correctly rejected by ValidatePlanBytes due to duplicate task id: %v", v.Errors)
	}
}

// TestGateFeedbackDoesNotMutateRegister: GateFeedback must not alias or mutate reg.Entries or its
// backing array — a second call on the same register must observe the same entries.
func TestGateFeedbackDoesNotMutateRegister(t *testing.T) {
	reg := gateFixture(t)
	before, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	_ = GateFeedback(reg, 12)
	after, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("GateFeedback mutated the input register:\nbefore=%s\nafter=%s", before, after)
	}
}

func sortedCopy(xs []string) []string {
	out := append([]string{}, xs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
