package bh

import (
	"encoding/json"
	"strings"
	"testing"
)

const at0 = "2026-06-25T11:00:00Z"

func TestParseBudget(t *testing.T) {
	if l, c, _ := ParseBudget("unlimited"); l != "unlimited" || c != nil {
		t.Fatalf("unlimited => %s %v", l, c)
	}
	if l, c, _ := ParseBudget("$5"); l != "$5.00" || c == nil || *c != 5 {
		t.Fatalf("$5 => %s %v", l, c)
	}
	if _, _, err := ParseBudget("abc"); err == nil {
		t.Fatal("expected error for non-numeric budget")
	}
}

func TestInitExec(t *testing.T) {
	ex, err := InitExec(validPlan(), InitExecOptions{Slug: "demo", Budget: "$5", At: at0, DesignUpdated: "2026-06-25T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Schema != ExecSchema || len(ex.Tasks) != 2 || ex.RunConfig.SpentUSD != 0 {
		t.Fatalf("bad init: %+v", ex.RunConfig)
	}
	if ex.RunConfig.BudgetCeilingUSD == nil || *ex.RunConfig.BudgetCeilingUSD != 5 {
		t.Fatal("ceiling not set")
	}
	for _, tk := range ex.Tasks {
		if tk.Status != StatusNotStarted {
			t.Fatalf("task %s not fresh", tk.ID)
		}
	}
	if _, err := InitExec(validPlan(), InitExecOptions{At: at0}); err == nil {
		t.Fatal("expected error without slug")
	}
}

func TestNextTaskRespectsDeps(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	// fresh: T1 first
	if r := NextTask(ex, p); r.Task == nil || r.Task.ID != "M1.P1.T1" {
		t.Fatalf("fresh next = %+v", r)
	}
	// T1 done -> T2 eligible
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("aaa1111")}, at0); err != nil {
		t.Fatal(err)
	}
	if r := NextTask(ex, p); r.Task == nil || r.Task.ID != "M1.P1.T2" {
		t.Fatalf("after T1 next = %+v", r)
	}
	// both done -> done
	_ = RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Commit: ptrS("bbb2222")}, at0)
	if r := NextTask(ex, p); !r.Done {
		t.Fatalf("expected done, got %+v", r)
	}
}

func TestNextTaskBlockedWhenDepUnmet(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	// mark T2 in-progress impossible normally, but simulate T1 failed and T2 not started:
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusFailed)}, at0)
	// T1 failed (non-terminal), so T1 is still the next eligible (no deps); not blocked.
	if r := NextTask(ex, p); r.Task == nil || r.Task.ID != "M1.P1.T1" {
		t.Fatalf("failed task should still be next-eligible: %+v", r)
	}
}

func TestRecordAccruesCost(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("a1b2c3d"), Cost: ptrF(0.27)}, at0)
	_ = RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Commit: ptrS("bbb2222"), Cost: ptrF(0.22)}, at0)
	if ex.RunConfig.SpentUSD != 0.49 {
		t.Fatalf("spent = %v, want 0.49", ex.RunConfig.SpentUSD)
	}
	if ex.Tasks[0].Commit != "a1b2c3d" || ex.Tasks[0].Test != "PASS" {
		t.Fatalf("T1 row not recorded: %+v", ex.Tasks[0])
	}
	if len(ex.Log) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(ex.Log))
	}
	if err := RecordTask(&ex, "NOPE", RecordFields{}, at0); err == nil {
		t.Fatal("expected error recording unknown task")
	}
}

// TestRecordTaskRefusesDoneWithNoCommit is the writer-side fail-fast: status=done may never
// persist against a task carrying no commit — neither supplied on this call nor already on the
// row — and the error names both the task id and the missing field so it can't be mistaken for a
// generic validation failure.
func TestRecordTaskRefusesDoneWithNoCommit(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone)}, at0)
	if err == nil {
		t.Fatal("expected refusal for status=done with no commit supplied and none recorded")
	}
	if !strings.Contains(err.Error(), "M1.P1.T1") || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("error must name the task id and the missing field, got: %v", err)
	}
	if ex.Tasks[0].Status != StatusNotStarted || ex.Tasks[0].Commit != "" || len(ex.Log) != 0 {
		t.Fatalf("refused write must persist nothing, got: %+v log=%v", ex.Tasks[0], ex.Log)
	}
}

