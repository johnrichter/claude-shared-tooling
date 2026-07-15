package bh

import (
	"encoding/json"
	"fmt"
	"testing"
)

// twoPhasePlan extends validPlan() with a second phase (and a second milestone) so
// milestone-level flattening-across-phases has something to prove.
func twoPhasePlan() Plan {
	p := validPlan()
	p.Milestones[0].Phases = append(p.Milestones[0].Phases, Phase{
		ID: "M1.P2", Name: "Phase two", Tasks: []Task{
			{ID: "M1.P2.T1", Summary: "third", Deliverable: "d3", Model: ModelHaiku45, Effort: EffortLow, TestStrategy: "lint", Acceptance: []string{"a3"}},
		},
	})
	p.Milestones = append(p.Milestones, Milestone{
		ID: "M2", Name: "Milestone two", Phases: []Phase{{
			ID: "M2.P1", Name: "Phase one", Tasks: []Task{
				{ID: "M2.P1.T1", Summary: "fourth", Deliverable: "d4", Model: ModelSonnet5, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a4"}},
			},
		}},
	})
	return p
}

func TestRetrievePlanOutline(t *testing.T) {
	out, err := RetrievePlan(twoPhasePlan(), RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	entries := out.([]OutlineEntry)
	// M1, M1.P1, M1.P1.T1, M1.P1.T2, M1.P2, M1.P2.T1, M2, M2.P1, M2.P1.T1
	wantIDs := []string{"M1", "M1.P1", "M1.P1.T1", "M1.P1.T2", "M1.P2", "M1.P2.T1", "M2", "M2.P1", "M2.P1.T1"}
	if len(entries) != len(wantIDs) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(wantIDs), entries)
	}
	for i, id := range wantIDs {
		if entries[i].ID != id {
			t.Fatalf("entry %d = %q, want %q", i, entries[i].ID, id)
		}
	}
	// deps surface on tasks only, status never (plan.json carries no status)
	for _, e := range entries {
		if e.Status != "" {
			t.Fatalf("plan outline must never carry status: %+v", e)
		}
	}
	var t2 OutlineEntry
	for _, e := range entries {
		if e.ID == "M1.P1.T2" {
			t2 = e
		}
	}
	if len(t2.Deps) != 1 || t2.Deps[0] != "M1.P1.T1" {
		t.Fatalf("T2 deps = %v, want [M1.P1.T1]", t2.Deps)
	}
}

func TestRetrievePlanOutlineRejectsID(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelOutline, ID: "M1"}); err == nil {
		t.Fatal("expected error: outline takes no --id")
	}
}

func TestRetrievePlanMilestoneFlattensAcrossPhases(t *testing.T) {
	res, err := RetrievePlan(twoPhasePlan(), RetrieveInput{Level: LevelMilestone, ID: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	gv := res.(GroupView)
	if gv.Name != "Milestone one" {
		t.Fatalf("name = %q", gv.Name)
	}
	if len(gv.Tasks) != 3 {
		t.Fatalf("expected 3 tasks flattened across M1's two phases, got %+v", gv.Tasks)
	}
}

func TestRetrievePlanMilestoneRejectsPhaseID(t *testing.T) {
	if _, err := RetrievePlan(twoPhasePlan(), RetrieveInput{Level: LevelMilestone, ID: "M1.P1"}); err == nil {
		t.Fatal("expected error: M1.P1 is a phase id, not a milestone id")
	}
}

func TestRetrievePlanPhase(t *testing.T) {
	res, err := RetrievePlan(twoPhasePlan(), RetrieveInput{Level: LevelPhase, ID: "M1.P2"})
	if err != nil {
		t.Fatal(err)
	}
	gv := res.(GroupView)
	if gv.Name != "Phase two" || len(gv.Tasks) != 1 || gv.Tasks[0].ID != "M1.P2.T1" {
		t.Fatalf("unexpected phase view: %+v", gv)
	}
}

func TestRetrievePlanPhaseNotFound(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelPhase, ID: "M9.P9"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestRetrievePlanTask(t *testing.T) {
	res, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelTask, ID: "M1.P1.T2"})
	if err != nil {
		t.Fatal(err)
	}
	tk := res.(Task)
	if tk.Deliverable != "d2" || tk.Model != ModelHaiku45 || len(tk.Deps) != 1 {
		t.Fatalf("unexpected task record: %+v", tk)
	}
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelTask, ID: "NOPE"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestRetrievePlanFieldTask(t *testing.T) {
	res, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "deliverable"})
	if err != nil {
		t.Fatal(err)
	}
	fv := res.(FieldValue)
	if fv.Value != "d1" {
		t.Fatalf("value = %v, want d1", fv.Value)
	}
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "bogus"}); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestRetrievePlanFieldMilestoneAndPhase(t *testing.T) {
	res, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1", Field: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(FieldValue).Value != "Milestone one" {
		t.Fatalf("got %+v", res)
	}
	res, err = RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1.P1", Field: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(FieldValue).Value != "Phase one" {
		t.Fatalf("got %+v", res)
	}
	// a milestone/phase field query must never surface its nested children (L4 size bound).
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1", Field: "phases"}); err == nil {
		t.Fatal("expected error: milestone field-level must not expose nested phases")
	}
}

