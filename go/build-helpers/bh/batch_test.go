package bh

import "testing"

// batchPlan is a two-milestone plan with parallelism in several shapes, used to exercise the
// pause-boundary scoping and the disjointness/independence predicates.
//
//	M1.P1: T1 (no deps, surface a/*)        T2 (no deps, surface b/*)   -> independent + disjoint
//	M1.P2: T3 (deps T1, surface c/*)        T4 (deps T1, surface d/*)   -> independent of each other
//	M2.P1: T5 (no deps, surface e/*)        T6 (no deps, surface e/*)   -> overlapping surface
func batchPlan() Plan {
	return Plan{
		Goal:            "demo",
		SuccessCriteria: []string{"works"},
		Milestones: []Milestone{
			{ID: "M1", Name: "one", Phases: []Phase{
				{ID: "M1.P1", Name: "p1", Tasks: []Task{
					{ID: "M1.P1.T1", Summary: "t1", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "a/*.go"}}},
					{ID: "M1.P1.T2", Summary: "t2", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "b/*.go"}}},
				}},
				{ID: "M1.P2", Name: "p2", Tasks: []Task{
					{ID: "M1.P2.T3", Summary: "t3", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "c/*.go"}}},
					{ID: "M1.P2.T4", Summary: "t4", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "d/*.go"}}},
				}},
			}},
			{ID: "M2", Name: "two", Phases: []Phase{
				{ID: "M2.P1", Name: "p1", Tasks: []Task{
					{ID: "M2.P1.T5", Summary: "t5", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "e/*.go"}}},
					{ID: "M2.P1.T6", Summary: "t6", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "e/handler.go"}}},
				}},
			}},
		},
	}
}

func mkExec(t *testing.T, p Plan, mode string, done ...string) ExecState {
	t.Helper()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0, Pause: mode})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range done {
		if err := RecordTask(&ex, id, RecordFields{Status: ptr(StatusDone)}, at0); err != nil {
			t.Fatal(err)
		}
	}
	return ex
}

func batchIDs(r BatchResult) []string {
	out := make([]string, 0, len(r.Tasks))
	for _, t := range r.Tasks {
		out = append(out, t.ID)
	}
	return out
}

func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// (1) phase mode batches only same-phase independent disjoint tasks.
func TestBatchPhaseScope(t *testing.T) {
	p := batchPlan()
	ex := mkExec(t, p, "phase")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 2 || !hasID(ids, "M1.P1.T1") || !hasID(ids, "M1.P1.T2") {
		t.Fatalf("phase mode should batch T1+T2 only, got %v", ids)
	}
	for _, id := range ids {
		if id == "M1.P2.T3" || id == "M2.P1.T5" {
			t.Fatalf("phase mode leaked out-of-phase task: %v", ids)
		}
	}
}

// (2) milestone mode spans phases within the milestone but not across milestones.
func TestBatchMilestoneSpansPhases(t *testing.T) {
	p := batchPlan()
	// T1 done so P2's T3/T4 become eligible alongside P1's remaining T2.
	ex := mkExec(t, p, "milestone", "M1.P1.T1")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if !hasID(ids, "M1.P1.T2") || !hasID(ids, "M1.P2.T3") || !hasID(ids, "M1.P2.T4") {
		t.Fatalf("milestone mode should span M1 phases (T2,T3,T4), got %v", ids)
	}
	if hasID(ids, "M2.P1.T5") || hasID(ids, "M2.P1.T6") {
		t.Fatalf("milestone mode must not cross into M2, got %v", ids)
	}
}

