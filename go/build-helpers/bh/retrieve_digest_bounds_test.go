package bh

import (
	"fmt"
	"testing"
)

// M4.P2.T2 — proves the build loop's working-set digest (the retrieval layer's L1 outline once
// at orientation + L3 task per active-batch member) stays bounded independent of total plan size,
// unlike a whole-doc read (json.Marshal(plan)/json.Marshal(exec)), which scales with it. This is
// the large-plan-fixture measurement the task's acceptance criterion requires.

// largePlan synthesizes a plan with nMilestones milestones x phasesPerM phases x tasksPerP tasks,
// each field sized to a realistic (not minimal) length, so byte measurements aren't an artifact of
// trivially short fixture strings.
func largePlan(nMilestones, phasesPerM, tasksPerP int) Plan {
	p := Plan{Goal: "large-plan resident-context fixture", SuccessCriteria: []string{"scale measurement"}}
	for m := 1; m <= nMilestones; m++ {
		mid := fmt.Sprintf("M%d", m)
		mil := Milestone{ID: mid, Name: fmt.Sprintf("Milestone %d — a representative multi-word milestone name for scale", m)}
		for ph := 1; ph <= phasesPerM; ph++ {
			phid := fmt.Sprintf("%s.P%d", mid, ph)
			phase := Phase{ID: phid, Name: fmt.Sprintf("Phase %d — a representative phase name for scale", ph)}
			for tk := 1; tk <= tasksPerP; tk++ {
				tid := fmt.Sprintf("%s.T%d", phid, tk)
				phase.Tasks = append(phase.Tasks, Task{
					ID:           tid,
					Summary:      fmt.Sprintf("Task %s — implement a representative unit of build work with a realistic summary length", tid),
					Deliverable:  "a representative deliverable description sized to realistic build-task length",
					Kind:         KindCode,
					Model:        ModelSonnet5,
					Effort:       EffortMedium,
					Thinking:     "a representative reasoning cue of realistic length for a build task",
					TestStrategy: "adversarial unit test suite covering every acceptance criterion",
					Acceptance:   []string{"criterion one of realistic length", "criterion two of realistic length"},
					FileSurface:  []FileSurfaceEntry{{Path: "path/to/representative/file/one.go"}, {Path: "path/to/representative/file/two.go"}},
				})
			}
			mil.Phases = append(mil.Phases, phase)
		}
		p.Milestones = append(p.Milestones, mil)
	}
	return p
}

// activeBatchBytes measures the loop's per-turn dispatch read: L3 task projections for the
// active batch only (up to MaxBatch tasks) — the resident payload the loop actually holds to
// dispatch a batch, sourced only through RetrievePlan (never a whole-doc read). L1 outline
// (fetched once at orientation, not per turn) is measured separately below.
func activeBatchBytes(t *testing.T, p Plan, ids []string) int {
	t.Helper()
	total := 0
	for _, id := range ids {
		task, err := RetrievePlan(p, RetrieveInput{Level: LevelTask, ID: id})
		if err != nil {
			t.Fatal(err)
		}
		total += len(mustBytes(t, task))
	}
	return total
}

