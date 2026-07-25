package bh

import (
	"strings"
	"testing"
)

// TestRecordTaskCannotClearCommitOnAlreadyDoneTask closes the non-status writer path into the
// stale-execution state: a commit-only call (status left nil, so the task stays done) that clears
// the commit to empty must be refused, and the prior commit left intact — the fail-fast keys off
// the resolved end-state, not just this call's --status.
func TestRecordTaskCannotClearCommitOnAlreadyDoneTask(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("abc1234")}, at0); err != nil {
		t.Fatalf("initial done with commit should succeed: %v", err)
	}
	err := RecordTask(&ex, "M1.P1.T1", RecordFields{Commit: ptrS("")}, at0)
	if err == nil {
		t.Fatal("clearing the commit on an already-done task must be refused")
	}
	if !strings.Contains(err.Error(), "M1.P1.T1") || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("error must name the task id and the missing field, got: %v", err)
	}
	if ex.Tasks[0].Status != StatusDone || ex.Tasks[0].Commit != "abc1234" {
		t.Fatalf("refused write must leave the prior done+commit row intact, got: %+v", ex.Tasks[0])
	}
}