// (3) none mode spans the whole plan.
func TestBatchNoneSpansPlan(t *testing.T) {
	p := batchPlan()
	ex := mkExec(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	// T1,T2 (P1) + exactly one of e-surface T5/T6 from M2; T3/T4 still dep-blocked on T1.
	if !hasID(ids, "M1.P1.T1") || !hasID(ids, "M1.P1.T2") {
		t.Fatalf("none mode should include P1 tasks, got %v", ids)
	}
	if !hasID(ids, "M2.P1.T5") {
		t.Fatalf("none mode should reach across the plan into M2, got %v", ids)
	}
	if hasID(ids, "M2.P1.T6") {
		t.Fatalf("T6 overlaps T5 surface and must not co-batch, got %v", ids)
	}
	if hasID(ids, "M1.P2.T3") || hasID(ids, "M1.P2.T4") {
		t.Fatalf("T3/T4 depend on not-yet-done T1 and must be ineligible, got %v", ids)
	}
}

// (4) task mode always returns at most one task.
func TestBatchTaskModeAtMostOne(t *testing.T) {
	p := batchPlan()
	ex := mkExec(t, p, "task")
	r := BatchTasks(ex, p, 8)
	if len(r.Tasks) != 1 || r.Tasks[0].ID != "M1.P1.T1" {
		t.Fatalf("task mode must return exactly the single next task, got %v", batchIDs(r))
	}
}

// (5) overlapping file_surface forces separate batches.
func TestBatchOverlappingSurfaceSeparates(t *testing.T) {
	p := batchPlan()
	// Force the anchor into M2.P1 by completing M1 entirely, with phase scope on M2.P1.
	ex := mkExec(t, p, "phase", "M1.P1.T1", "M1.P1.T2", "M1.P2.T3", "M1.P2.T4")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 {
		t.Fatalf("T5/T6 share e/ surface -> exactly one admitted, got %v", ids)
	}
	if ids[0] != "M2.P1.T5" {
		t.Fatalf("first-in-topo-order should win, got %v", ids)
	}
}

// (6) absent/empty file_surface => batch-of-1.
func TestBatchAbsentSurfaceAlone(t *testing.T) {
	p := batchPlan()
	// T2 has no file_surface -> overlaps everything.
	p.Milestones[0].Phases[0].Tasks[1].FileSurface = nil
	ex := mkExec(t, p, "phase")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("with T2 surface absent, anchor T1 must batch alone, got %v", ids)
	}
}

// (6b) the anchor itself having no surface forces a batch-of-1.
func TestBatchAnchorNoSurfaceAlone(t *testing.T) {
	p := batchPlan()
	p.Milestones[0].Phases[0].Tasks[0].FileSurface = nil // T1 anchor, no surface
	ex := mkExec(t, p, "phase")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("anchor without surface must batch alone, got %v", ids)
	}
}

// (7) dependency-linked tasks never co-batch.
func TestBatchDepLinkedNeverCoBatch(t *testing.T) {
	p := batchPlan()
	// Make T2 depend on T1 within the same phase; both are eligible only when... T2 is not
	// eligible until T1 done. So instead test the transitive guard directly: make T2 depend on
	// T1, T1 done, then T3(deps T1) and T2(deps T1) are both eligible but T3 also deps... keep
	// it simple: same-phase chain via a fresh plan.
	p.Milestones[0].Phases[0].Tasks[1].Deps = []string{"M1.P1.T1"}
	ex := mkExec(t, p, "phase")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	// T2 depends on not-done T1 -> ineligible; only T1 admitted.
	if len(ids) != 1 || ids[0] != "M1.P1.T1" {
		t.Fatalf("dep-linked T2 must not co-batch with T1, got %v", ids)
	}
}

