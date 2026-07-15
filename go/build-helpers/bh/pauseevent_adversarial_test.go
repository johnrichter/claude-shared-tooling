package bh

import (
	"encoding/json"
	"strings"
	"testing"
)

// Adversarial checks beyond the implementer's suite: rejection must not have partial side
// effects, empty-string reason is rejected (not silently treated as valid), MechanicalSlipCount
// on a nil/zero-value ExecState.PauseEvents is 0, and event order/count is preserved across
// multiple appends of the same reason.

func TestRecordPauseEvent_RejectionHasNoSideEffects(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	before := ex.Updated
	beforeLogLen := len(ex.Log)
	if err := RecordPauseEvent(&ex, PauseReason(""), "M1.P1.T1", "2026-06-25T12:00:00Z"); err == nil {
		t.Fatal("expected error for empty reason_enum")
	}
	if ex.Updated != before {
		t.Fatalf("Updated must not change on rejection: got %q want %q", ex.Updated, before)
	}
	if len(ex.Log) != beforeLogLen {
		t.Fatalf("Log must not grow on rejection: got %d want %d", len(ex.Log), beforeLogLen)
	}
	if len(ex.PauseEvents) != 0 {
		t.Fatalf("PauseEvents must not grow on rejection: got %+v", ex.PauseEvents)
	}
}

func TestMechanicalSlipCount_ZeroValueExecState(t *testing.T) {
	var ex ExecState
	if got := MechanicalSlipCount(ex); got != 0 {
		t.Fatalf("zero-value ExecState: expected 0, got %d", got)
	}
}

func TestMechanicalSlipCount_RepeatedSameReasonAccumulates(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	for i := 0; i < 5; i++ {
		if err := RecordPauseEvent(&ex, PauseState, "", at0); err != nil {
			t.Fatal(err)
		}
	}
	if got := MechanicalSlipCount(ex); got != 5 {
		t.Fatalf("expected 5 repeated state slips, got %d", got)
	}
	if len(ex.PauseEvents) != 5 {
		t.Fatalf("expected 5 pause events persisted, got %d", len(ex.PauseEvents))
	}
}

func TestPauseReason_CaseSensitivity(t *testing.T) {
	// Reason strings must be exact-match; "Git"/"GIT" are not silently accepted as "git".
	if PauseReason("Git").Known() {
		t.Fatal("PauseReason must be case-sensitive: \"Git\" should not be Known")
	}
	if PauseReason("GIT").Known() {
		t.Fatal("PauseReason must be case-sensitive: \"GIT\" should not be Known")
	}
}

func TestPauseEvent_JSONFieldNames(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordPauseEvent(&ex, PauseGit, "M1.P1.T1", at0)
	rawBytes, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawBytes)
	for _, key := range []string{`"reason_enum":"git"`, `"at":"` + at0, `"task_id":"M1.P1.T1"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("expected marshaled execution.json to contain %q, got %s", key, raw)
		}
	}
}
