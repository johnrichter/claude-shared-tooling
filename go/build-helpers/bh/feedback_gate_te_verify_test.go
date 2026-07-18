package bh

import (
	"encoding/json"
	"math/rand"
	"testing"
)

// TestGateRandomizedPartitionProperty is an independent adversarial property test (authored
// separately from the implementation): across many random registers and thresholds, the
// partition must stay total, lossless, exactly-once, and rank-ordered — a hard-
// correctness invariant, exercised well beyond the implementer's fixed fixture.
func TestGateRandomizedPartitionProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20260704))
	for trial := 0; trial < 200; trial++ {
		n := rng.Intn(15) // 0..14 entries, including the empty-register edge
		reg := FeedbackRegister{}
		for i := 0; i < n; i++ {
			imp, urg := rng.Intn(5)+1, rng.Intn(5)+1
			var err error
			reg, err = AddFeedback(reg, FeedbackInput{
				Title: "t", Feedback: "f", Impact: imp, Urgency: urg,
			}, "")
			if err != nil {
				t.Fatalf("trial %d: add: %v", trial, err)
			}
		}
		threshold := rng.Intn(31) - 3 // -3..27, straddling the 1..25 criticality domain
		g := GateFeedback(reg, threshold)

		all := idsOf(ListFeedback(reg, FeedbackFilter{}))
		seen := map[string]int{}
		for _, e := range g.AmendNow {
			seen[e.ID]++
		}
		for _, e := range g.Deferred {
			seen[e.ID]++
		}
		if len(seen) != len(all) {
			t.Fatalf("trial %d threshold %d: routed %d distinct ids, register has %d", trial, threshold, len(seen), len(all))
		}
		for id, c := range seen {
			if c != 1 {
				t.Fatalf("trial %d threshold %d: id %s routed %d times", trial, threshold, id, c)
			}
		}
		for _, id := range all {
			if seen[id] != 1 {
				t.Fatalf("trial %d threshold %d: register entry %s missing from routed output", trial, threshold, id)
			}
		}

		// Every AmendNow entry has criticality >= threshold; every Deferred entry < threshold.
		for _, e := range g.AmendNow {
			if e.Criticality < threshold {
				t.Fatalf("trial %d threshold %d: amend-now entry %s criticality %d < threshold", trial, threshold, e.ID, e.Criticality)
			}
		}
		for _, e := range g.Deferred {
			if e.Criticality >= threshold {
				t.Fatalf("trial %d threshold %d: deferred entry %s criticality %d >= threshold", trial, threshold, e.ID, e.Criticality)
			}
		}

		// Rank order preserved within each bucket: criticality desc, id asc tiebreak.
		assertRanked(t, g.AmendNow, trial, threshold, "amend_now")
		assertRanked(t, g.Deferred, trial, threshold, "deferred")
	}
}

func assertRanked(t *testing.T, es []FeedbackEntry, trial, threshold int, bucket string) {
	t.Helper()
	for i := 1; i < len(es); i++ {
		prev, cur := es[i-1], es[i]
		if prev.Criticality < cur.Criticality {
			t.Fatalf("trial %d threshold %d: %s not rank-ordered at %d: %d before %d", trial, threshold, bucket, i, prev.Criticality, cur.Criticality)
		}
		if prev.Criticality == cur.Criticality && prev.ID > cur.ID {
			t.Fatalf("trial %d threshold %d: %s tie not id-ascending at %d: %s before %s", trial, threshold, bucket, i, prev.ID, cur.ID)
		}
	}
}

// TestGatePlanFeedbackAmendNowEntriesExcludedFromMilestone verifies the reconcile-exec handoff
// shape directly: no amend-now entry's task ever leaks into the feedback-review milestone, and no
// deferred entry is missing from it — the two outputs (amend_now slice, feedback-review milestone)
// must be a strict, disjoint mirror of the same partition.
func TestGatePlanFeedbackAmendNowEntriesExcludedFromMilestone(t *testing.T) {
	reg := gateFixture(t)
	res := GatePlanFeedback(basePlan(), reg, 6)

	var frm *Milestone
	for i := range res.Plan.Milestones {
		if res.Plan.Milestones[i].ID == FeedbackReviewMilestoneID {
			frm = &res.Plan.Milestones[i]
		}
	}
	if frm == nil {
		t.Fatal("expected a feedback-review milestone (deferred set non-empty)")
	}
	taskIDs := map[string]bool{}
	for _, tk := range frm.Phases[0].Tasks {
		taskIDs[tk.ID] = true
	}
	for _, e := range res.AmendNow {
		if taskIDs[feedbackReviewTaskID(e.ID)] {
			t.Errorf("amend-now entry %s leaked into feedback-review milestone as task %s", e.ID, feedbackReviewTaskID(e.ID))
		}
	}
	for _, e := range res.Deferred {
		if !taskIDs[feedbackReviewTaskID(e.ID)] {
			t.Errorf("deferred entry %s missing its feedback-review task %s", e.ID, feedbackReviewTaskID(e.ID))
		}
	}
	if len(taskIDs) != len(res.Deferred) {
		t.Errorf("feedback-review milestone has %d distinct tasks, want exactly %d (one per deferred entry)", len(taskIDs), len(res.Deferred))
	}
}