// (7b) two eligible-but-dep-linked tasks are never admitted together.
func TestBatchTransitiveDepGuard(t *testing.T) {
	// A -> no deps; B deps A; C deps A. With A done, B and C are eligible & independent of each
	// other but each is dep-linked to A. A is terminal so not a candidate; B+C may co-batch.
	// Now add D deps B: with A,B done, C and D eligible; C independent of D -> co-batch ok.
	// To prove the guard fires, make D deps C as well; then with A done only B,C eligible and
	// independent -> they co-batch; verify A (done) never appears.
	p := Plan{
		Goal: "g", SuccessCriteria: []string{"s"},
		Milestones: []Milestone{{ID: "M1", Name: "m", Phases: []Phase{{ID: "M1.P1", Name: "p", Tasks: []Task{
			{ID: "M1.P1.T1", Summary: "A", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "a/*.go"}}},
			{ID: "M1.P1.T2", Summary: "B", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "b/*.go"}}},
			{ID: "M1.P1.T3", Summary: "C", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Deps: []string{"M1.P1.T2"}, Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "c/*.go"}}},
		}}}}},
	}
	// A done -> B eligible (deps A done), C ineligible (deps B not done). Only B admitted.
	ex := mkExec(t, p, "phase", "M1.P1.T1")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if len(ids) != 1 || ids[0] != "M1.P1.T2" {
		t.Fatalf("only B eligible (C dep-blocked on B), got %v", ids)
	}
}

// (8) --max and the hard cap 8 truncate correctly.
func TestBatchMaxAndHardCap(t *testing.T) {
	// 10 independent, disjoint, eligible tasks in one phase under "none".
	var tasks []Task
	for i := 1; i <= 10; i++ {
		id := "M1.P1.T" + itoa(i)
		tasks = append(tasks, Task{ID: id, Summary: id, Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "d" + itoa(i) + "/*.go"}}})
	}
	p := Plan{Goal: "g", SuccessCriteria: []string{"s"}, Milestones: []Milestone{{ID: "M1", Name: "m", Phases: []Phase{{ID: "M1.P1", Name: "p", Tasks: tasks}}}}}
	ex := mkExec(t, p, "none")

	if got := len(BatchTasks(ex, p, 3).Tasks); got != 3 {
		t.Fatalf("--max 3 should truncate to 3, got %d", got)
	}
	if got := len(BatchTasks(ex, p, 100).Tasks); got != MaxBatch {
		t.Fatalf("hard cap should bound at %d, got %d", MaxBatch, got)
	}
	if got := len(BatchTasks(ex, p, 0).Tasks); got != MaxBatch {
		t.Fatalf("max<=0 means no caller limit -> hard cap %d, got %d", MaxBatch, got)
	}
}

// (9) done/blocked match NextTask.
func TestBatchDoneMatchesNext(t *testing.T) {
	p := validPlan()
	ex := mkExec(t, p, "phase", "M1.P1.T1", "M1.P1.T2")
	nr := NextTask(ex, p)
	br := BatchTasks(ex, p, 8)
	if !nr.Done || !br.Done {
		t.Fatalf("both should be done: next=%+v batch=%+v", nr, br)
	}
}

func TestBatchBlockedMatchesNext(t *testing.T) {
	// Cycle -> NextTask blocked; BatchTasks blocked with the same cycle set.
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Deps = []string{"M1.P1.T2"} // T1<->T2 cycle
	ex := mkExec(t, p, "phase")
	nr := NextTask(ex, p)
	br := BatchTasks(ex, p, 8)
	if len(nr.Blocked) == 0 || len(br.Blocked) == 0 {
		t.Fatalf("both should be blocked: next=%+v batch=%+v", nr, br)
	}
	if len(nr.Blocked) != len(br.Blocked) {
		t.Fatalf("blocked sets differ: next=%v batch=%v", nr.Blocked, br.Blocked)
	}
}