func TestRetrievePlanUnknownLevel(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: "bogus"}); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

// ---- execution.json ----

func execFixture(t *testing.T) ExecState {
	t.Helper()
	ex, err := InitExec(twoPhasePlan(), InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("abc123")}, at0); err != nil {
		t.Fatal(err)
	}
	return ex
}

func TestRetrieveExecOutline(t *testing.T) {
	out, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	entries := out.([]OutlineEntry)
	if len(entries) != 4 { // execution.json is flat: no separate milestone/phase rows
		t.Fatalf("expected 4 flat task rows, got %+v", entries)
	}
	var t1 OutlineEntry
	for _, e := range entries {
		if e.ID == "M1.P1.T1" {
			t1 = e
		}
	}
	if t1.Status != StatusDone {
		t.Fatalf("T1 status = %q, want done", t1.Status)
	}
	if t1.Deps != nil {
		t.Fatalf("execution.json outline must never carry deps: %+v", t1)
	}
}

func TestRetrieveExecGroupSynthesizedFromIDPrefix(t *testing.T) {
	res, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelMilestone, ID: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	gv := res.(GroupView)
	if gv.Name != "" {
		t.Fatalf("execution.json group must carry no name (not stored there): %+v", gv)
	}
	if len(gv.Tasks) != 3 {
		t.Fatalf("expected M1's 3 tasks (both phases), got %+v", gv.Tasks)
	}
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelPhase, ID: "M9.P9"}); err == nil {
		t.Fatal("expected not-found error for a phase with no tasks")
	}
}

func TestRetrieveExecTask(t *testing.T) {
	res, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
	if err != nil {
		t.Fatal(err)
	}
	et := res.(ExecTask)
	if et.Status != StatusDone || et.Commit != "abc123" {
		t.Fatalf("unexpected exec task record: %+v", et)
	}
}

func TestRetrieveExecField(t *testing.T) {
	res, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(FieldValue).Value != StatusDone {
		t.Fatalf("got %+v", res)
	}
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelField, ID: "M1", Field: "name"}); err == nil {
		t.Fatal("expected error: execution.json has no milestone-scoped fields")
	}
}

func TestRetrieveExecUnknownLevel(t *testing.T) {
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{Level: "bogus"}); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

// ---- archive.json ----

// archiveFixture archives the "done-heavy" archivePlan's M1 (archive_test.go) into a fresh
// ArchiveDoc, giving RetrieveArchive a real two-task archived group to project.
func archiveFixture(t *testing.T) ArchiveDoc {
	t.Helper()
	p := archivePlan()
	ex := doneM1Exec(t)
	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	return out.Archive
}

func TestRetrieveArchiveOutline(t *testing.T) {
	out, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	entries := out.([]OutlineEntry)
	wantIDs := []string{"M1", "M1.P1", "M1.P1.T1", "M1.P1.T2"}
	if len(entries) != len(wantIDs) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(wantIDs), entries)
	}
	for i, id := range wantIDs {
		if entries[i].ID != id {
			t.Fatalf("entry %d = %q, want %q", i, entries[i].ID, id)
		}
	}
	var t1 OutlineEntry
	for _, e := range entries {
		if e.ID == "M1.P1.T1" {
			t1 = e
		}
	}
	if t1.Status != StatusDone {
		t.Fatalf("archived task outline status = %q, want done", t1.Status)
	}
}