// TestRecordTaskAcceptsDoneWithCommitSuppliedOrAlreadyRecorded covers both legal done paths: a
// commit supplied on this call, and a commit already on the row from a prior call (this call
// supplies none) — the boundary case the fail-fast must not over-refuse.
func TestRecordTaskAcceptsDoneWithCommitSuppliedOrAlreadyRecorded(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("aaa1111")}, at0); err != nil {
		t.Fatalf("done with a supplied commit should be accepted: %v", err)
	}
	// Already-done task with a recorded commit re-records done, supplying no commit this time.
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Note: ptrS("re-recorded")}, at0); err != nil {
		t.Fatalf("done on a task already carrying a commit should be accepted: %v", err)
	}
	if ex.Tasks[0].Commit != "aaa1111" {
		t.Fatalf("prior commit should be left intact, got %q", ex.Tasks[0].Commit)
	}
}

// TestRecordTaskDoneRefusalDoesNotAffectNonDoneStatuses proves the fail-fast is scoped to
// status=done: every other status must persist with no commit at all.
func TestRecordTaskDoneRefusalDoesNotAffectNonDoneStatuses(t *testing.T) {
	p := validPlan()
	for _, s := range []Status{StatusInProgress, StatusBlocked, StatusFailed, StatusSuperseded} {
		ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
		if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(s)}, at0); err != nil {
			t.Fatalf("status=%s with no commit should be accepted, got: %v", s, err)
		}
		if ex.Tasks[0].Status != s {
			t.Fatalf("status=%s did not persist: %+v", s, ex.Tasks[0])
		}
	}
}

func TestReconcilePreservesDoneWork(t *testing.T) {
	oldP := validPlan()
	ex, _ := InitExec(oldP, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("aaa"), Cost: ptrF(0.30)}, at0)
	_ = RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Commit: ptrS("bbb"), Cost: ptrF(0.20)}, at0)

	newP := validPlan()
	newP.Milestones[0].Phases[0].Tasks[0].Deliverable = "CHANGED"                                                                                                                                                  // T1 changed
	newP.Milestones[0].Phases[0].Tasks = append(newP.Milestones[0].Phases[0].Tasks[:1], Task{ID: "M1.P1.T3", Summary: "new", Deliverable: "d", Acceptance: []string{"a"}, Model: ModelHaiku45, Effort: EffortLow}) // drop T2, add T3

	ReconcileExec(&ex, oldP, newP, "2026-06-26T00:00:00Z", "2026-06-26T00:05:00Z", "2026-06-26T00:10:00Z")

	byID := map[string]ExecTask{}
	for _, x := range ex.Tasks {
		byID[x.ID] = x
	}
	if byID["M1.P1.T1"].Status != StatusNotStarted || !strings.Contains(byID["M1.P1.T1"].Notes, "changed by design") {
		t.Fatalf("changed T1 should reset: %+v", byID["M1.P1.T1"])
	}
	if byID["M1.P1.T2"].Status != StatusSuperseded {
		t.Fatalf("removed T2 should be superseded: %+v", byID["M1.P1.T2"])
	}
	if byID["M1.P1.T3"].Status != StatusNotStarted {
		t.Fatalf("added T3 should be fresh: %+v", byID["M1.P1.T3"])
	}
	// T1 (was done, cost 0.30) reset to 0; T2 superseded keeps its 0.20; spent recomputed.
	if ex.RunConfig.SpentUSD != 0.20 {
		t.Fatalf("spent after reconcile = %v, want 0.20", ex.RunConfig.SpentUSD)
	}
	if ex.Provenance.DesignUpdated != "2026-06-26T00:00:00Z" {
		t.Fatal("provenance not updated")
	}
}

