package bh

// batch_adversarial_test.go: test-engineer-authored adversarial suite for BatchTasks.
// Each test maps to a specific acceptance criterion and exercises failure modes, boundaries,
// and edge cases beyond the implementer's batch_test.go.

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- plan-building helpers ----

// mkTask builds a Task with required fields and an optional file_surface.
func mkTask(id, surf string, deps ...string) Task {
	t := Task{
		ID:           id,
		Summary:      id,
		Deliverable:  "d",
		Model:        ModelSonnet46,
		Effort:       EffortMedium,
		TestStrategy: "u",
		Acceptance:   []string{"a"},
		Deps:         deps,
	}
	if surf != "" {
		t.FileSurface = []FileSurfaceEntry{{Path: surf + "/*.go"}}
	}
	return t
}

// singlePhasePlan wraps tasks into M1.P1.
func singlePhasePlan(tasks ...Task) Plan {
	return Plan{
		Goal:            "g",
		SuccessCriteria: []string{"s"},
		Milestones: []Milestone{
			{
				ID:   "M1",
				Name: "m1",
				Phases: []Phase{
					{ID: "M1.P1", Name: "p1", Tasks: tasks},
				},
			},
		},
	}
}

// mkExecMode initialises an ExecState for a plan+mode and marks tasks as done.
func mkExecMode(t *testing.T, p Plan, mode string, done ...string) ExecState {
	t.Helper()
	ex, err := InitExec(p, InitExecOptions{Slug: "adv", At: at0, Pause: mode})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range done {
		s := StatusDone
		if err := RecordTask(&ex, id, RecordFields{Status: &s}, at0); err != nil {
			t.Fatal(err)
		}
	}
	return ex
}

// refByIDFromPlan returns the TaskRef for id, searching the whole plan.
func refByIDFromPlan(p Plan, id string) TaskRef {
	for _, r := range WalkTasks(p) {
		if r.Task.ID == id {
			return r
		}
	}
	return TaskRef{}
}

// threeMillPlan: 3 milestones, 2 phases each, 2 independent disjoint tasks each.
func threeMillPlan() Plan {
	return Plan{
		Goal:            "g",
		SuccessCriteria: []string{"s"},
		Milestones: []Milestone{
			{
				ID:   "M1",
				Name: "m1",
				Phases: []Phase{
					{ID: "M1.P1", Name: "p1", Tasks: []Task{
						mkTask("M1.P1.T1", "a"),
						mkTask("M1.P1.T2", "b"),
					}},
					{ID: "M1.P2", Name: "p2", Tasks: []Task{
						mkTask("M1.P2.T3", "c"),
						mkTask("M1.P2.T4", "d"),
					}},
				},
			},
			{
				ID:   "M2",
				Name: "m2",
				Phases: []Phase{
					{ID: "M2.P1", Name: "p1", Tasks: []Task{
						mkTask("M2.P1.T5", "e"),
						mkTask("M2.P1.T6", "f"),
					}},
					{ID: "M2.P2", Name: "p2", Tasks: []Task{
						mkTask("M2.P2.T7", "g"),
						mkTask("M2.P2.T8", "h"),
					}},
				},
			},
			{
				ID:   "M3",
				Name: "m3",
				Phases: []Phase{
					{ID: "M3.P1", Name: "p1", Tasks: []Task{
						mkTask("M3.P1.T9", "i"),
						mkTask("M3.P1.T10", "j"),
					}},
				},
			},
		},
	}
}

// ---- AC1: pairwise dependency-independent and file_surface-disjoint ----

// Three tasks in a chain (A->B->C): only A is eligible.
func TestAdv_ChainOnlyFirstEligible(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "a"),
		mkTask("M1.P1.T2", "b", "M1.P1.T1"),
		mkTask("M1.P1.T3", "c", "M1.P1.T2"),
	)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("chain: only A eligible; got %v", ids)
	}
}

// Two tasks with exactly the same file surface must not co-batch.
func TestAdv_ExactSameFileSurfaceNeverCoBatch(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg/main.go"}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkg/main.go"}}
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 {
		t.Fatalf("exact same literal surface: must be batch-of-1, got %v", ids)
	}
}

