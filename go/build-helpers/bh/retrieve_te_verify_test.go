package bh

import "testing"

// Test-engineer re-verification pass (M4.P2.T1, post-fix). Additive gaps not covered by
// retrieve_test.go / retrieve_adversarial_test.go: task-level projection mutation-safety
// (RetrievePlan's own return path, not just field/group views), and missing-ID errors for
// milestone/phase levels (empty string, not just garbage/wrong-kind).

// RetrievePlan's LevelTask branch returns cloneTask(t); confirm mutating any of its three
// slice fields (Deps, Acceptance, FileSurface) does not reach the canonical Plan.
func TestRetrievePlanTaskCloneIsIndependent(t *testing.T) {
	p := validPlan()
	res, err := RetrievePlan(p, RetrieveInput{Level: LevelTask, ID: "M1.P1.T2"})
	if err != nil {
		t.Fatal(err)
	}
	tk := res.(Task)
	if len(tk.Deps) == 0 || len(tk.Acceptance) == 0 {
		t.Fatal("fixture invariant broken: M1.P1.T2 needs non-empty Deps and Acceptance")
	}
	tk.Deps[0] = "CORRUPTED-DEP"
	tk.Acceptance[0] = "CORRUPTED-ACC"

	fresh, err := RetrievePlan(p, RetrieveInput{Level: LevelTask, ID: "M1.P1.T2"})
	if err != nil {
		t.Fatal(err)
	}
	ft := fresh.(Task)
	if ft.Deps[0] == "CORRUPTED-DEP" {
		t.Fatalf("MUTATION LEAK: task-level Deps still aliases the canonical Plan")
	}
	if ft.Acceptance[0] == "CORRUPTED-ACC" {
		t.Fatalf("MUTATION LEAK: task-level Acceptance still aliases the canonical Plan")
	}
}

// Empty --id (flag simply omitted) must error identically to a missing --id at every
// ID-bearing plan.json level — distinct from the "wrong kind of id" / "garbage id" cases
// retrieve_adversarial_test.go already covers.
func TestRetrievePlanEmptyIDRejectedAtEveryIDLevel(t *testing.T) {
	p := validPlan()
	for _, lvl := range []RetrievalLevel{LevelMilestone, LevelPhase, LevelTask} {
		if _, err := RetrievePlan(p, RetrieveInput{Level: lvl}); err == nil {
			t.Fatalf("level %q accepted empty --id without error", lvl)
		}
	}
	if _, err := RetrievePlan(p, RetrieveInput{Level: LevelField, Field: "name"}); err == nil {
		t.Fatal("level field accepted empty --id without error")
	}
}

// Same for execution.json's ID-bearing levels.
func TestRetrieveExecEmptyIDRejectedAtEveryIDLevel(t *testing.T) {
	ex := execFixture(t)
	for _, lvl := range []RetrievalLevel{LevelMilestone, LevelPhase, LevelTask} {
		if _, err := RetrieveExec(ex, RetrieveInput{Level: lvl}); err == nil {
			t.Fatalf("level %q accepted empty --id without error", lvl)
		}
	}
	if _, err := RetrieveExec(ex, RetrieveInput{Level: LevelField, Field: "status"}); err == nil {
		t.Fatal("level field accepted empty --id without error")
	}
}

// A phase id under --level task must fail with a clean not-found error, not silently return
// a zero-value Task — confirms findTaskPlan only ever matches exact task-shaped ids.
func TestRetrievePlanTaskRejectsPhaseID(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelTask, ID: "M1.P1"}); err == nil {
		t.Fatal("expected not-found error: M1.P1 is a phase id, not a task id")
	}
}

// RetrieveExec's task-level branch must likewise reject a milestone/phase id rather than
// scanning past it into an unrelated task.
func TestRetrieveExecTaskRejectsMilestoneID(t *testing.T) {
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelTask, ID: "M1"}); err == nil {
		t.Fatal("expected not-found error: M1 is a milestone id, not a task id")
	}
}