// ---- archive-awareness (next/reconcile-exec) ----

// TestNextTaskArchivedDepResolvesAsDone — a live task whose sole dep was archived (necessarily
// terminal — Archive's own precondition) must still be picked, never stall. Fails on pre-fix
// code: an id absent from ex.Tasks defaults to not-started, permanently unmet.
func TestNextTaskArchivedDepResolvesAsDone(t *testing.T) {
	p := Plan{Goal: "g", SuccessCriteria: []string{"s"}, Milestones: []Milestone{
		{ID: "M2", Name: "two", Phases: []Phase{{ID: "M2.P1", Name: "p1", Tasks: []Task{
			{ID: "M2.P1.T1", Summary: "dependent", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a"}},
		}}}},
	}}
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	ex.Archived = []Tombstone{{ID: "M1.P1.T1", Summary: "archived root", Status: StatusDone, Commit: "aaa1111", CostUSD: 0.1}}
	if r := NextTask(ex, p); r.Task == nil || r.Task.ID != "M2.P1.T1" {
		t.Fatalf("next should schedule M2.P1.T1 past its archived dep, got %+v", r)
	}
}

// TestReconcileExecArchivedTaskNotReAdded — a design regenerate that still derives an
// already-archived task (design.md unchanged there) must not resurrect it as a fresh not-started
// live row; it stays out of ex.Tasks, and the tombstone index is left untouched.
func TestReconcileExecArchivedTaskNotReAdded(t *testing.T) {
	oldP := Plan{Goal: "g", SuccessCriteria: []string{"s"}, Milestones: []Milestone{
		{ID: "M2", Name: "two", Phases: []Phase{{ID: "M2.P1", Name: "p1", Tasks: []Task{
			{ID: "M2.P1.T1", Summary: "live", Deliverable: "d", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a"}},
		}}}},
	}}
	newP := Plan{Goal: "g", SuccessCriteria: []string{"s"}, Milestones: []Milestone{
		{ID: "M1", Name: "one", Phases: []Phase{{ID: "M1.P1", Name: "p1", Tasks: []Task{
			{ID: "M1.P1.T1", Summary: "archived root", Deliverable: "d0", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a0"}},
		}}}},
		oldP.Milestones[0],
	}}
	ex, _ := InitExec(oldP, InitExecOptions{Slug: "demo", At: at0})
	ex.Archived = []Tombstone{{ID: "M1.P1.T1", Summary: "archived root", Status: StatusDone, Commit: "aaa1111", CostUSD: 0.1}}

	ReconcileExec(&ex, oldP, newP, "2026-07-04T00:00:00Z", "2026-07-04T00:05:00Z", "2026-07-04T00:10:00Z")

	for _, row := range ex.Tasks {
		if row.ID == "M1.P1.T1" {
			t.Fatalf("archived task re-admitted as a live row: %+v", row)
		}
	}
	if len(ex.Tasks) != 1 || ex.Tasks[0].ID != "M2.P1.T1" {
		t.Fatalf("live doc should carry only the un-archived task, got %+v", ex.Tasks)
	}
	if len(ex.Archived) != 1 || ex.Archived[0].ID != "M1.P1.T1" {
		t.Fatalf("tombstone index must be left untouched, got %+v", ex.Archived)
	}
}

func TestRenderExecutionRoundTrip(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("a1b2c3d"), Cost: ptrF(0.27)}, at0)
	md := RenderExecution(ex, p)
	for _, want := range []string{"id: project:demo:execution", "description: \"Live execution-state mirror", "Resume here →** M1.P1.T2", "✅", "a1b2c3d", "Do not hand-edit"} {
		if !strings.Contains(md, want) {
			t.Fatalf("rendered execution.md missing %q:\n%s", want, md)
		}
	}
}

func TestLogNote(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	LogNote(&ex, "plan-acceptance gate: ACCEPT (all success_criteria met)", "2026-06-25T12:00:00Z")
	if len(ex.Log) != 1 || !strings.Contains(ex.Log[0], "NOTE plan-acceptance gate: ACCEPT") {
		t.Fatalf("expected a NOTE log line, got %v", ex.Log)
	}
	if ex.Updated != "2026-06-25T12:00:00Z" {
		t.Fatalf("updated not bumped: %s", ex.Updated)
	}
}

func ptr(s Status) *Status    { return &s }
func ptrF(f float64) *float64 { return &f }
func ptrS(s string) *string   { return &s }

// ---- pause events ----

func TestRecordPauseEvent(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordPauseEvent(&ex, PauseGit, "M1.P1.T1", "2026-06-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if len(ex.PauseEvents) != 1 {
		t.Fatalf("expected 1 pause event, got %d", len(ex.PauseEvents))
	}
	got := ex.PauseEvents[0]
	if got.ReasonEnum != PauseGit || got.At != "2026-06-25T12:00:00Z" || got.TaskID != "M1.P1.T1" {
		t.Fatalf("bad pause event: %+v", got)
	}
	if ex.Updated != "2026-06-25T12:00:00Z" {
		t.Fatalf("updated not bumped: %s", ex.Updated)
	}
	if len(ex.Log) != 1 || !strings.Contains(ex.Log[0], "PAUSE git M1.P1.T1") {
		t.Fatalf("expected a PAUSE log line, got %v", ex.Log)
	}
}

func TestRecordPauseEventNoTaskID(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordPauseEvent(&ex, PauseApproval, "", "2026-06-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if ex.PauseEvents[0].TaskID != "" {
		t.Fatalf("expected empty task_id for a plan-level pause, got %q", ex.PauseEvents[0].TaskID)
	}
	if strings.Contains(ex.Log[0], "  ") {
		t.Fatalf("no-task-id log line should not have a trailing/double space: %q", ex.Log[0])
	}
}

func TestRecordPauseEventRejectsUnknownReason(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordPauseEvent(&ex, PauseReason("bogus"), "", at0); err == nil {
		t.Fatal("expected error for unrecognized reason_enum")
	}
	if len(ex.PauseEvents) != 0 {
		t.Fatalf("rejected event must not persist, got %+v", ex.PauseEvents)
	}
}

func TestPauseReasonKnownAndMechanical(t *testing.T) {
	mechanical := []PauseReason{PauseGit, PauseState, PauseMerge}
	nonMechanical := []PauseReason{PauseDesignLevel, PauseApproval, PauseBudget, PauseSigning}
	for _, r := range mechanical {
		if !r.Known() || !r.Mechanical() {
			t.Fatalf("%q expected known+mechanical", r)
		}
	}
	for _, r := range nonMechanical {
		if !r.Known() || r.Mechanical() {
			t.Fatalf("%q expected known, non-mechanical", r)
		}
	}
	if PauseReason("bogus").Known() {
		t.Fatal("unrecognized reason must not be Known")
	}
}

func TestMechanicalSlipCount(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	for _, r := range []PauseReason{PauseGit, PauseState, PauseMerge, PauseDesignLevel, PauseApproval, PauseBudget, PauseSigning} {
		if err := RecordPauseEvent(&ex, r, "", at0); err != nil {
			t.Fatal(err)
		}
	}
	if got := MechanicalSlipCount(ex); got != 3 {
		t.Fatalf("expected mechanical-slip count 3 (git+state+merge), got %d", got)
	}
}

// TestPauseEventsRoundTripJSON confirms a persisted pause-event survives marshal/unmarshal
// through execution.json byte-for-byte on the fields that matter (reason_enum, at, task_id), and
// that a pre-existing doc with no pause_events key still unmarshals cleanly (additive/omitempty).
func TestPauseEventsRoundTripJSON(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordPauseEvent(&ex, PauseMerge, "M1.P1.T1", "2026-06-25T12:00:00Z")
	_ = RecordPauseEvent(&ex, PauseBudget, "", "2026-06-25T13:00:00Z")

	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	var round ExecState
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.PauseEvents) != 2 {
		t.Fatalf("expected 2 pause events after round-trip, got %d", len(round.PauseEvents))
	}
	if round.PauseEvents[0].ReasonEnum != PauseMerge || round.PauseEvents[0].TaskID != "M1.P1.T1" {
		t.Fatalf("bad round-tripped event[0]: %+v", round.PauseEvents[0])
	}
	if round.PauseEvents[1].ReasonEnum != PauseBudget || round.PauseEvents[1].TaskID != "" {
		t.Fatalf("bad round-tripped event[1]: %+v", round.PauseEvents[1])
	}
	if MechanicalSlipCount(round) != 1 {
		t.Fatalf("expected mechanical-slip count 1 (merge) after round-trip, got %d", MechanicalSlipCount(round))
	}

	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "pause_events")
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var legacyEx ExecState
	if err := json.Unmarshal(legacyRaw, &legacyEx); err != nil {
		t.Fatal(err)
	}
	if len(legacyEx.PauseEvents) != 0 || MechanicalSlipCount(legacyEx) != 0 {
		t.Fatalf("legacy doc without pause_events must resolve to zero, got %+v", legacyEx.PauseEvents)
	}
}