// One surface is a glob that matches the other's literal path.
func TestAdv_GlobMatchesLiteralNeverCoBatch(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg/*.go"}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkg/main.go"}}
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 {
		t.Fatalf("glob matches literal: must be batch-of-1, got %v", ids)
	}
	if ids[0] != "M1.P1.T1" {
		t.Fatalf("topo-first task (T1) must be anchor; got %v", ids)
	}
}

// ---- AC2: task mode returns ≤1 ----

// task mode on a plan with many eligible disjoint tasks still returns exactly one.
func TestAdv_TaskModeDisjointPlanReturnsOne(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "task")
	r := BatchTasks(ex, p, 8)
	if len(r.Tasks) > 1 {
		t.Fatalf("task mode must return ≤1 task; got %v", batchIDs(r))
	}
	if len(r.Tasks) == 0 {
		t.Fatalf("task mode: non-empty plan should return 1 task; done=%v blocked=%v", r.Done, r.Blocked)
	}
}

// task mode with max=1 and max=8 should return the same count.
func TestAdv_TaskModeMaxEquivalence(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "task")
	if len(BatchTasks(ex, p, 1).Tasks) != len(BatchTasks(ex, p, 8).Tasks) {
		t.Fatal("task mode: max=1 and max=8 must return the same count (≤1)")
	}
}

// ---- AC2: phase mode stays within the current (anchor) phase ----

// After anchor phase completes, the next phase becomes the new boundary.
func TestAdv_PhaseModeBoundaryAdvancesAfterPhaseComplete(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "phase", "M1.P1.T1", "M1.P1.T2")
	r := BatchTasks(ex, p, 8)
	for _, id := range batchIDs(r) {
		ref := refByIDFromPlan(p, id)
		if ref.Phase.ID != "M1.P2" {
			t.Fatalf("after P1 done, anchor must be in M1.P2; task %s is in %s", id, ref.Phase.ID)
		}
	}
}

// Phase mode must not include tasks from a different phase.
func TestAdv_PhaseModeDoesNotLeakOtherPhase(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "phase")
	for _, id := range batchIDs(BatchTasks(ex, p, 8)) {
		ref := refByIDFromPlan(p, id)
		if ref.Phase.ID != "M1.P1" {
			t.Fatalf("phase mode leaked out-of-anchor-phase task %s (phase %s)", id, ref.Phase.ID)
		}
	}
}

// ---- AC2: milestone mode may span phases but not cross milestones ----

// Milestone mode must not leak into the next milestone.
func TestAdv_MilestoneModeDoesNotCrossMilestone(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "milestone")
	for _, id := range batchIDs(BatchTasks(ex, p, 8)) {
		ref := refByIDFromPlan(p, id)
		if ref.Milestone.ID != "M1" {
			t.Fatalf("milestone mode leaked into milestone %s (task %s)", ref.Milestone.ID, id)
		}
	}
}

// After M1 fully done, anchor shifts to M2 and respects M2 boundary.
func TestAdv_MilestoneModeBoundaryAdvances(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "milestone", "M1.P1.T1", "M1.P1.T2", "M1.P2.T3", "M1.P2.T4")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) == 0 {
		t.Fatal("after M1 done, M2 tasks should be returned")
	}
	for _, id := range ids {
		ref := refByIDFromPlan(p, id)
		if ref.Milestone.ID != "M2" {
			t.Fatalf("after M1 done: expected tasks in M2; got %s in %s", id, ref.Milestone.ID)
		}
	}
}

// ---- AC2: none mode spans the plan ----

// none mode reaches eligible tasks in non-contiguous milestones.
func TestAdv_NoneModeSpansNonContiguousMilestones(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "none",
		"M1.P1.T1", "M1.P1.T2", "M1.P2.T3", "M1.P2.T4",
		"M2.P1.T5", "M2.P1.T6", "M2.P2.T7", "M2.P2.T8",
	)
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if !hasID(ids, "M3.P1.T9") && !hasID(ids, "M3.P1.T10") {
		t.Fatalf("none mode must reach M3; got %v", ids)
	}
}

// ---- AC3: absent/empty file_surface forces batch-of-1 ----

// A candidate with nil surface cannot join an already-admitted task; a subsequent
// disjoint task CAN join since the nil-surface candidate was rejected (not admitted).
func TestAdv_AbsentSurfaceCandidateRejected(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "a")
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = nil
	t3 := mkTask("M1.P1.T3", "c")
	p := singlePhasePlan(t1, t2, t3)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if !hasID(ids, "M1.P1.T1") {
		t.Fatalf("T1 (anchor) must be in batch; got %v", ids)
	}
	if hasID(ids, "M1.P1.T2") {
		t.Fatalf("T2 has nil surface and must not co-batch with T1; got %v", ids)
	}
	// T3 is disjoint from T1 and T2 was rejected (not admitted), so T3 should be admitted.
	if !hasID(ids, "M1.P1.T3") {
		t.Fatalf("T3 is disjoint from admitted T1 and should co-batch; got %v", ids)
	}
}

// Anchor with empty (non-nil) slice surface must batch alone.
func TestAdv_AnchorEmptySliceSurfaceAlone(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{} // empty slice, not nil
	t2 := mkTask("M1.P1.T2", "b")
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("anchor with empty slice surface must be batch-of-1; got %v", ids)
	}
}

// ---- AC4: done/blocked outcomes match NextTask ----

// All tasks terminal => both NextTask and BatchTasks return Done=true.
func TestAdv_DoneStateConsistency(t *testing.T) {
	p := threeMillPlan()
	all := []string{
		"M1.P1.T1", "M1.P1.T2", "M1.P2.T3", "M1.P2.T4",
		"M2.P1.T5", "M2.P1.T6", "M2.P2.T7", "M2.P2.T8",
		"M3.P1.T9", "M3.P1.T10",
	}
	ex := mkExecMode(t, p, "none", all...)
	nr := NextTask(ex, p)
	br := BatchTasks(ex, p, 8)
	if !nr.Done {
		t.Fatalf("NextTask should be done; got %+v", nr)
	}
	if !br.Done {
		t.Fatalf("BatchTasks should be done; got %+v", br)
	}
	if len(br.Tasks) != 0 {
		t.Fatalf("done BatchResult must have no tasks; got %v", batchIDs(br))
	}
}

// Cycle in a two-task plan => both NextTask and BatchTasks return non-empty Blocked.
// Uses a dedicated two-task plan so the cycle blocks the entire plan (no other schedulable tasks).
func TestAdv_CycleBlockedConsistency(t *testing.T) {
	// validPlan has T1 (no deps) and T2 (dep T1); add T1 dep on T2 to form a cycle.
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Deps = []string{"M1.P1.T2"}
	ex := mkExecMode(t, p, "none")
	nr := NextTask(ex, p)
	br := BatchTasks(ex, p, 8)
	if len(nr.Blocked) == 0 {
		t.Fatalf("NextTask should be blocked (cycle); got %+v", nr)
	}
	if len(br.Blocked) == 0 {
		t.Fatalf("BatchTasks should be blocked (cycle); got %+v", br)
	}
}

// T1 in-progress (non-terminal, no deps) => eligible in both NextTask and BatchTasks.
func TestAdv_DepStallInProgress(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "a"),
		mkTask("M1.P1.T2", "b", "M1.P1.T1"),
	)
	ex, _ := InitExec(p, InitExecOptions{Slug: "s", At: at0, Pause: "none"})
	s := StatusInProgress
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: &s}, at0); err != nil {
		t.Fatal(err)
	}
	nr := NextTask(ex, p)
	br := BatchTasks(ex, p, 8)
	if nr.Task == nil || nr.Task.ID != "M1.P1.T1" {
		t.Fatalf("NextTask should return in-progress T1; got %+v", nr)
	}
	if len(br.Tasks) != 1 || br.Tasks[0].ID != "M1.P1.T1" {
		t.Fatalf("BatchTasks should return T1; got tasks=%v done=%v blocked=%v", batchIDs(br), br.Done, br.Blocked)
	}
}

// ---- AC1/AC5: max clamping boundary conditions ----

func TestAdv_MaxOneClamp(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 1)
	if len(r.Tasks) != 1 {
		t.Fatalf("max=1 should return exactly 1 task; got %v", batchIDs(r))
	}
}

func TestAdv_MaxZeroUsesHardCap(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "none")
	if n := len(BatchTasks(ex, p, 0).Tasks); n > MaxBatch {
		t.Fatalf("max=0 must not exceed MaxBatch=%d; got %d", MaxBatch, n)
	}
}

func TestAdv_MaxNegativeUsesHardCap(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "none")
	if n := len(BatchTasks(ex, p, -1).Tasks); n > MaxBatch {
		t.Fatalf("max=-1 must not exceed MaxBatch=%d; got %d", MaxBatch, n)
	}
}

func TestAdv_MaxAboveHardCapClamped(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "none")
	if n := len(BatchTasks(ex, p, 999).Tasks); n > MaxBatch {
		t.Fatalf("max=999 must not exceed hard cap %d; got %d", MaxBatch, n)
	}
}

// ---- glob-overlap adversarial cases (fileSurfaceOverlap + globsMayIntersect) ----

func TestAdv_DoubleStarConservative(t *testing.T) {
	if !fileSurfaceOverlap([]string{"**"}, []string{"a/b/c.go"}) {
		t.Fatal("** must conservatively overlap any concrete path")
	}
	if !fileSurfaceOverlap([]string{"pkg/**"}, []string{"pkg/sub/deep/file.go"}) {
		t.Fatal("pkg/** must overlap pkg/sub/deep/file.go")
	}
}

func TestAdv_DisjointTopLevelDirs(t *testing.T) {
	if fileSurfaceOverlap([]string{"alpha/*.go"}, []string{"beta/*.go"}) {
		t.Fatal("alpha/*.go and beta/*.go are provably disjoint")
	}
}

func TestAdv_SubPathWithoutBridge(t *testing.T) {
	if fileSurfaceOverlap([]string{"a/b.go"}, []string{"a/b/c.go"}) {
		t.Fatal("a/b.go and a/b/c.go differ in depth without ** bridge — must be disjoint")
	}
}

func TestAdv_MultiGlobCrossPairOverlap(t *testing.T) {
	a := []string{"x/*.go", "shared/main.go"}
	b := []string{"y/*.go", "shared/main.go"}
	if !fileSurfaceOverlap(a, b) {
		t.Fatal("shared/main.go appears in both surfaces; must report overlap")
	}
}

func TestAdv_MultiGlobFullyDisjoint(t *testing.T) {
	if fileSurfaceOverlap([]string{"x/*.go", "y/*.go"}, []string{"z/*.go", "w/*.go"}) {
		t.Fatal("all globs are in distinct dirs; must be disjoint")
	}
}

// Both meta segments: conservatively overlap.
func TestAdv_TwoWildcardSegmentsConservative(t *testing.T) {
	// segmentsMayMatch("*.go","*.txt"): both have meta -> returns true (cannot disprove intersection).
	if !globsMayIntersect("pkg/*.go", "pkg/*.txt") {
		t.Fatal("*.go and *.txt: both have meta -> conservatively overlap (cannot disprove)")
	}
}

// Two literal paths with distinct first segments are provably disjoint.
func TestAdv_TwoLiteralSegmentsDistinct(t *testing.T) {
	if globsMayIntersect("alpha/main.go", "beta/main.go") {
		t.Fatal("alpha/main.go and beta/main.go are provably disjoint")
	}
}

// ---- Superseded dep: not StatusDone -> T2 should be blocked ----

func TestAdv_SupersededDepNotDoneBlocks(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "a"),
		mkTask("M1.P1.T2", "b", "M1.P1.T1"),
	)
	ex, _ := InitExec(p, InitExecOptions{Slug: "s", At: at0, Pause: "none"})
	s := StatusSuperseded
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: &s}, at0); err != nil {
		t.Fatal(err)
	}
	// T1 is terminal (superseded), T2's dep is T1 which is NOT StatusDone.
	// T2 is not eligible. No anchor => Blocked.
	r := BatchTasks(ex, p, 8)
	if r.Done {
		t.Fatal("T2 has dep on superseded-but-not-done T1; should not be Done")
	}
	if len(r.Tasks) > 0 {
		t.Fatalf("T2 must not be eligible when dep is superseded; got tasks %v", batchIDs(r))
	}
	if len(r.Blocked) == 0 {
		t.Fatalf("should be blocked; got %+v", r)
	}
}

// ---- Topo order determines greedy selection when max < pool size ----

func TestAdv_TopoOrderDeterminesSelection(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "a"),
		mkTask("M1.P1.T2", "b"),
		mkTask("M1.P1.T3", "c"),
	)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 2)
	ids := batchIDs(r)
	if len(ids) != 2 {
		t.Fatalf("max=2 with 3 eligible tasks: expected 2; got %v", ids)
	}
	if !hasID(ids, "M1.P1.T1") || !hasID(ids, "M1.P1.T2") {
		t.Fatalf("topo-first two tasks (T1,T2) must be selected; got %v", ids)
	}
}

// ---- Unrecognized pause_mode acts like none ----

func TestAdv_UnrecognizedPauseModeSpansPlan(t *testing.T) {
	p := threeMillPlan()
	ex := mkExecMode(t, p, "banana") // unrecognized -> default branch = whole plan
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) == 0 {
		t.Fatal("unrecognized mode should act like none and return tasks")
	}
	foundM1 := false
	for _, id := range ids {
		if refByIDFromPlan(p, id).Milestone.ID == "M1" {
			foundM1 = true
		}
	}
	if !foundM1 {
		t.Fatal("unrecognized mode (acts as none): M1 tasks should be in pool")
	}
}

// ---- Transitive dep closure correctness ----

// Diamond: D->C->A, D->C->B. With D done, only C eligible; A and B are dep-blocked.
func TestAdv_DiamondDepOnlyRootEligible(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "d"),
		mkTask("M1.P1.T2", "c", "M1.P1.T1"),
		mkTask("M1.P1.T3", "a", "M1.P1.T2"),
		mkTask("M1.P1.T4", "b", "M1.P1.T2"),
	)
	// T1 done -> T2 eligible; T3,T4 still dep-blocked on T2 (not done).
	ex := mkExecMode(t, p, "none", "M1.P1.T1")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T2" {
		t.Fatalf("diamond: only C (T2) eligible; got %v", ids)
	}
}

// Diamond: with T1+T2 done, T3 and T4 are both eligible, independent, disjoint -> co-batch.
func TestAdv_DiamondBothEligibleCoBatch(t *testing.T) {
	p := singlePhasePlan(
		mkTask("M1.P1.T1", "d"),
		mkTask("M1.P1.T2", "c", "M1.P1.T1"),
		mkTask("M1.P1.T3", "a", "M1.P1.T2"),
		mkTask("M1.P1.T4", "b", "M1.P1.T2"),
	)
	ex := mkExecMode(t, p, "none", "M1.P1.T1", "M1.P1.T2")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if !hasID(ids, "M1.P1.T3") || !hasID(ids, "M1.P1.T4") {
		t.Fatalf("diamond: A (T3) and B (T4) both eligible and disjoint -> must co-batch; got %v", ids)
	}
}

// Transitive dep: A<-B<-C means depLinked(A,C) is true.
func TestAdv_TransitiveDepLinkDetected(t *testing.T) {
	depsOf := map[string][]string{"A": {}, "B": {"A"}, "C": {"B"}}
	memo := map[string]map[string]bool{}
	closureFor := func(id string) map[string]bool { return transitiveDeps(id, depsOf, &memo) }
	if !depLinked("A", "C", closureFor) {
		t.Fatal("A and C are transitively linked (A<-B<-C); depLinked must return true")
	}
	if !depLinked("C", "A", closureFor) {
		t.Fatal("depLinked must be symmetric: C and A should also be linked")
	}
}

// B and C are independent of each other (only share ancestor A).
func TestAdv_IndependentTasksNotDepLinked(t *testing.T) {
	depsOf := map[string][]string{"A": {}, "B": {}, "C": {"A"}}
	memo := map[string]map[string]bool{}
	closureFor := func(id string) map[string]bool { return transitiveDeps(id, depsOf, &memo) }
	if depLinked("B", "C", closureFor) {
		t.Fatal("B and C share no dep path; depLinked must return false")
	}
}

// ---- Non-terminal (in-progress, failed) tasks are eligible ----

func TestAdv_InProgressIsEligible(t *testing.T) {
	p := singlePhasePlan(mkTask("M1.P1.T1", "a"))
	ex, _ := InitExec(p, InitExecOptions{Slug: "s", At: at0, Pause: "none"})
	s := StatusInProgress
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: &s}, at0); err != nil {
		t.Fatal(err)
	}
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("in-progress task with no deps should be eligible; got %v (done=%v blocked=%v)", ids, r.Done, r.Blocked)
	}
}

func TestAdv_FailedIsEligible(t *testing.T) {
	p := singlePhasePlan(mkTask("M1.P1.T1", "a"))
	ex, _ := InitExec(p, InitExecOptions{Slug: "s", At: at0, Pause: "none"})
	s := StatusFailed
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: &s}, at0); err != nil {
		t.Fatal(err)
	}
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("failed task with no deps should be eligible; got %v (done=%v blocked=%v)", ids, r.Done, r.Blocked)
	}
}

// ---- M13.P3.T4 (FB19): symbol-level disjointness screen + post-merge build gate ----
//
// sharedPackageSymbolRisk and RunPostMergeBuildGate do not exist on pre-change code, so this
// whole section fails to compile (not just fails an assertion) on pre-change code and compiles +
// passes after — the repo's standing proof-of-fix convention (sca-gate-semantics.md: "a fixture
// that passes both before and after proves nothing").

// ---- AC2: sharedPackageSymbolRisk (statically derivable half) ----

func TestSharedPackageSymbolRisk(t *testing.T) {
	cases := []struct {
		name string
		a, b []FileSurfaceEntry
		want bool
	}{
		{"dir covers a file inside it", []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}, []FileSurfaceEntry{{Path: "pkg/new.go"}}, true},
		{"dir covers itself named literally", []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}, []FileSurfaceEntry{{Path: "pkg"}}, true},
		{"dir does not cover a same-prefix sibling dir", []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}, []FileSurfaceEntry{{Path: "pkgish/file.go"}}, false},
		{"two plain files, same dir, neither kind=dir -> undecidable, no false alarm", []FileSurfaceEntry{{Path: "pkg/a.go"}}, []FileSurfaceEntry{{Path: "pkg/b.go"}}, false},
		{"dir on the b side still detected (symmetric)", []FileSurfaceEntry{{Path: "pkg/new.go"}}, []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}, true},
		{"no entries on either side -> nothing to compare", nil, []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}, false},
	}
	for _, c := range cases {
		if got := sharedPackageSymbolRisk(c.a, c.b); got != c.want {
			t.Fatalf("%s: sharedPackageSymbolRisk(%v,%v)=%v want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

// A dir-kind package surface and a file inside that same package are a same-package
// symbol-collision risk (FB19): fileSurfaceOverlap alone (literal path text, kind-blind) would
// call "pkg" and "pkg/newfile.go" disjoint (differing depth, no ** bridge) and let them co-batch.
func TestAdv_DirKindSurfaceForcesBatchOfOneWithFileInsideIt(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkg/newfile.go"}}
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 {
		t.Fatalf("dir-kind package surface + a file inside it must not co-batch (FB19 symbol risk); got %v", ids)
	}
}

// A dir-kind surface on "pkg" must not false-positive against an unrelated directory that merely
// shares a string prefix ("pkgish" is not nested under "pkg").
func TestAdv_DirKindSurfaceDoesNotFalsePositiveOnPrefixSibling(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkgish/file.go"}}
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 2 {
		t.Fatalf("unrelated pkgish/ must still co-batch with a pkg/ dir surface; got %v", ids)
	}
}

// Two plain (non-dir-kind) files in the same directory stay co-batchable: the genuinely
// undecidable case (brand-new symbol names, not knowable at schedule time) is intentionally left
// to the post-merge build gate below, not guessed at here — matches the existing pinned case in
// batch_test.go's TestFileSurfaceOverlapPredicate ("a/b/c.go" vs "a/b/d.go" -> disjoint).
func TestAdv_PlainSameDirFilesStillCoBatchUndecidableCase(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg/a.go"}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkg/b.go"}}
	p := singlePhasePlan(t1, t2)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 2 {
		t.Fatalf("plain same-dir files with no dir-kind declaration must co-batch (undecidable case, left to the build gate); got %v", ids)
	}
}

// ---- AC1/AC3: RunPostMergeBuildGate (always-on backstop) ----

// writeGoFixtureModule writes a minimal go.mod so `go build ./...` resolves the temp package.
func writeGoFixtureModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fb19fixture\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGoFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The exact FB19 scenario: two file_surface-disjoint files (a.go, b.go — different literal
// paths, no glob overlap) each add an identically-named package-level symbol (Foo). Git's
// text-merge sees no conflict (different files) and fileSurfaceOverlap sees no path overlap —
// both would pass on pre-change code. Only compiling the merged tree catches the collision.
func TestAdv_SamePackageDuplicateSymbolFailsPostMergeBuildGate(t *testing.T) {
	dir := t.TempDir()
	writeGoFixtureModule(t, dir)
	writeGoFixtureFile(t, dir, "a.go", "package pkg\n\nfunc Foo() {}\n")
	writeGoFixtureFile(t, dir, "b.go", "package pkg\n\nfunc Foo() {}\n")

	res := RunPostMergeBuildGate(dir, []string{"go", "build", "./..."})
	if res.OK {
		t.Fatalf("same-package duplicate symbol (Foo in a.go and b.go) must fail the post-merge build gate; output: %s", res.Output)
	}
}

// A normal disjoint batch (distinct symbol names, same package) still passes the gate — the gate
// must not false-positive on ordinary multi-file-same-package work.
func TestAdv_NormalDisjointBatchPassesPostMergeBuildGate(t *testing.T) {
	dir := t.TempDir()
	writeGoFixtureModule(t, dir)
	writeGoFixtureFile(t, dir, "a.go", "package pkg\n\nfunc Foo() {}\n")
	writeGoFixtureFile(t, dir, "b.go", "package pkg\n\nfunc Bar() {}\n")

	res := RunPostMergeBuildGate(dir, []string{"go", "build", "./..."})
	if !res.OK {
		t.Fatalf("a normal disjoint batch (distinct symbols) must pass the post-merge build gate; output: %s", res.Output)
	}
}

// An empty/undetected build command is a deliberate no-op (never blocks a merge when the target
// repo's toolchain can't be identified).
func TestAdv_PostMergeBuildGateNoOpWhenCommandUnresolved(t *testing.T) {
	if res := RunPostMergeBuildGate(t.TempDir(), nil); !res.OK {
		t.Fatal("an empty/undetected build command must no-op (ok=true), never block the merge")
	}
}

// ---- SDET-added: additional adversarial coverage beyond the implementer's own suite ----

// A non-empty but unresolvable/nonexistent command (exec.Command lookup failure, not just a
// nonzero exit from a real build) must still surface as a gate failure, not panic or silently OK.
func TestAdv_PostMergeBuildGateCommandNotFoundFails(t *testing.T) {
	res := RunPostMergeBuildGate(t.TempDir(), []string{"this-binary-does-not-exist-fb19"})
	if res.OK {
		t.Fatal("an unresolvable command must report a gate failure (OK=false), not no-op")
	}
}

// Three-file batch: dir-kind "pkg" package surface plus two plain files inside it must still
// force pkg's admission to reject both (not just the first candidate it happens to compare).
func TestAdv_DirKindSurfaceRejectsMultipleFilesInside(t *testing.T) {
	t1 := mkTask("M1.P1.T1", "")
	t1.FileSurface = []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}
	t2 := mkTask("M1.P1.T2", "")
	t2.FileSurface = []FileSurfaceEntry{{Path: "pkg/one.go"}}
	t3 := mkTask("M1.P1.T3", "")
	t3.FileSurface = []FileSurfaceEntry{{Path: "pkg/two.go"}}
	p := singlePhasePlan(t1, t2, t3)
	ex := mkExecMode(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("dir-kind pkg surface must reject every candidate whose path falls inside it; got %v", ids)
	}
}

// Two dir-kind entries naming a parent and its nested child directory are a same-package risk —
// the child is a path prefix match under the parent, so admitting both risks a merged package.
func TestAdv_NestedDirKindSurfacesOverlap(t *testing.T) {
	a := []FileSurfaceEntry{{Path: "pkg", Kind: FSDir}}
	b := []FileSurfaceEntry{{Path: "pkg/sub", Kind: FSDir}}
	if !sharedPackageSymbolRisk(a, b) {
		t.Fatal("a dir-kind parent and a dir-kind nested child directory must overlap (pkg/sub is under pkg)")
	}
}

// A trailing slash on the dir-kind path must not defeat the prefix match (path.Clean normalizes it).
func TestAdv_DirKindSurfaceTrailingSlashStillMatches(t *testing.T) {
	a := []FileSurfaceEntry{{Path: "pkg/", Kind: FSDir}}
	b := []FileSurfaceEntry{{Path: "pkg/new.go"}}
	if !sharedPackageSymbolRisk(a, b) {
		t.Fatal("trailing slash on a dir-kind path must not defeat the same-package overlap check")
	}
}