// glob-overlap helper: directly exercise the conservative predicate.
func TestFileSurfaceOverlapPredicate(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a/*.go"}, []string{"b/*.go"}, false},                    // disjoint dirs
		{[]string{"a/*.go"}, []string{"a/x.go"}, true},                     // glob matches literal
		{[]string{"a/*.go"}, []string{"a/*.go"}, true},                     // identical
		{nil, []string{"a/*.go"}, true},                                    // absent overlaps everything
		{[]string{"a/*.go"}, nil, true},                                    // absent overlaps everything
		{[]string{}, []string{}, true},                                     // both empty -> overlap
		{[]string{"pkg/**"}, []string{"pkg/sub/x.go"}, true},               // doublestar swallows depth
		{[]string{"a/b/c.go"}, []string{"a/b/d.go"}, false},                // distinct literals
		{[]string{"a/b.go"}, []string{"a/b/c.go"}, false},                  // different depth, no bridge
		{[]string{"x/*.go", "y/*.go"}, []string{"z/*.go", "y/q.go"}, true}, // one pair overlaps
	}
	for i, c := range cases {
		if got := fileSurfaceOverlap(c.a, c.b); got != c.want {
			t.Fatalf("case %d overlap(%v,%v)=%v want %v", i, c.a, c.b, got, c.want)
		}
	}
}

// itoa avoids importing strconv just for the test loop.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---- archive-awareness (next/batch/classify/reconcile-exec + retrieval) ----
//
// crossArchiveDepPlan is the "live task depends on an archived task" shape archivePlan
// (archive_test.go) deliberately leaves uncovered — M2 there has no cross-milestone dep on M1.
// Here M2.P1.T1's sole dep IS M1.P1.T1, so admitting it past archival requires next/batch to
// resolve the archived tombstone's status, not just the live task list.
func crossArchiveDepPlan() Plan {
	return Plan{
		Goal:            "cross-boundary dep",
		SuccessCriteria: []string{"resume never stalls on an archived dep"},
		Milestones: []Milestone{
			{ID: "M1", Name: "one", Phases: []Phase{
				{ID: "M1.P1", Name: "p1", Tasks: []Task{
					{ID: "M1.P1.T1", Summary: "archived root", Deliverable: "d1", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a1"}, FileSurface: []FileSurfaceEntry{{Path: "a/*.go"}}},
				}},
			}},
			{ID: "M2", Name: "two", Phases: []Phase{
				{ID: "M2.P1", Name: "p1", Tasks: []Task{
					{ID: "M2.P1.T1", Summary: "live dependent", Deliverable: "d2", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a2"}, FileSurface: []FileSurfaceEntry{{Path: "b/*.go"}}},
				}},
			}},
		},
	}
}

// TestBatchArchivedDepResolvesAsDone mirrors TestNextTaskArchivedDepResolvesAsDone
// (execution_test.go) for BatchTasks: the shared archiveAwareStatus resolver must back both.
func TestBatchArchivedDepResolvesAsDone(t *testing.T) {
	p := crossArchiveDepPlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0, Pause: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone)}, at0); err != nil {
		t.Fatal(err)
	}
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatalf("archive should succeed on M1 (wholly done): %v", err)
	}
	r := BatchTasks(out.Exec, out.Plan, 4)
	if len(r.Tasks) != 1 || r.Tasks[0].ID != "M2.P1.T1" {
		t.Fatalf("batch should admit M2.P1.T1 past its archived dep, got %+v", r)
	}
}

// TestCrossSelector_ArchivedMilestoneConsistentAcrossEverySelector is the test-strategy's
// named regression: place a newly-archived milestone whose task is a live dependency and assert
// next, batch, reconcile-exec, classify, and retrieve all treat it consistently. Fails on pre-fix
// code at the next/batch/reconcile-exec assertions.
func TestCrossSelector_ArchivedMilestoneConsistentAcrossEverySelector(t *testing.T) {
	p := crossArchiveDepPlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0, Pause: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("aaa1111"), Cost: ptrF(0.10), TokensOut: ptrI(400)}, at0); err != nil {
		t.Fatal(err)
	}
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatalf("archive should succeed on M1 (wholly done): %v", err)
	}
	if len(out.Plan.Milestones) != 1 || out.Plan.Milestones[0].ID != "M2" {
		t.Fatalf("live plan should shrink to M2 only, got %+v", out.Plan.Milestones)
	}

	// next: never re-selects archived work, never stalls on the archived dep.
	if nr := NextTask(out.Exec, out.Plan); nr.Task == nil || nr.Task.ID != "M2.P1.T1" {
		t.Fatalf("next should schedule M2.P1.T1 past its archived dep, got %+v", nr)
	}

	// batch: same resolution.
	if br := BatchTasks(out.Exec, out.Plan, 4); len(br.Tasks) != 1 || br.Tasks[0].ID != "M2.P1.T1" {
		t.Fatalf("batch should admit M2.P1.T1 past its archived dep, got %+v", br)
	}

	// reconcile-exec: a design regenerate that still derives M1 unchanged must not resurrect it.
	reconciled := out.Exec
	ReconcileExec(&reconciled, out.Plan, p, "2026-07-04T00:00:00Z", "2026-07-04T00:05:00Z", "2026-07-04T00:10:00Z")
	for _, row := range reconciled.Tasks {
		if row.ID == "M1.P1.T1" {
			t.Fatalf("reconcile-exec re-admitted an archived task as a live row: %+v", row)
		}
	}
	if len(reconciled.Archived) != 1 || reconciled.Archived[0].ID != "M1.P1.T1" {
		t.Fatalf("reconcile-exec must leave the tombstone index untouched, got %+v", reconciled.Archived)
	}

	// classify: doc-presence routing is unaffected by the archived milestone.
	complete := "---\ntags:\n  - status:complete\nupdated: 2026-07-04T00:00:00Z\n---\n"
	cr := Classify(ClassifyInput{DesignPresent: true, DesignText: complete, PlanPresent: true, PlanProvenanceDesignUpdated: "2026-07-04T00:00:00Z", ExecutionPresent: true})
	if cr.Route != RouteReady {
		t.Fatalf("classify route = %q, want %q — archiving must not change doc-presence routing", cr.Route, RouteReady)
	}

	// retrieve: archived detail is fetchable via the SC10 API at every level (§3).
	outline, err := RetrieveArchive(out.Archive, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatalf("archive outline: %v", err)
	}
	if entries := outline.([]OutlineEntry); len(entries) != 3 { // M1, M1.P1, M1.P1.T1
		t.Fatalf("archive outline = %+v, want 3 entries", entries)
	}
	mv, err := RetrieveArchive(out.Archive, RetrieveInput{Level: LevelMilestone, ID: "M1"})
	if err != nil {
		t.Fatalf("archive milestone: %v", err)
	}
	if gv := mv.(GroupView); len(gv.Tasks) != 1 || gv.Tasks[0].ID != "M1.P1.T1" {
		t.Fatalf("archive milestone view = %+v", gv)
	}
	pv, err := RetrieveArchive(out.Archive, RetrieveInput{Level: LevelPhase, ID: "M1.P1"})
	if err != nil {
		t.Fatalf("archive phase: %v", err)
	}
	if gv := pv.(GroupView); len(gv.Tasks) != 1 || gv.Tasks[0].ID != "M1.P1.T1" {
		t.Fatalf("archive phase view = %+v", gv)
	}
	tv, err := RetrieveArchive(out.Archive, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
	if err != nil {
		t.Fatalf("archive task: %v", err)
	}
	at := tv.(ArchivedTask)
	if at.Status != StatusDone || at.Commit != "aaa1111" || at.Deliverable != "d1" || at.CostUSD != 0.10 {
		t.Fatalf("archived task detail incomplete: %+v", at)
	}
	fv, err := RetrieveArchive(out.Archive, RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "status"})
	if err != nil {
		t.Fatalf("archive field: %v", err)
	}
	if fv.(FieldValue).Value != StatusDone {
		t.Fatalf("archive field value = %+v, want done", fv)
	}
}

// ---- next/batch structurally refuse an orchestrator_only task for dispatch ----
//
// These tests guard against Task.OrchestratorOnly being a no-op: without this check, both
// NextTask and BatchTasks would unconditionally return the task in Task/Tasks for dispatch.

// orchestratorOnlyPlan is a single-task plan whose only task is orchestrator_only:true — the
// simplest case where the guard must fire on the very next call (the task is the anchor).
func orchestratorOnlyPlan() Plan {
	return Plan{
		Goal:            "demo",
		SuccessCriteria: []string{"works"},
		Milestones: []Milestone{
			{ID: "M1", Name: "one", Phases: []Phase{
				{ID: "M1.P1", Name: "p1", Tasks: []Task{
					{ID: "M1.P1.T1", Summary: "gate", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, OrchestratorOnly: true},
				}},
			}},
		},
	}
}

func TestNextRefusesOrchestratorOnlyTask(t *testing.T) {
	p := orchestratorOnlyPlan()
	ex := mkExec(t, p, "task")
	r := NextTask(ex, p)
	if r.Task != nil {
		t.Fatalf("NextTask emitted an orchestrator_only task for dispatch: %+v", r.Task)
	}
	if r.OrchestratorOnly == nil || r.OrchestratorOnly.ID != "M1.P1.T1" {
		t.Fatalf("NextTask did not flag the orchestrator_only refusal: %+v", r)
	}
	if r.Done || len(r.Blocked) > 0 {
		t.Fatalf("orchestrator_only refusal must not also report done/blocked: %+v", r)
	}
}

func TestBatchRefusesOrchestratorOnlyTask(t *testing.T) {
	p := orchestratorOnlyPlan()
	ex := mkExec(t, p, "task")
	r := BatchTasks(ex, p, 8)
	if len(r.Tasks) != 0 {
		t.Fatalf("BatchTasks emitted an orchestrator_only task for dispatch: %+v", r.Tasks)
	}
	if r.OrchestratorOnly == nil || r.OrchestratorOnly.ID != "M1.P1.T1" {
		t.Fatalf("BatchTasks did not flag the orchestrator_only refusal: %+v", r)
	}
	if r.Done || len(r.Blocked) > 0 {
		t.Fatalf("orchestrator_only refusal must not also report done/blocked: %+v", r)
	}
}

// TestBatchNeverAdmitsOrchestratorOnlyAlongsideIndependentTask: an orchestrator_only task that is
// dependency-independent and file_surface-disjoint from a normal, earlier-in-topo-order task must
// still never appear in the admitted batch — it stays pending until it becomes the anchor.
func TestBatchNeverAdmitsOrchestratorOnlyAlongsideIndependentTask(t *testing.T) {
	p := Plan{
		Goal: "demo", SuccessCriteria: []string{"works"},
		Milestones: []Milestone{
			{ID: "M1", Name: "one", Phases: []Phase{
				{ID: "M1.P1", Name: "p1", Tasks: []Task{
					{ID: "M1.P1.T1", Summary: "normal", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "a/*.go"}}},
					{ID: "M1.P1.T2", Summary: "gate", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}, FileSurface: []FileSurfaceEntry{{Path: "b/*.go"}}, OrchestratorOnly: true},
				}},
			}},
		},
	}
	ex := mkExec(t, p, "none")
	r := BatchTasks(ex, p, 8)
	ids := batchIDs(r)
	if hasID(ids, "M1.P1.T2") {
		t.Fatalf("BatchTasks admitted an orchestrator_only task into a dispatchable batch: %v", ids)
	}
	if !hasID(ids, "M1.P1.T1") {
		t.Fatalf("BatchTasks should still admit the independent normal task: %v", ids)
	}
}

// TestNextStillReturnsNormalTask is the negative-control sanity check: a plan with no
// orchestrator_only tasks is unaffected by the guard.
func TestNextStillReturnsNormalTask(t *testing.T) {
	p := batchPlan()
	ex := mkExec(t, p, "task")
	r := NextTask(ex, p)
	if r.Task == nil || r.Task.ID != "M1.P1.T1" {
		t.Fatalf("NextTask should still return a normal eligible task, got %+v", r)
	}
	if r.OrchestratorOnly != nil {
		t.Fatalf("NextTask flagged a refusal for a plan with no orchestrator_only tasks: %+v", r)
	}
}
