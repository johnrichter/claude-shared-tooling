package bh

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// legacyPlan builds a v1-shape plan: no task `name` (the SC15 delta), and one milestone/phase with
// an empty name to exercise the id-derived backfill. Task IDs/deps match legacyExecJSON's rows so a
// migrated pair schedules coherently.
func legacyPlan() Plan {
	return Plan{
		Goal:            "demo goal",
		SuccessCriteria: []string{"it works"},
		Milestones: []Milestone{{
			ID: "M1", Name: "", Phases: []Phase{{ // empty milestone name -> backfilled from id
				ID: "M1.P1", Name: "", Tasks: []Task{ // empty phase name -> backfilled from id
					{ID: "M1.P1.T1", Summary: "First task. It does the initial thing.", Deliverable: "d1", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a1"}},
					{ID: "M1.P1.T2", Summary: "second", Deliverable: "d2", Model: ModelHaiku45, Effort: EffortLow, TestStrategy: "lint", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a2"}},
				},
			}},
		}},
	}
}

func TestMigrateProjectBackfillsNamesAndStampsVersion(t *testing.T) {
	p := legacyPlan()
	ex := mustUnmarshalExec(t, legacyExecJSON)
	rep, err := MigrateProject(&p, &ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AlreadyV2 {
		t.Fatal("a v1 project must not report already_v2")
	}
	// SC15: every entity now carries a non-empty name.
	if p.Milestones[0].Name != "Milestone M1" {
		t.Errorf("milestone name = %q, want backfilled 'Milestone M1'", p.Milestones[0].Name)
	}
	if p.Milestones[0].Phases[0].Name != "Phase M1.P1" {
		t.Errorf("phase name = %q, want backfilled 'Phase M1.P1'", p.Milestones[0].Phases[0].Name)
	}
	t1 := p.Milestones[0].Phases[0].Tasks[0]
	if t1.Name != "First task" { // first sentence of the summary, trailing period trimmed
		t.Errorf("T1 name = %q, want 'First task' (first sentence of summary)", t1.Name)
	}
	if got := p.Milestones[0].Phases[0].Tasks[1].Name; got != "second" {
		t.Errorf("T2 name = %q, want 'second'", got)
	}
	// execution schema_version stamped current.
	if ex.SchemaVersion != CurrentExecSchemaVersion {
		t.Errorf("exec schema_version = %d, want %d", ex.SchemaVersion, CurrentExecSchemaVersion)
	}
	// the migrated plan is a valid v2 plan.
	if res := ValidatePlanBytes(mustBytes(t, p)); !res.OK {
		t.Fatalf("migrated plan must validate as v2: %v", res.Errors)
	}
}

// TestMigrateProjectLossless is the SC14 field-by-field check: no completed-work field is altered.
func TestMigrateProjectLossless(t *testing.T) {
	p := legacyPlan()
	ex := mustUnmarshalExec(t, legacyExecJSON)
	// snapshot the done task's completed-work fields before migrating.
	before := ex.Tasks[0]
	if _, err := MigrateProject(&p, &ex, false); err != nil {
		t.Fatal(err)
	}
	after := ex.Tasks[0]
	if after.Status != before.Status || after.Commit != before.Commit || after.CostUSD != before.CostUSD ||
		after.TokensOut != before.TokensOut || after.Test != before.Test || after.Review != before.Review {
		t.Fatalf("done-task fields mutated by migrate:\nbefore=%+v\nafter=%+v", before, after)
	}
	if after.Commit != "a1b2c3d" || after.CostUSD != 0.27 || after.Test != "PASS" || after.Review != "ACCEPT" {
		t.Fatalf("done-task verdicts/SHA/cost not preserved: %+v", after)
	}
	// run-config totals + log untouched.
	if ex.RunConfig.SpentUSD != 0.27 || ex.RunConfig.TokensOut != 1200 {
		t.Errorf("run_config totals mutated: %+v", ex.RunConfig)
	}
	if len(ex.Log) != 1 {
		t.Errorf("log mutated by migrate: %v", ex.Log)
	}
	// accounting never fabricated for a v1 run that never measured it.
	if ex.RunConfig.Accounting != nil {
		t.Errorf("migrate must not fabricate accounting: %+v", ex.RunConfig.Accounting)
	}
}

// TestMigrateProjectIdempotent: re-running on the migrated output is a no-op (already_v2, no changes).
func TestMigrateProjectIdempotent(t *testing.T) {
	p := legacyPlan()
	ex := mustUnmarshalExec(t, legacyExecJSON)
	if _, err := MigrateProject(&p, &ex, false); err != nil {
		t.Fatal(err)
	}
	// second pass over the now-v2 values.
	rep2, err := MigrateProject(&p, &ex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.AlreadyV2 || len(rep2.Changes) != 0 {
		t.Fatalf("re-migrate must be a no-op: already_v2=%v changes=%+v", rep2.AlreadyV2, rep2.Changes)
	}
	// a fresh v2 project (InitExec + named plan) is already v2 on the first pass.
	vp := validPlan()
	vex, _ := InitExec(vp, InitExecOptions{Slug: "demo", At: at0})
	rep3, err := MigrateProject(&vp, &vex, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep3.AlreadyV2 || len(rep3.Changes) != 0 {
		t.Fatalf("a fresh v2 project must report already_v2 with no changes: %+v", rep3)
	}
}

// TestMigrateProjectDryRunPreviewsExactChanges: dry-run yields the identical change list a real run
// would apply (the CLI is what withholds the write). Compared against a non-dry-run of an identical
// input.
func TestMigrateProjectDryRunPreviewsExactChanges(t *testing.T) {
	pDry, exDry := legacyPlan(), mustUnmarshalExec(t, legacyExecJSON)
	pWet, exWet := legacyPlan(), mustUnmarshalExec(t, legacyExecJSON)
	dry, err := MigrateProject(&pDry, &exDry, true)
	if err != nil {
		t.Fatal(err)
	}
	wet, err := MigrateProject(&pWet, &exWet, false)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || wet.DryRun {
		t.Fatalf("dry_run flag not reflected: dry=%v wet=%v", dry.DryRun, wet.DryRun)
	}
	if !reflect.DeepEqual(dry.Changes, wet.Changes) {
		t.Fatalf("dry-run preview differs from applied changes:\ndry=%+v\nwet=%+v", dry.Changes, wet.Changes)
	}
	if len(dry.Changes) == 0 {
		t.Fatal("dry-run over a v1 project must preview a non-empty change list")
	}
	// the change list names the concrete v2 deltas.
	var sawVersion, sawTaskName bool
	for _, c := range dry.Changes {
		if c.Target == "execution" && c.Field == "schema_version" {
			sawVersion = true
		}
		if c.Target == "plan" && c.Field == "name" && c.ID == "M1.P1.T1" {
			sawTaskName = true
		}
	}
	if !sawVersion || !sawTaskName {
		t.Fatalf("change list missing expected deltas: sawVersion=%v sawTaskName=%v", sawVersion, sawTaskName)
	}
}

// TestMigrateProjectResumesDeterministically: a migrated pair yields the exact same next/batch
// scheduling decision as the pre-migrate state (migrate must never touch status/deps/order).
func TestMigrateProjectResumesDeterministically(t *testing.T) {
	exBefore := mustUnmarshalExec(t, legacyExecJSON)
	pAfter := legacyPlan()
	exAfter := mustUnmarshalExec(t, legacyExecJSON)
	if _, err := MigrateProject(&pAfter, &exAfter, false); err != nil {
		t.Fatal(err)
	}
	// Schedule both exec states against the SAME (migrated) plan so the comparison isolates the
	// exec migration's effect on scheduling — the plan's backfilled names are display-only and must
	// not change WHICH task is next.
	if !reflect.DeepEqual(NextTask(exBefore, pAfter), NextTask(exAfter, pAfter)) {
		t.Fatalf("next differs after exec migrate:\nbefore=%+v\nafter=%+v", NextTask(exBefore, pAfter), NextTask(exAfter, pAfter))
	}
	if !reflect.DeepEqual(BatchTasks(exBefore, pAfter, 4), BatchTasks(exAfter, pAfter, 4)) {
		t.Fatal("batch differs after exec migrate")
	}
	// the resumed next task surfaces its backfilled name (SC15 readout).
	nx := NextTask(exAfter, pAfter)
	if nx.Task == nil || nx.Task.Name == "" {
		t.Fatalf("resumed next task must surface a name: %+v", nx)
	}
}

func TestMigrateProjectRejectsFutureExecVersion(t *testing.T) {
	p := legacyPlan()
	ex := mustUnmarshalExec(t, legacyExecJSON)
	ex.SchemaVersion = CurrentExecSchemaVersion + 1
	if _, err := MigrateProject(&p, &ex, false); err == nil {
		t.Fatal("migrate must reject an execution.json newer than this build supports")
	}
}

func TestMigrateProjectSaveRoundTripThenIdempotent(t *testing.T) {
	p := legacyPlan()
	ex := mustUnmarshalExec(t, legacyExecJSON)
	if _, err := MigrateProject(&p, &ex, false); err != nil {
		t.Fatal(err)
	}
	// serialize both docs (the CLI's write), reload, re-migrate: must be a clean no-op.
	pRaw, exRaw := mustBytes(t, p), mustBytes(t, ex)
	var p2 Plan
	if err := json.Unmarshal(pRaw, &p2); err != nil {
		t.Fatal(err)
	}
	ex2 := mustUnmarshalExec(t, string(exRaw))
	rep, err := MigrateProject(&p2, &ex2, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.AlreadyV2 {
		t.Fatalf("save->reload->migrate must be a no-op, got changes: %+v", rep.Changes)
	}
}

// orchOnly returns whether task id is stamped orchestrator_only after migration.
func orchOnly(p Plan, id string) bool {
	for _, r := range WalkTasks(p) {
		if r.Task.ID == id {
			return r.Task.OrchestratorOnly
		}
	}
	return false
}

// TestMigrateOrchestratorOnly_OnlyDoNotDispatchNotes: a forces_pause risk carrying the "do not
// dispatch <id>" convention stamps that task; a forces_pause risk that merely names a different
// task by id (an ordinary regression/empirical-test hazard) must NOT stamp it. Guards the
// over-match that would turn normal implementation tasks permanently-undispatchable.
func TestMigrateOrchestratorOnly_OnlyDoNotDispatchNotes(t *testing.T) {
	p := Plan{
		Goal: "g", SuccessCriteria: []string{"x"},
		Milestones: []Milestone{{ID: "M1", Name: "m", Phases: []Phase{{ID: "M1.P1", Name: "p", Tasks: []Task{
			{ID: "M1.P1.T1", Name: "gate", Summary: "measure", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}},
			{ID: "M1.P1.T2", Name: "impl", Summary: "normal", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "u", Acceptance: []string{"a"}},
		}}}}},
		Risks: []Risk{
			{Risk: "O-measurement gate: do not dispatch M1.P1.T1 to a subagent.", ForcesPause: true},
			{Risk: "M1.P1.T2 may regress the sequential path during the rewrite.", Mitigation: "M1.P1.T2's review cross-checks it.", ForcesPause: true},
		},
	}
	rep := MigrateReport{Changes: []MigrateChange{}, Warnings: []string{}}
	migrateOrchestratorOnly(&p, &rep)
	if !orchOnly(p, "M1.P1.T1") {
		t.Fatal("a do-not-dispatch hand-note must stamp orchestrator_only")
	}
	if orchOnly(p, "M1.P1.T2") {
		t.Fatal("an ordinary forces_pause risk that only names a task must NOT stamp orchestrator_only")
	}
}

func TestNameFromSummary(t *testing.T) {
	cases := []struct{ in, fallback, want string }{
		{"First task. Then more.", "id", "First task"},
		{"single clause no period", "id", "single clause no period"},
		{"trailing period only.", "id", "trailing period only"},
		{"   ", "M1.P1.T1", "M1.P1.T1"}, // empty summary -> id fallback
		{strings.Repeat("word ", 40), "id", ""},
	}
	for _, c := range cases {
		got := nameFromSummary(c.in, c.fallback)
		if c.want != "" && got != c.want {
			t.Errorf("nameFromSummary(%q) = %q, want %q", c.in, got, c.want)
		}
		if got == "" {
			t.Errorf("nameFromSummary(%q) returned empty (never allowed)", c.in)
		}
		if len([]rune(got)) > nameMaxLen+1 { // +1 for the ellipsis rune
			t.Errorf("nameFromSummary(%q) len %d exceeds cap", c.in, len([]rune(got)))
		}
	}
}