// TestGateTripleRerunStillIdempotent extends the implementer's double-rerun idempotency check one
// step further: feeding the gate's output plan back through the gate three generations deep must
// converge to a byte-stable fixed point, not merely be stable for one extra hop.
func TestGateTripleRerunStillIdempotent(t *testing.T) {
	reg := gateFixture(t)
	gen0 := GatePlanFeedback(basePlan(), reg, 12)
	gen1 := GatePlanFeedback(gen0.Plan, reg, 12)
	gen2 := GatePlanFeedback(gen1.Plan, reg, 12)
	a, b, c := mustJSON(t, gen0.Plan), mustJSON(t, gen1.Plan), mustJSON(t, gen2.Plan)
	if a != b || b != c {
		t.Fatalf("gate not a fixed point across 3 generations:\ngen0=%s\ngen1=%s\ngen2=%s", a, b, c)
	}
}

// TestClassifyFeedbackCriticalityBoundaryExhaustive sweeps every criticality in the documented 1-25
// domain against every threshold in a superset range, cross-checking ClassifyFeedbackCriticality
// against the raw >= definition directly (no reliance on GateFeedback machinery).
func TestClassifyFeedbackCriticalityBoundaryExhaustive(t *testing.T) {
	for crit := -2; crit <= 27; crit++ {
		for threshold := -2; threshold <= 27; threshold++ {
			want := RouteFeedbackReview
			if crit >= threshold {
				want = RouteFeedbackAmend
			}
			if got := ClassifyFeedbackCriticality(crit, threshold); got != want {
				t.Fatalf("ClassifyFeedbackCriticality(%d,%d) = %q, want %q", crit, threshold, got, want)
			}
		}
	}
}

// TestGateFeedbackOutputSlicesAreFresh guards against accidental aliasing between AmendNow and
// Deferred (e.g. a shared backing array causing appends to one to corrupt the other) by forcing a
// grow-past-capacity append on one and confirming the other is untouched.
func TestGateFeedbackOutputSlicesAreFresh(t *testing.T) {
	reg := gateFixture(t)
	g := GateFeedback(reg, 6)
	deferredBefore := append([]FeedbackEntry{}, g.Deferred...)
	// Force a reallocation-triggering mutation on AmendNow's slice header only.
	_ = append(g.AmendNow, FeedbackEntry{ID: "INJECTED"})
	for i, e := range g.Deferred {
		if e.ID != deferredBefore[i].ID {
			t.Fatalf("Deferred aliased with AmendNow's backing array: deferred[%d] changed from %s to %s", i, deferredBefore[i].ID, e.ID)
		}
	}
}

// TestFeedbackGateJSONShapeStable pins the wire shape the magistrate/CLI consumer depends on:
// threshold, amend_now, deferred (FeedbackGateResult additionally carries plan). A field rename
// here is a breaking change to the reconcile-exec handoff.
func TestFeedbackGateJSONShapeStable(t *testing.T) {
	reg := gateFixture(t)
	res := GatePlanFeedback(basePlan(), reg, 12)
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"threshold", "amend_now", "deferred", "plan"} {
		if _, ok := m[key]; !ok {
			t.Errorf("FeedbackGateResult JSON missing expected key %q", key)
		}
	}
}

// TestGateFeedbackStableOrderAcrossRepeatedCalls: two independent GateFeedback calls on the same
// register/threshold must return identical ordering, not merely identical sets — map iteration or
// non-stable sort would show up as ordering nondeterminism across repeated calls.
func TestGateFeedbackStableOrderAcrossRepeatedCalls(t *testing.T) {
	reg := gateFixture(t)
	first := GateFeedback(reg, 6)
	for i := 0; i < 20; i++ {
		again := GateFeedback(reg, 6)
		if !equalStrings(idsOf(again.AmendNow), idsOf(first.AmendNow)) {
			t.Fatalf("call %d: amend_now order drifted: %v vs %v", i, idsOf(again.AmendNow), idsOf(first.AmendNow))
		}
		if !equalStrings(idsOf(again.Deferred), idsOf(first.Deferred)) {
			t.Fatalf("call %d: deferred order drifted: %v vs %v", i, idsOf(again.Deferred), idsOf(first.Deferred))
		}
	}
}