func TestRetrieveArchiveOutlineRejectsID(t *testing.T) {
	if _, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelOutline, ID: "M1"}); err == nil {
		t.Fatal("expected error: outline takes no --id")
	}
}

func TestRetrieveArchiveMilestoneAndPhase(t *testing.T) {
	res, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelMilestone, ID: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	gv := res.(GroupView)
	if gv.Name != "one" || len(gv.Tasks) != 2 {
		t.Fatalf("unexpected milestone view: %+v", gv)
	}
	res, err = RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelPhase, ID: "M1.P1"})
	if err != nil {
		t.Fatal(err)
	}
	gv = res.(GroupView)
	if gv.Name != "p1" || len(gv.Tasks) != 2 {
		t.Fatalf("unexpected phase view: %+v", gv)
	}
	if _, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelMilestone, ID: "M9"}); err == nil {
		t.Fatal("expected not-found error for an unarchived milestone id")
	}
}

func TestRetrieveArchiveTask(t *testing.T) {
	res, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelTask, ID: "M1.P1.T1"})
	if err != nil {
		t.Fatal(err)
	}
	at := res.(ArchivedTask)
	// full merged plan-slice + exec-slice record (T0 spike's L3 shape).
	if at.Deliverable != "d1" || at.TestStrategy != "unit" || at.Status != StatusDone || at.Commit != "aaa1111" || at.CostUSD != 0.30 {
		t.Fatalf("unexpected archived task record: %+v", at)
	}
	if _, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelTask, ID: "NOPE"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestRetrieveArchiveField(t *testing.T) {
	res, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(FieldValue).Value != "aaa1111" {
		t.Fatalf("value = %v, want aaa1111", res.(FieldValue).Value)
	}
	res, err = RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelField, ID: "M1", Field: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(FieldValue).Value != "one" {
		t.Fatalf("got %+v", res)
	}
	if _, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "bogus"}); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestRetrieveArchiveUnknownLevel(t *testing.T) {
	if _, err := RetrieveArchive(archiveFixture(t), RetrieveInput{Level: "bogus"}); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

// TestFieldRetrievalBoundedOnLargePlan proves the test-strategy claim: a field-level response
// stays small (tens of bytes) independent of total plan size, unlike a whole-doc load whose
// size grows with the plan. Confirms Target A's premise (T0 spike) at the unit level; a
// live/orchestrator-scale measurement is a build-time (SC10) concern, not this test's job.
func TestFieldRetrievalBoundedOnLargePlan(t *testing.T) {
	p := Plan{Goal: "large plan", SuccessCriteria: []string{"scale"}}
	for m := 1; m <= 20; m++ {
		mile := Milestone{ID: fmt.Sprintf("M%d", m), Name: fmt.Sprintf("Milestone %d", m)}
		for ph := 1; ph <= 3; ph++ {
			phase := Phase{ID: fmt.Sprintf("M%d.P%d", m, ph), Name: fmt.Sprintf("Phase %d", ph)}
			for tk := 1; tk <= 5; tk++ {
				phase.Tasks = append(phase.Tasks, Task{
					ID: fmt.Sprintf("M%d.P%d.T%d", m, ph, tk), Summary: "a long repeated summary line ", Deliverable: "a long repeated deliverable line ",
					Model: ModelSonnet5, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a", "b", "c"},
				})
			}
			mile.Phases = append(mile.Phases, phase)
		}
		p.Milestones = append(p.Milestones, mile)
	}
	whole, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RetrievePlan(p, RetrieveInput{Level: LevelField, ID: "M10.P2.T3", Field: "deliverable"})
	if err != nil {
		t.Fatal(err)
	}
	field, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(field) > 200 {
		t.Fatalf("field-level response = %d bytes, want < 200 regardless of the %d-byte whole plan", len(field), len(whole))
	}
	if len(whole) < 10*len(field) {
		t.Fatalf("fixture too small to demonstrate the bound: whole=%d field=%d", len(whole), len(field))
	}
}