// TestActiveBatchL3BytesIndependentOfPlanSize is the direct M4.P2.T2 measurement: the L3
// active-batch read the loop dispatches from every turn is the same size whether the plan has 20
// tasks or 900 (45x more) — it depends only on batch size (MaxBatch-capped) and per-task record
// length, never on total milestone/phase/task count elsewhere in the plan. Contrast: the whole-doc
// size (json.Marshal(plan)) scales directly with plan size — exactly the load `retrieve` removes
// from the loop's resident context (SKILL.md Cardinal rules: the working-set digest is a
// `retrieve` view, never a whole-doc read).
func TestActiveBatchL3BytesIndependentOfPlanSize(t *testing.T) {
	small := largePlan(2, 2, 5)   // 20 tasks
	large := largePlan(20, 3, 15) // 900 tasks — 45x more tasks than small

	smallWhole := len(mustBytes(t, small))
	largeWhole := len(mustBytes(t, large))
	if largeWhole <= smallWhole*10 {
		t.Fatalf("fixture invariant broken: expected whole-plan bytes to scale with plan size (small=%d, large=%d) so this test proves something real", smallWhole, largeWhole)
	}

	// Same fixed-size active batch on both plans — exactly what `batch --max N` (capped at
	// MaxBatch) hands the loop each turn, regardless of how many total tasks exist.
	batch := []string{"M1.P1.T1", "M1.P1.T2", "M1.P1.T3", "M1.P1.T4"}
	if len(batch) > MaxBatch {
		t.Fatalf("fixture batch (%d) exceeds MaxBatch (%d)", len(batch), MaxBatch)
	}

	smallBatchBytes := activeBatchBytes(t, small, batch)
	largeBatchBytes := activeBatchBytes(t, large, batch)

	// The decisive assertion: with the plan 45x bigger, the active-batch L3 payload for the SAME
	// batch is essentially unchanged (small tolerance for ID digit-length variance, e.g. "T4" vs
	// "T15") — never anywhere close to the whole-doc growth ratio.
	batchGrowthRatio := float64(largeBatchBytes) / float64(smallBatchBytes)
	if batchGrowthRatio > 1.2 {
		t.Fatalf("active-batch L3 bytes grew with plan size (small=%d, large=%d, %.2fx) — want flat (<=1.2x) regardless of total plan size", smallBatchBytes, largeBatchBytes, batchGrowthRatio)
	}
	wholeGrowthRatio := float64(largeWhole) / float64(smallWhole)

	// L1 outline (fetched once at orientation, not per turn) DOES grow with total entity count —
	// that is expected and out of scope here: capping it is the archive op's job. Logged for
	// honesty, asserted nowhere — it is not part of the per-turn active-batch payload bounded above.
	smallOutline, err := RetrievePlan(small, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	largeOutline, err := RetrievePlan(large, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("whole-doc bytes: small=%d large=%d (%.2fx) | active-batch L3 bytes (flat, %d tasks): small=%d large=%d (%.2fx) | L1 outline bytes (one-time, out of scope): small=%d large=%d",
		smallWhole, largeWhole, wholeGrowthRatio, len(batch), smallBatchBytes, largeBatchBytes, batchGrowthRatio,
		len(mustBytes(t, smallOutline)), len(mustBytes(t, largeOutline)))
}

// TestWorkingSetDigestExecTaskViewBoundedIndependentOfPlanSize mirrors the plan-side measurement
// for execution.json: the L3 exec-task read the loop uses for record/next status checks stays a
// fixed per-task size regardless of how many tasks are tracked in execution state.
func TestWorkingSetDigestExecTaskViewBoundedIndependentOfPlanSize(t *testing.T) {
	small := largePlan(2, 2, 5)
	large := largePlan(20, 3, 15)

	smallEx, err := InitExec(small, InitExecOptions{Slug: "small-demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	largeEx, err := InitExec(large, InitExecOptions{Slug: "large-demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}

	smallWhole := len(mustBytes(t, smallEx))
	largeWhole := len(mustBytes(t, largeEx))
	if largeWhole <= smallWhole*10 {
		t.Fatalf("fixture invariant broken: expected whole-exec bytes to scale with plan size (small=%d, large=%d)", smallWhole, largeWhole)
	}

	oneSmall, err := RetrieveExec(smallEx, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
	if err != nil {
		t.Fatal(err)
	}
	oneLarge, err := RetrieveExec(largeEx, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
	if err != nil {
		t.Fatal(err)
	}
	smallTaskBytes := len(mustBytes(t, oneSmall))
	largeTaskBytes := len(mustBytes(t, oneLarge))
	// A single task's own record is independent of how many other tasks exist in the doc.
	if largeTaskBytes > smallTaskBytes*2 {
		t.Fatalf("single exec-task L3 view grew with total plan size (small=%d, large=%d) — expected it to depend only on that task's own fields", smallTaskBytes, largeTaskBytes)
	}
}