// ---- escalation events ----

func TestRecordEscalationEvent(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordEscalationEvent(&ex, TriggerFailedTaskTriage, "xhigh", RouteMagistrate, "M1.P1.T1", "2026-06-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if len(ex.EscalationEvents) != 1 {
		t.Fatalf("expected 1 escalation event, got %d", len(ex.EscalationEvents))
	}
	got := ex.EscalationEvents[0]
	if got.Trigger != TriggerFailedTaskTriage || got.Tier != "xhigh" || got.Route != RouteMagistrate || got.At != "2026-06-25T12:00:00Z" || got.TaskID != "M1.P1.T1" {
		t.Fatalf("bad escalation event: %+v", got)
	}
	if ex.Updated != "2026-06-25T12:00:00Z" {
		t.Fatalf("updated not bumped: %s", ex.Updated)
	}
	if len(ex.Log) != 1 || !strings.Contains(ex.Log[0], "ESCALATE failed-task-triage -> magistrate (tier xhigh) M1.P1.T1") {
		t.Fatalf("expected an ESCALATE log line, got %v", ex.Log)
	}
}

func TestRecordEscalationEventNoTaskID(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordEscalationEvent(&ex, TriggerLocalDeltaReplan, "high", RouteMagistrate, "", "2026-06-25T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if ex.EscalationEvents[0].TaskID != "" {
		t.Fatalf("expected empty task_id for a plan-level firing, got %q", ex.EscalationEvents[0].TaskID)
	}
	if strings.Contains(ex.Log[0], "  ") {
		t.Fatalf("no-task-id log line should not have a trailing/double space: %q", ex.Log[0])
	}
}

