package bh

import (
	"encoding/json"
	"reflect"
	"testing"
)

// archivePlan is a "done-heavy" two-milestone plan: M1 (2 tasks, later marked done) is the
// archive candidate; M2 (1 task, left not-started) has no cross-milestone dep on M1, so it stays
// schedulable regardless of whether M1 is live or archived — the cross-boundary dependency case
// (a live task depending on an archived one) is M8.P1.T3's scope (statusOrDefault/eligible
// archive-awareness), not this op's.
func archivePlan() Plan {
	return Plan{
		Goal:            "demo",
		SuccessCriteria: []string{"works"},
		Milestones: []Milestone{
			{ID: "M1", Name: "one", Phases: []Phase{
				{ID: "M1.P1", Name: "p1", Tasks: []Task{
					{ID: "M1.P1.T1", Summary: "t1", Deliverable: "d1", Model: ModelSonnet46, Effort: EffortMedium, Thinking: "th1", TestStrategy: "unit", Acceptance: []string{"a1"}, FileSurface: []FileSurfaceEntry{{Path: "a/*.go"}}},
					{ID: "M1.P1.T2", Summary: "t2", Deliverable: "d2", Model: ModelHaiku45, Effort: EffortLow, TestStrategy: "lint", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a2"}, FileSurface: []FileSurfaceEntry{{Path: "b/*.go"}}},
				}},
			}},
			{ID: "M2", Name: "two", Phases: []Phase{
				{ID: "M2.P1", Name: "p1", Tasks: []Task{
					{ID: "M2.P1.T1", Summary: "t3", Deliverable: "d3", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a3"}, FileSurface: []FileSurfaceEntry{{Path: "c/*.go"}}},
				}},
			}},
		},
	}
}

// doneM1Exec builds exec state for archivePlan with M1's two tasks recorded done (commit + cost)
// and M2 left not-started — the "done-heavy" fixture the test strategy calls for.
func doneM1Exec(t *testing.T) ExecState {
	t.Helper()
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("aaa1111"), Cost: ptrF(0.30), TokensOut: ptrI(1000)}, at0); err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("bbb2222"), Cost: ptrF(0.20), TokensOut: ptrI(500)}, at0); err != nil {
		t.Fatal(err)
	}
	return ex
}

func ptrI(i int64) *int64 { return &i }

func TestArchive_MovesTerminalMilestoneAndShrinksLiveDocs(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	preTotal := ex.RunConfig.SpentUSD
	preTok := ex.RunConfig.TokensOut

	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, "2026-07-01T00:00:00Z")
	if err != nil {
		t.Fatalf("archive should succeed on a wholly-done milestone: %v", err)
	}
	if got := out.Archived; len(got) != 1 || got[0] != "M1" {
		t.Fatalf("archived = %v, want [M1]", got)
	}
	if len(out.Skipped) != 0 {
		t.Fatalf("skipped should be empty, got %v", out.Skipped)
	}

	// Live plan shrinks to active+pending (M1 dropped, M2 kept).
	if len(out.Plan.Milestones) != 1 || out.Plan.Milestones[0].ID != "M2" {
		t.Fatalf("plan should shrink to just M2, got %+v", out.Plan.Milestones)
	}
	// Live exec drops M1's rows, keeps M2's.
	if len(out.Exec.Tasks) != 1 || out.Exec.Tasks[0].ID != "M2.P1.T1" {
		t.Fatalf("exec tasks should shrink to just M2.P1.T1, got %+v", out.Exec.Tasks)
	}

	// done/SHA/cost truth preserved in the tombstone index.
	if len(out.Exec.Archived) != 2 {
		t.Fatalf("expected 2 tombstones, got %d", len(out.Exec.Archived))
	}
	tomb := map[string]Tombstone{}
	for _, x := range out.Exec.Archived {
		tomb[x.ID] = x
	}
	if tomb["M1.P1.T1"].Status != StatusDone || tomb["M1.P1.T1"].Commit != "aaa1111" || tomb["M1.P1.T1"].CostUSD != 0.30 {
		t.Fatalf("T1 tombstone missing done/SHA/cost truth: %+v", tomb["M1.P1.T1"])
	}
	if tomb["M1.P1.T2"].Status != StatusDone || tomb["M1.P1.T2"].Commit != "bbb2222" || tomb["M1.P1.T2"].CostUSD != 0.20 {
		t.Fatalf("T2 tombstone missing done/SHA/cost truth: %+v", tomb["M1.P1.T2"])
	}

	// Full fidelity preserved in archive.json (plan-slice + exec-slice).
	if len(out.Archive.Milestones) != 1 || out.Archive.Milestones[0].ID != "M1" {
		t.Fatalf("archive doc should carry archived group M1, got %+v", out.Archive.Milestones)
	}
	g := out.Archive.Milestones[0]
	if len(g.Phases) != 1 || len(g.Phases[0].Tasks) != 2 {
		t.Fatalf("archived group should carry both M1 tasks, got %+v", g)
	}
	byID := map[string]ArchivedTask{}
	for _, at := range g.Phases[0].Tasks {
		byID[at.ID] = at
	}
	t1 := byID["M1.P1.T1"]
	if t1.Deliverable != "d1" || t1.TestStrategy != "unit" || t1.Thinking != "th1" || len(t1.Acceptance) != 1 || t1.Acceptance[0] != "a1" {
		t.Fatalf("archived plan-slice for T1 incomplete: %+v", t1)
	}
	if t1.Status != StatusDone || t1.Commit != "aaa1111" || t1.CostUSD != 0.30 || t1.TokensOut != 1000 {
		t.Fatalf("archived exec-slice for T1 incomplete: %+v", t1)
	}
	if out.Archive.Schema != ArchiveSchema {
		t.Fatalf("archive schema = %q, want %q", out.Archive.Schema, ArchiveSchema)
	}

	// Cost truth whole-project-true: recompute across live+archived == pre-archive total.
	if out.Exec.RunConfig.SpentUSD != preTotal {
		t.Fatalf("spent_usd changed by archiving: got %v, want unchanged %v", out.Exec.RunConfig.SpentUSD, preTotal)
	}
	if out.Exec.RunConfig.TokensOut != preTok {
		t.Fatalf("tokens_out changed by archiving: got %v, want unchanged %v", out.Exec.RunConfig.TokensOut, preTok)
	}
}

func TestArchive_ResumeAfterArchiveIsDeterministic(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	// The remaining live task (M2, independent of the archived M1) must still be the
	// deterministic next pick post-archive — resume is not disrupted by the live-doc shrink.
	r := NextTask(out.Exec, out.Plan)
	if r.Task == nil || r.Task.ID != "M2.P1.T1" {
		t.Fatalf("next after archive = %+v, want M2.P1.T1", r)
	}
	// Calling it again on the same (unmutated) state is byte-identical — deterministic.
	r2 := NextTask(out.Exec, out.Plan)
	if !reflect.DeepEqual(r, r2) {
		t.Fatalf("next is non-deterministic across repeated calls: %+v vs %+v", r, r2)
	}
}

func TestArchive_ExecJSONRoundTripIsLossless(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out.Exec)
	if err != nil {
		t.Fatal(err)
	}
	var back ExecState
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if err := MigrateExec(&back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Exec, back) {
		t.Fatalf("execution.json round-trip lost data:\nwant %+v\ngot  %+v", out.Exec, back)
	}

	ab, err := json.Marshal(out.Archive)
	if err != nil {
		t.Fatal(err)
	}
	var archiveBack ArchiveDoc
	if err := json.Unmarshal(ab, &archiveBack); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Archive, archiveBack) {
		t.Fatalf("archive.json round-trip lost data:\nwant %+v\ngot  %+v", out.Archive, archiveBack)
	}
}

func TestArchive_RefusesNonTerminalMilestoneWithNoPartialWrite(t *testing.T) {
	p := archivePlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Cost: ptrF(0.1)}, at0)
	// T2 left not-started -> M1 is not wholly terminal.
	_, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err == nil {
		t.Fatal("expected refusal: M1.P1.T2 is not terminal")
	}
}

func TestArchive_UnknownMilestoneIsAnError(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	if _, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M9"}}, at0); err == nil {
		t.Fatal("expected error for a milestone id that is neither live nor already archived")
	}
}

func TestArchive_ExplicitOnlyRefusesEmptySelection(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	if _, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{}, at0); err == nil {
		t.Fatal("expected error: no implicit 'archive everything done' default")
	}
}

func TestArchive_ReArchivingAlreadyArchivedMilestoneIsANoOp(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	first, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	// Re-invoke against the post-archive plan/exec/archive triple (simulating a second, later
	// operator call naming the same id) — must be a no-op, not an error and not a duplicate entry.
	second, err := Archive(first.Plan, first.Exec, first.Archive, ArchiveOptions{MilestoneIDs: []string{"M1"}}, "2026-07-02T00:00:00Z")
	if err != nil {
		t.Fatalf("re-archiving an already-archived milestone should be a no-op, got error: %v", err)
	}
	if len(second.Archived) != 0 || len(second.Skipped) != 1 || second.Skipped[0] != "M1" {
		t.Fatalf("expected no-op skip of M1, got archived=%v skipped=%v", second.Archived, second.Skipped)
	}
	if !reflect.DeepEqual(first.Plan, second.Plan) || !reflect.DeepEqual(first.Exec, second.Exec) || !reflect.DeepEqual(first.Archive, second.Archive) {
		t.Fatal("no-op re-archive must not mutate plan/exec/archive")
	}
}

func TestArchive_MultipleMilestonesInOneCall(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	if err := RecordTask(&ex, "M2.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("ccc3333"), Cost: ptrF(0.05)}, at0); err != nil {
		t.Fatal(err)
	}
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1", "M2"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Plan.Milestones) != 0 {
		t.Fatalf("plan should shrink to zero live milestones, got %+v", out.Plan.Milestones)
	}
	if len(out.Exec.Tasks) != 0 {
		t.Fatalf("exec should shrink to zero live tasks, got %+v", out.Exec.Tasks)
	}
	if len(out.Exec.Archived) != 3 {
		t.Fatalf("expected 3 tombstones, got %d", len(out.Exec.Archived))
	}
	if len(out.Archive.Milestones) != 2 {
		t.Fatalf("expected 2 archived groups, got %d", len(out.Archive.Milestones))
	}
}

func TestArchive_AccumulatesAcrossCalls(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	first, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	// Mark M2 done in the shrunk live exec, then archive it in a second call against the
	// already-populated archive doc — the store must accumulate, not overwrite.
	ex2 := first.Exec
	if err := RecordTask(&ex2, "M2.P1.T1", RecordFields{Status: ptr(StatusDone), Cost: ptrF(0.05)}, at0); err != nil {
		t.Fatal(err)
	}
	second, err := Archive(first.Plan, ex2, first.Archive, ArchiveOptions{MilestoneIDs: []string{"M2"}}, "2026-07-03T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Archive.Milestones) != 2 {
		t.Fatalf("archive doc should accumulate both groups across calls, got %d", len(second.Archive.Milestones))
	}
	if len(second.Exec.Archived) != 3 {
		t.Fatalf("tombstone index should accumulate across calls, got %d", len(second.Exec.Archived))
	}
}

// Crash-recovery idempotency. runArchive writes three files via separate temp-then-rename swaps,
// so a crash can land some renames and not others (the writes are not jointly atomic). The design
// (archival-design.md §4.4) requires retry to self-heal. These fixtures reproduce the two partial
// states and assert a second Archive call completes the archival without duplicating the immutable
// archive record or falsely refusing. Both FAIL on the pre-fix code (dup group / terminal refusal).

func TestArchive_Retry_CrashAfterArchiveOnly_NoDuplicate(t *testing.T) {
	// Crash after archive.json's rename, before plan.json/execution.json: live docs are still the
	// PRE-archive state, but the archive doc already holds M1.
	p := archivePlan()
	ex := doneM1Exec(t)
	first, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := Archive(p, ex, first.Archive, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatalf("retry after partial write must not error: %v", err)
	}
	if len(retry.Archive.Milestones) != 1 || retry.Archive.Milestones[0].ID != "M1" {
		t.Fatalf("archive must not duplicate the already-stored M1 group, got %+v", retry.Archive.Milestones)
	}
	if len(retry.Exec.Archived) != 2 {
		t.Fatalf("expected exactly 2 tombstones after retry, got %d", len(retry.Exec.Archived))
	}
	if len(retry.Plan.Milestones) != 1 || retry.Plan.Milestones[0].ID != "M2" {
		t.Fatalf("retry must still shrink the live plan to M2, got %+v", retry.Plan.Milestones)
	}
	if len(retry.Exec.Tasks) != 1 || retry.Exec.Tasks[0].ID != "M2.P1.T1" {
		t.Fatalf("retry must still drop M1's live rows, got %+v", retry.Exec.Tasks)
	}
}

func TestArchive_Retry_CrashAfterArchiveAndExec_NoRefusalNoDuplicate(t *testing.T) {
	// Crash after archive.json AND execution.json renamed, before plan.json: the archive doc holds
	// M1, the exec doc has M1's tombstones with its live rows already dropped, but plan.json still
	// lists M1. Pre-fix, the terminal precondition reads M1's tasks as not-started (absent from
	// live rows) and refuses; the fix reads their frozen tombstone status instead.
	p := archivePlan()
	ex := doneM1Exec(t)
	first, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := Archive(p, first.Exec, first.Archive, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatalf("retry must not refuse on already-tombstoned tasks: %v", err)
	}
	if len(retry.Archive.Milestones) != 1 {
		t.Fatalf("archive must not duplicate M1, got %+v", retry.Archive.Milestones)
	}
	if len(retry.Exec.Archived) != 2 {
		t.Fatalf("tombstone index must not duplicate, got %d", len(retry.Exec.Archived))
	}
	if len(retry.Plan.Milestones) != 1 || retry.Plan.Milestones[0].ID != "M2" {
		t.Fatalf("retry must complete the plan shrink to M2, got %+v", retry.Plan.Milestones)
	}
	if retry.Exec.RunConfig.SpentUSD != first.Exec.RunConfig.SpentUSD {
		t.Fatalf("retry changed whole-project spend: got %v, want %v", retry.Exec.RunConfig.SpentUSD, first.Exec.RunConfig.SpentUSD)
	}
}

func TestMigrateExec_V2ToV3_ArchivedAbsentTreatedEmpty(t *testing.T) {
	// A pre-M8 execution.json: schema_version 2, no "archived" key at all.
	raw := []byte(`{"schema":"execution-state/v1","schema_version":2,"project":"demo","tasks":[{"id":"M1.P1.T1","status":"done","cost_usd":0.5}],"run_config":{"spent_usd":0.5}}`)
	var ex ExecState
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatal(err)
	}
	if err := MigrateExec(&ex); err != nil {
		t.Fatal(err)
	}
	if ex.SchemaVersion != CurrentExecSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", ex.SchemaVersion, CurrentExecSchemaVersion)
	}
	if len(ex.Archived) != 0 {
		t.Fatalf("absent archived[] should migrate to empty, got %+v", ex.Archived)
	}
	if len(ex.Tasks) != 1 || ex.Tasks[0].ID != "M1.P1.T1" {
		t.Fatalf("pre-existing tasks must survive migration untouched: %+v", ex.Tasks)
	}
}
