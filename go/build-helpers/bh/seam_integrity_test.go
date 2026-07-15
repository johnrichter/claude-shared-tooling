package bh

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// M13.P7.T1 — the seam-integrity sweep (design.md line 105; plan.md M13.P7.T1).
//
// This is the ONE cross-reader test: it constructs data exercising every new M13 field —
// typed file_surface {path,required,kind} (M13.P3.T1), pause-event {reason_enum,at,task_id?}
// (M13.P4.T1), escalation-event {trigger,tier,route,at,task_id?} (M13.P4.T2), and the accounting
// additions cost_status/specs_as_of/build_helpers_sha/identity/list_vs_actual_notes/per-task+
// per-run Usage (M13.P2.T1/T3/T4/T5) — through its producer (the bh writer: InitExec/RecordTask/
// RecordPauseEvent/RecordEscalationEvent/SetAccounting/SetAccountingUnresolved/
// RecordListVsActualNote) AND every enumerated reader: retrieve (SC10), render (SC15),
// migrate-project (SC14), archive (SC11, incl. archive×slip-count/firing-count), the
// schema-version-bump upgrade path (MigrateExec), and resume/next/batch/classify.
//
// Every assertion here targets behavior that did not exist before its corresponding M13 field
// task landed — the typed Kind-aware batch disjointness check, the closed-set agreement between
// RecordEscalationEvent and ClassifyEscalation, the lossless schema-version-bump round trip for
// each new field, and the archive-preserves-telemetry invariant. Reverting any one of those field
// tasks removes the very function/field this test calls, so the test fails to compile or fails
// its assertion on pre-change code; it passes once every dependency (M13.P2.T1-T4, M13.P3.T1,
// M13.P4.T1-T2, plus M13.P3.T3/T4 for a consistent batch.go) has landed.
func TestSeamIntegrity_NewFieldsHandledByProducerAndEveryReader(t *testing.T) {
	// seamPlan is the shared fixture: three independent (no-dep) tasks under one milestone/phase,
	// carrying the typed file_surface shapes needed to exercise the kind-aware batch
	// disjointness seam (a dir-kind entry nesting a file-kind entry — FB19) alongside the
	// pause-event/escalation-event/accounting seams below.
	seamPlan := func() Plan {
		return Plan{
			Goal:            "seam-integrity demo",
			SuccessCriteria: []string{"seams hold"},
			Milestones: []Milestone{{
				ID: "M1", Name: "Seam milestone", Phases: []Phase{{
					ID: "M1.P1", Name: "Seam phase", Tasks: []Task{
						{ID: "M1.P1.T1", Name: "dir task", Summary: "owns pkg/a", Deliverable: "d1", Model: ModelSonnet5, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a1"},
							FileSurface: []FileSurfaceEntry{{Path: "pkg/a", Kind: FSDir, Required: true}}},
						{ID: "M1.P1.T2", Name: "nested file task", Summary: "touches pkg/a/foo.go", Deliverable: "d2", Model: ModelSonnet5, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a2"},
							FileSurface: []FileSurfaceEntry{{Path: "pkg/a/foo.go", Kind: FSFile}}},
						{ID: "M1.P1.T3", Name: "disjoint glob task", Summary: "touches pkg/c", Deliverable: "d3", Model: ModelSonnet5, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a3"},
							FileSurface: []FileSurfaceEntry{{Path: "pkg/c/*.go", Kind: FSGlob}}},
					},
				}},
			}},
		}
	}

	// ---- file_surface (M13.P3.T1) ----
	t.Run("file_surface", func(t *testing.T) {
		p := seamPlan()
		tasks := p.Milestones[0].Phases[0].Tasks
		t1, t2 := tasks[0], tasks[1]

		// producer: typed shape round-trips through JSON; the pre-M13.P3.T1 bare-string shape
		// still parses (backward compat) and resolves to the file-kind default.
		raw := mustBytes(t, p)
		var reparsed Plan
		if err := json.Unmarshal(raw, &reparsed); err != nil {
			t.Fatalf("typed file_surface must round-trip through JSON: %v", err)
		}
		if got := reparsed.Milestones[0].Phases[0].Tasks[0].FileSurface[0]; got.Path != "pkg/a" || got.Kind != FSDir || !got.Required {
			t.Fatalf("typed file_surface entry lost on round-trip: %+v", got)
		}
		var bare FileSurfaceEntry
		if err := json.Unmarshal([]byte(`"legacy/path.go"`), &bare); err != nil {
			t.Fatalf("backward-compat bare-string file_surface must still parse: %v", err)
		}
		if bare.Path != "legacy/path.go" || bare.Kind.Resolve() != FSFile {
			t.Fatalf("bare-string file_surface must default to {path, kind:file}: %+v", bare)
		}

		// validate: typed kind accepted; an unknown kind is rejected.
		if res := ValidatePlanBytes(raw); !res.OK {
			t.Fatalf("validate should accept typed file_surface entries: %v", res.Errors)
		}
		bad := seamPlan()
		bad.Milestones[0].Phases[0].Tasks[0].FileSurface[0].Kind = FileSurfaceKind("bogus")
		if res := ValidatePlanBytes(mustBytes(t, bad)); res.OK {
			t.Fatal("validate should reject an unknown file_surface kind")
		}

		// retrieve/SC10: typed shape projects at task- and field-level, without aliasing the
		// canonical plan (read-only contract).
		got, err := RetrievePlan(p, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
		if err != nil {
			t.Fatal(err)
		}
		task := got.(Task)
		if len(task.FileSurface) != 1 || task.FileSurface[0].Kind != FSDir || !task.FileSurface[0].Required {
			t.Fatalf("RetrievePlan(task) lost the typed file_surface: %+v", task.FileSurface)
		}
		task.FileSurface[0].Path = "mutated"
		if p.Milestones[0].Phases[0].Tasks[0].FileSurface[0].Path == "mutated" {
			t.Fatal("RetrievePlan(task) must return a deep copy, not alias the canonical plan")
		}
		fv, err := RetrievePlan(p, RetrieveInput{Level: LevelField, ID: "M1.P1.T2", Field: "file_surface"})
		if err != nil {
			t.Fatal(err)
		}
		fs := fv.(FieldValue).Value.([]FileSurfaceEntry)
		if len(fs) != 1 || fs[0].Path != "pkg/a/foo.go" || fs[0].Kind != FSFile {
			t.Fatalf("RetrievePlan(field) lost the typed file_surface: %+v", fs)
		}

		// render/SC15: plan.md surfaces path + resolved kind + the required marker.
		md := RenderPlan(p, PlanDocMeta{})
		if !strings.Contains(md, "file surface: pkg/a (dir, required)") {
			t.Fatalf("RenderPlan must surface the typed file_surface (path, kind, required); got:\n%s", md)
		}
		if !strings.Contains(md, "file surface: pkg/a/foo.go (file)") {
			t.Fatalf("RenderPlan must surface a non-required file-kind entry without the required marker; got:\n%s", md)
		}

		ex, err := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if err != nil {
			t.Fatal(err)
		}

		// migrate-project/SC14: an already-typed entry passes through untouched.
		pm := seamPlan()
		rep, err := MigrateProject(&pm, &ex, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rep.Changes {
			if c.Field == "file_surface" {
				t.Fatalf("migrate-project must never touch an already-typed file_surface, got change: %+v", c)
			}
		}
		if !reflect.DeepEqual(pm.Milestones[0].Phases[0].Tasks[0].FileSurface, p.Milestones[0].Phases[0].Tasks[0].FileSurface) {
			t.Fatal("migrate-project altered a typed file_surface entry")
		}

		// resume/next/batch/classify: Kind is load-bearing for batch's disjointness gate. A
		// kind-blind check (fileSurfaceOverlap on bare path text) cannot see that T2's file sits
		// inside T1's declared directory — only the kind-aware layer (sharedPackageSymbolRisk,
		// which reads Kind) does. Prove the kind-blind predicate alone would misjudge this pair
		// disjoint (the exact FB19 defect), then prove BatchTasks — which runs BOTH layers —
		// still refuses to admit the pair together.
		if fileSurfaceOverlap(surfacePaths(t1.FileSurface), surfacePaths(t2.FileSurface)) {
			t.Fatal("test setup invalid: the kind-blind path predicate must NOT already catch this nesting (would defeat the point of this assertion)")
		}
		batch := BatchTasks(ex, p, 3)
		admittedT1, admittedT2, admittedT3 := false, false, false
		for _, bt := range batch.Tasks {
			switch bt.ID {
			case "M1.P1.T1":
				admittedT1 = true
			case "M1.P1.T2":
				admittedT2 = true
			case "M1.P1.T3":
				admittedT3 = true
			}
		}
		if admittedT1 && admittedT2 {
			t.Fatalf("BatchTasks admitted a dir-kind entry alongside a file-kind entry nested inside it — the typed Kind field is not being consulted: %+v", batch.Tasks)
		}
		if !admittedT1 && !admittedT2 {
			t.Fatalf("BatchTasks admitted neither of the conflicting pair — expected exactly one: %+v", batch.Tasks)
		}
		if !admittedT3 {
			t.Fatalf("BatchTasks should still admit the disjoint T3 alongside whichever of T1/T2 it picked: %+v", batch.Tasks)
		}

		// archive/SC11: the typed entry survives into ArchivedTask verbatim.
		for _, id := range []string{"M1.P1.T1", "M1.P1.T2", "M1.P1.T3"} {
			if err := RecordTask(&ex, id, RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("cafe123")}, at0); err != nil {
				t.Fatal(err)
			}
		}
		out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
		if err != nil {
			t.Fatal(err)
		}
		archT1, ok := findArchivedTask(out.Archive, "M1.P1.T1")
		if !ok {
			t.Fatal("archived task M1.P1.T1 not found in archive.json")
		}
		if len(archT1.FileSurface) != 1 || archT1.FileSurface[0].Kind != FSDir || !archT1.FileSurface[0].Required {
			t.Fatalf("archive must preserve the typed file_surface entry, got: %+v", archT1.FileSurface)
		}
	})

	// ---- pause-event (M13.P4.T1) ----
	t.Run("pause_event", func(t *testing.T) {
		p := seamPlan()
		ex, err := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if err != nil {
			t.Fatal(err)
		}

		// producer: RecordPauseEvent is the sole writer, gated on the closed reason_enum set with
		// no partial effect on rejection.
		if err := RecordPauseEvent(&ex, PauseReason("bogus"), "", at0); err == nil {
			t.Fatal("RecordPauseEvent must reject an out-of-set reason_enum")
		}
		if len(ex.PauseEvents) != 0 {
			t.Fatalf("a rejected reason_enum must not persist a partial event: %+v", ex.PauseEvents)
		}
		if err := RecordPauseEvent(&ex, PauseGit, "M1.P1.T1", "2026-07-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := RecordPauseEvent(&ex, PauseApproval, "", "2026-07-01T00:05:00Z"); err != nil {
			t.Fatal(err)
		}
		if got := MechanicalSlipCount(ex); got != 1 {
			t.Fatalf("MechanicalSlipCount = %d, want 1 (git is mechanical; approval is operator-facing and excluded)", got)
		}

		// resume/next/batch: scheduling is unaffected by pause-event telemetry (checked against
		// an otherwise-identical, event-free ExecState before any task status diverges).
		clean, _ := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if !reflect.DeepEqual(NextTask(ex, p), NextTask(clean, p)) {
			t.Fatal("pause-event telemetry must not affect next-task scheduling")
		}
		if !reflect.DeepEqual(BatchTasks(ex, p, 3), BatchTasks(clean, p, 3)) {
			t.Fatal("pause-event telemetry must not affect batch scheduling")
		}

		// retrieve/SC10: the task-level projection is uncorrupted by the plan-level event log.
		outline, err := RetrieveExec(ex, RetrieveInput{Level: LevelOutline})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(outline.([]OutlineEntry)); got != 3 {
			t.Fatalf("RetrieveExec outline row count = %d, want 3 (pause events must not leak into the task projection)", got)
		}

		// render/SC15: the human-readable mirror's log section carries the pause narrative.
		md := RenderExecution(ex, p)
		if !strings.Contains(md, "PAUSE git M1.P1.T1") || !strings.Contains(md, "PAUSE approval") {
			t.Fatalf("RenderExecution must surface the recorded pause events in the log; got:\n%s", md)
		}

		// schema-version-bump upgrade path (MigrateExec): a legacy-versioned snapshot upgrades in
		// place, losslessly preserving pause_events.
		legacy := ex
		legacy.SchemaVersion = 0
		raw := mustBytes(t, legacy)
		var reloaded ExecState
		if err := json.Unmarshal(raw, &reloaded); err != nil {
			t.Fatal(err)
		}
		if err := MigrateExec(&reloaded); err != nil {
			t.Fatal(err)
		}
		if reloaded.SchemaVersion != CurrentExecSchemaVersion {
			t.Fatalf("MigrateExec did not bump schema_version: got %d, want %d", reloaded.SchemaVersion, CurrentExecSchemaVersion)
		}
		if !reflect.DeepEqual(reloaded.PauseEvents, ex.PauseEvents) {
			t.Fatalf("MigrateExec must preserve pause_events unchanged across the schema-version bump: got %+v, want %+v", reloaded.PauseEvents, ex.PauseEvents)
		}

		// migrate-project/SC14: same lossless-preservation contract through the higher-level tool.
		pm := seamPlan()
		if _, err := MigrateProject(&pm, &ex, false); err != nil {
			t.Fatal(err)
		}
		wantEvents := []PauseEvent{
			{ReasonEnum: PauseGit, At: "2026-07-01T00:00:00Z", TaskID: "M1.P1.T1"},
			{ReasonEnum: PauseApproval, At: "2026-07-01T00:05:00Z"},
		}
		if !reflect.DeepEqual(ex.PauseEvents, wantEvents) {
			t.Fatalf("migrate-project must never alter pause_events: got %+v, want %+v", ex.PauseEvents, wantEvents)
		}

		// archive/SC11 × slip-count: archiving a wholly-done milestone must not touch the
		// plan-level pause-event log or its derived slip count — pause events are session-scoped,
		// never per-task, so the archive op (which moves TASKS, not session state) cannot drop them.
		for _, id := range []string{"M1.P1.T1", "M1.P1.T2", "M1.P1.T3"} {
			if err := RecordTask(&ex, id, RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("cafe123")}, at0); err != nil {
				t.Fatal(err)
			}
		}
		before := MechanicalSlipCount(ex)
		out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
		if err != nil {
			t.Fatal(err)
		}
		after := MechanicalSlipCount(out.Exec)
		if before != after || after != 1 {
			t.Fatalf("archive must preserve the mechanical-slip count unchanged: before=%d after=%d", before, after)
		}
		if !reflect.DeepEqual(out.Exec.PauseEvents, ex.PauseEvents) {
			t.Fatal("archive must not alter the pause-event log")
		}
	})

	// ---- escalation-event (M13.P4.T2) ----
	t.Run("escalation_event", func(t *testing.T) {
		p := seamPlan()
		ex, err := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if err != nil {
			t.Fatal(err)
		}

		// producer + classify: RecordEscalationEvent is gated by the SAME closed trigger set
		// ClassifyEscalation routes on (escalationTiers) — an out-of-set condition is rejected by
		// the writer and independently routed to no-escalation by the classifier; they can never
		// silently disagree.
		if err := RecordEscalationEvent(&ex, EscalationTrigger("bogus-trigger"), "high", RouteMagistrate, "", at0); err == nil {
			t.Fatal("RecordEscalationEvent must reject an out-of-set trigger")
		}
		if got := ClassifyEscalation(EscalationInput{Condition: "bogus-trigger"}).Route; got != RouteNoEscalation {
			t.Fatalf("classify must independently route the same out-of-set condition to no-escalation, got %q", got)
		}

		trigger := TriggerFailedTaskTriage
		wantTier := escalationTiers[trigger]
		if err := RecordEscalationEvent(&ex, trigger, wantTier, RouteMagistrate, "M1.P1.T1", "2026-07-01T00:10:00Z"); err != nil {
			t.Fatal(err)
		}
		if got := MagistrateFiringCount(ex); got != 1 {
			t.Fatalf("MagistrateFiringCount = %d, want 1", got)
		}
		cr := ClassifyEscalation(EscalationInput{Condition: string(trigger)})
		if cr.Route != RouteMagistrate || cr.Tier != wantTier {
			t.Fatalf("classify disagrees with the recorded escalation event: route=%q tier=%q, want %q/%q", cr.Route, cr.Tier, RouteMagistrate, wantTier)
		}
		known := false
		for _, tr := range EscalationTriggers() {
			if tr == trigger {
				known = true
			}
		}
		if !known {
			t.Fatalf("recorded trigger %q is not in the enumerated closed set %v", trigger, EscalationTriggers())
		}

		// resume/next/batch: scheduling is unaffected by escalation telemetry.
		clean, _ := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if !reflect.DeepEqual(NextTask(ex, p), NextTask(clean, p)) {
			t.Fatal("escalation-event telemetry must not affect next-task scheduling")
		}
		if !reflect.DeepEqual(BatchTasks(ex, p, 3), BatchTasks(clean, p, 3)) {
			t.Fatal("escalation-event telemetry must not affect batch scheduling")
		}

		// retrieve/SC10: non-corruption of the task-level projection.
		if _, err := RetrieveExec(ex, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"}); err != nil {
			t.Fatalf("RetrieveExec must still project a task correctly alongside a populated escalation-event log: %v", err)
		}

		// render/SC15: the human-readable mirror's log carries the escalation narrative.
		md := RenderExecution(ex, p)
		wantLine := "ESCALATE " + string(trigger) + " -> " + RouteMagistrate + " (tier " + wantTier + ") M1.P1.T1"
		if !strings.Contains(md, wantLine) {
			t.Fatalf("RenderExecution must surface the recorded escalation event %q; got:\n%s", wantLine, md)
		}

		// schema-version-bump upgrade path (MigrateExec) + migrate-project/SC14: lossless
		// preservation across both the direct upgrade and the higher-level migration tool.
		legacy := ex
		legacy.SchemaVersion = 0
		raw := mustBytes(t, legacy)
		var reloaded ExecState
		if err := json.Unmarshal(raw, &reloaded); err != nil {
			t.Fatal(err)
		}
		if err := MigrateExec(&reloaded); err != nil {
			t.Fatal(err)
		}
		if reloaded.SchemaVersion != CurrentExecSchemaVersion {
			t.Fatalf("MigrateExec did not bump schema_version: got %d", reloaded.SchemaVersion)
		}
		if !reflect.DeepEqual(reloaded.EscalationEvents, ex.EscalationEvents) {
			t.Fatalf("MigrateExec must preserve escalation_events unchanged across the schema-version bump: got %+v, want %+v", reloaded.EscalationEvents, ex.EscalationEvents)
		}
		pm := seamPlan()
		if _, err := MigrateProject(&pm, &ex, false); err != nil {
			t.Fatal(err)
		}
		if len(ex.EscalationEvents) != 1 || ex.EscalationEvents[0].Trigger != trigger {
			t.Fatalf("migrate-project must never alter escalation_events: got %+v", ex.EscalationEvents)
		}

		// archive/SC11 × firing-count (the escalation-event analog of archive×slip-count):
		// archiving a wholly-done milestone must not touch the plan-level escalation-event log or
		// its derived magistrate-firing count.
		for _, id := range []string{"M1.P1.T1", "M1.P1.T2", "M1.P1.T3"} {
			if err := RecordTask(&ex, id, RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("cafe123")}, at0); err != nil {
				t.Fatal(err)
			}
		}
		before := MagistrateFiringCount(ex)
		out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
		if err != nil {
			t.Fatal(err)
		}
		after := MagistrateFiringCount(out.Exec)
		if before != after || after != 1 {
			t.Fatalf("archive must preserve the magistrate-firing count unchanged: before=%d after=%d", before, after)
		}
	})

	// ---- accounting: cost_status/specs_as_of/build_helpers_sha/identity/list_vs_actual_notes,
	// per-task+per-run Usage (M13.P2.T1/T3/T4/T5) ----
	t.Run("accounting", func(t *testing.T) {
		rates := loadTestRates(t) // skips this subtest if the co-located specs fixture is absent
		mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
		sources, handles := discoverFixtureSources(t, mainPath)
		defer closeHandles(handles)
		acct, err := Account(nil, sources, rates, "2026-07-05T00:00:00Z")
		if err != nil {
			t.Fatalf("Account: %v", err)
		}

		p := seamPlan()
		ex, err := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		if err != nil {
			t.Fatal(err)
		}

		// producer: SetAccounting derives O + the ACC5 identity and persists cost_status/
		// specs_as_of/build_helpers_sha; RecordTask persists per-task Usage (ACC3's four token
		// classes), recomputed into per-run Usage; RecordListVsActualNote appends the ACC5 §5 note.
		SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef123")
		taskUsage := &Usage{InputTokens: 100, CacheCreationTokens: 20, CacheReadTokens: 30, OutputTokens: 40, TotalTokens: 190, Turns: 2}
		if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Usage: taskUsage}, "2026-07-05T00:05:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := RecordListVsActualNote(&ex, 9.99, "operator", "2026-07-05T00:10:00Z"); err != nil {
			t.Fatal(err)
		}
		acctState := ex.RunConfig.Accounting
		if acctState == nil || acctState.CostStatus != "" || acctState.SpecsAsOf != "2026-07-03" || acctState.BuildHelpersSHA != "deadbeef123" {
			t.Fatalf("SetAccounting did not persist cost_status/specs_as_of/build_helpers_sha: %+v", acctState)
		}
		if acctState.Identity == nil {
			t.Fatal("SetAccounting did not persist the ACC5 identity result")
		}
		if len(acctState.ListVsActualNotes) != 1 {
			t.Fatalf("RecordListVsActualNote did not persist a note: %+v", acctState.ListVsActualNotes)
		}
		if ex.RunConfig.Usage == nil || ex.RunConfig.Usage.TotalTokens != taskUsage.TotalTokens {
			t.Fatalf("per-run Usage (ACC3) not recomputed from the per-task Usage: %+v", ex.RunConfig.Usage)
		}

		// resume/next/batch: scheduling is unaffected by the accounting snapshot's presence.
		bare, _ := InitExec(p, InitExecOptions{Slug: "seam", At: at0})
		withAcct := bare
		withAcct.RunConfig.Accounting = acctState
		if !reflect.DeepEqual(NextTask(bare, p), NextTask(withAcct, p)) {
			t.Fatal("the accounting snapshot must not affect next-task scheduling")
		}
		if !reflect.DeepEqual(BatchTasks(bare, p, 3), BatchTasks(withAcct, p, 3)) {
			t.Fatal("the accounting snapshot must not affect batch scheduling")
		}

		// retrieve/SC10: per-task Usage (ACC3) projects at task- and field-level.
		got, err := RetrieveExec(ex, RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
		if err != nil {
			t.Fatal(err)
		}
		if u := got.(ExecTask).Usage; u == nil || *u != *taskUsage {
			t.Fatalf("RetrieveExec(task) lost the recorded Usage: %+v", u)
		}
		fv, err := RetrieveExec(ex, RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "usage"})
		if err != nil {
			t.Fatal(err)
		}
		fu, ok := fv.(FieldValue).Value.(*Usage)
		if !ok || fu == nil || *fu != *taskUsage {
			t.Fatalf("RetrieveExec(field usage) = %+v, want %+v", fv, taskUsage)
		}

		// render/SC15: the human mirror surfaces the accounting-derived dollar total (the
		// documentary sub-fields — cost_status/identity/list-vs-actual — are not separately
		// printed, but their presence must not corrupt or suppress the figure that IS rendered).
		md := RenderExecution(ex, p)
		wantCost := fmt.Sprintf("$%.4f", acctState.CostUSD)
		if !strings.Contains(md, wantCost) {
			t.Fatalf("RenderExecution must surface the accounting-derived cost total %s; got:\n%s", wantCost, md)
		}

		// schema-version-bump upgrade path (MigrateExec): the whole accounting snapshot (incl.
		// cost_status/specs_as_of/build_helpers_sha/identity/list_vs_actual_notes) survives a
		// legacy schema_version upgrade byte-exact.
		legacy := ex
		legacy.SchemaVersion = 0
		raw := mustBytes(t, legacy)
		var reloaded ExecState
		if err := json.Unmarshal(raw, &reloaded); err != nil {
			t.Fatal(err)
		}
		if err := MigrateExec(&reloaded); err != nil {
			t.Fatal(err)
		}
		if reloaded.SchemaVersion != CurrentExecSchemaVersion {
			t.Fatalf("MigrateExec did not bump schema_version: got %d", reloaded.SchemaVersion)
		}
		if !reflect.DeepEqual(reloaded.RunConfig.Accounting, ex.RunConfig.Accounting) {
			t.Fatalf("MigrateExec must preserve the accounting snapshot unchanged across a schema-version upgrade:\ngot=%+v\nwant=%+v", reloaded.RunConfig.Accounting, ex.RunConfig.Accounting)
		}

		// migrate-project/SC14: never fabricates or alters an existing accounting snapshot —
		// compared by independent JSON snapshots (not pointer identity) taken before/after.
		beforeJSON := mustBytes(t, ex.RunConfig.Accounting)
		pm := seamPlan()
		if _, err := MigrateProject(&pm, &ex, false); err != nil {
			t.Fatal(err)
		}
		afterJSON := mustBytes(t, ex.RunConfig.Accounting)
		if string(beforeJSON) != string(afterJSON) {
			t.Fatalf("migrate-project must never alter the accounting snapshot:\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
		}

		// archive/SC11: a done task's Usage (ACC3) is preserved in its tombstone, the whole-session
		// accounting snapshot is untouched, and the whole-project Usage total is unchanged by
		// archiving (recomputeTotals sums live Tasks + Archived).
		for _, id := range []string{"M1.P1.T1", "M1.P1.T2", "M1.P1.T3"} {
			if err := RecordTask(&ex, id, RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("cafe123")}, at0); err != nil {
				t.Fatal(err)
			}
		}
		preTotal := ex.RunConfig.Usage.TotalTokens
		preAccountingJSON := mustBytes(t, ex.RunConfig.Accounting)
		out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
		if err != nil {
			t.Fatal(err)
		}
		if out.Exec.RunConfig.Usage == nil || out.Exec.RunConfig.Usage.TotalTokens != preTotal {
			t.Fatalf("archive must leave the whole-project Usage total unchanged: got %+v, want total %d", out.Exec.RunConfig.Usage, preTotal)
		}
		if got := mustBytes(t, out.Exec.RunConfig.Accounting); string(got) != string(preAccountingJSON) {
			t.Fatalf("archive must not alter the whole-session accounting snapshot:\nbefore=%s\nafter=%s", preAccountingJSON, got)
		}
		var tombUsage *Usage
		for _, tomb := range out.Exec.Archived {
			if tomb.ID == "M1.P1.T1" {
				tombUsage = tomb.Usage
			}
		}
		if tombUsage == nil || *tombUsage != *taskUsage {
			t.Fatalf("archive must preserve the per-task Usage (ACC3) in its tombstone: %+v", tombUsage)
		}
	})
}