// TestRecordEscalationEventRejectsOutOfSetTrigger — the closed-trigger-set guard: only a trigger
// in classify.go's closed named set may persist (acceptance 2, no catch-all reaching persistence).
func TestRecordEscalationEventRejectsOutOfSetTrigger(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	for _, bogus := range []EscalationTrigger{"", "bogus", "SURPRISE-OVERLAP", "surprise_overlap"} {
		if err := RecordEscalationEvent(&ex, bogus, "xhigh", RouteMagistrate, "", at0); err == nil {
			t.Fatalf("expected error for out-of-set trigger %q", bogus)
		}
	}
	if len(ex.EscalationEvents) != 0 {
		t.Fatalf("rejected event must not persist, got %+v", ex.EscalationEvents)
	}
}

// TestMagistrateFiringCount — the derivation the equal-magistrate-firing void check reads: a
// plain count of persisted escalation-events, regardless of which named trigger fired each one.
func TestMagistrateFiringCount(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if got := MagistrateFiringCount(ex); got != 0 {
		t.Fatalf("expected 0 firings on a fresh run, got %d", got)
	}
	for _, trig := range EscalationTriggers() {
		if err := RecordEscalationEvent(&ex, trig, escalationTiers[trig], RouteMagistrate, "", at0); err != nil {
			t.Fatal(err)
		}
	}
	if got := MagistrateFiringCount(ex); got != len(EscalationTriggers()) {
		t.Fatalf("expected %d firings (one per named trigger), got %d", len(EscalationTriggers()), got)
	}
}

// TestClassifyEscalationPersistsRecordedEvent — the classify -> record seam: ClassifyEscalation's
// magistrate route, for each named trigger, persists as a well-formed escalation-event carrying the
// SAME trigger and tier the classifier selected (acceptance 1 + 2: asserts on recorded events, not
// just the pure classify result).
func TestClassifyEscalationPersistsRecordedEvent(t *testing.T) {
	for trigger, tier := range escalationTiers {
		t.Run(string(trigger), func(t *testing.T) {
			p := validPlan()
			ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
			r := ClassifyEscalation(EscalationInput{Condition: string(trigger)})
			if r.Route != RouteMagistrate {
				t.Fatalf("route = %q, want %q", r.Route, RouteMagistrate)
			}
			if err := RecordEscalationEvent(&ex, r.Trigger, r.Tier, r.Route, "", at0); err != nil {
				t.Fatal(err)
			}
			if len(ex.EscalationEvents) != 1 {
				t.Fatalf("expected 1 recorded event, got %d", len(ex.EscalationEvents))
			}
			got := ex.EscalationEvents[0]
			if got.Trigger != trigger || got.Tier != tier || got.Route != RouteMagistrate {
				t.Fatalf("recorded event %+v does not match classified trigger %q / tier %q", got, trigger, tier)
			}
		})
	}
}

// TestEscalationEventsRoundTripJSON confirms a persisted escalation-event survives marshal/
// unmarshal through execution.json byte-for-byte on the fields that matter (trigger, tier, route,
// at, task_id), and that a pre-existing doc with no escalation_events key still unmarshals cleanly
// (additive/omitempty).
func TestEscalationEventsRoundTripJSON(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordEscalationEvent(&ex, TriggerSurpriseOverlap, "xhigh", RouteMagistrate, "M1.P1.T1", "2026-06-25T12:00:00Z")
	_ = RecordEscalationEvent(&ex, TriggerPhaseGateRegression, "high", RouteMagistrate, "", "2026-06-25T13:00:00Z")

	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	var round ExecState
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.EscalationEvents) != 2 {
		t.Fatalf("expected 2 escalation events after round-trip, got %d", len(round.EscalationEvents))
	}
	if round.EscalationEvents[0].Trigger != TriggerSurpriseOverlap || round.EscalationEvents[0].TaskID != "M1.P1.T1" {
		t.Fatalf("bad round-tripped event[0]: %+v", round.EscalationEvents[0])
	}
	if round.EscalationEvents[1].Trigger != TriggerPhaseGateRegression || round.EscalationEvents[1].TaskID != "" {
		t.Fatalf("bad round-tripped event[1]: %+v", round.EscalationEvents[1])
	}
	if MagistrateFiringCount(round) != 2 {
		t.Fatalf("expected firing count 2 after round-trip, got %d", MagistrateFiringCount(round))
	}

	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "escalation_events")
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var legacyEx ExecState
	if err := json.Unmarshal(legacyRaw, &legacyEx); err != nil {
		t.Fatal(err)
	}
	if len(legacyEx.EscalationEvents) != 0 || MagistrateFiringCount(legacyEx) != 0 {
		t.Fatalf("legacy doc without escalation_events must resolve to zero, got %+v", legacyEx.EscalationEvents)
	}
}
