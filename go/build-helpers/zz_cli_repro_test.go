package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCLI_RecordCommitOnlyCannotClearCommitOnAlreadyDoneTask drives the writer-side fail-fast
// through the actual CLI binary via the path that used to bypass it: a `record` call that supplies
// --commit (empty) with no --status flag on a task already status=done. The refusal is on the
// RESOLVED end-state, so this path is refused (exit 2, the existing usage-error code) and nothing
// is written — the stale-execution state M0.P8.T2 requires be unproducible by ANY writer path.
func TestCLI_RecordCommitOnlyCannotClearCommitOnAlreadyDoneTask(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(minimalPlanJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	execOut := runCLI(t, bin, "init-exec", planPath, "--slug", "demo", "--at", "2026-07-01T00:00:00Z")
	execPath := filepath.Join(dir, "execution.json")
	if err := os.WriteFile(execPath, execOut, 0o644); err != nil {
		t.Fatal(err)
	}

	// Legitimate done write with a commit.
	recorded := runCLI(t, bin, "record", execPath, "M1.P1.T1", "--status", "done", "--commit", "aaa1111", "--at", "2026-07-01T01:00:00Z")
	if err := os.WriteFile(execPath, recorded, 0o644); err != nil {
		t.Fatal(err)
	}

	// Second call: only --commit, set to empty, no --status flag at all — must be refused (exit 2).
	runCLIExpect(t, bin, 2, "record", execPath, "M1.P1.T1", "--commit", "", "--at", "2026-07-01T02:00:00Z")

	// The refused write must have persisted nothing: the on-disk row still carries the commit.
	onDisk, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	var ex map[string]any
	if err := json.Unmarshal(onDisk, &ex); err != nil {
		t.Fatalf("cannot parse execution.json: %v\n%s", err, onDisk)
	}
	task := ex["tasks"].([]any)[0].(map[string]any)
	if task["status"] != "done" || task["commit"] != "aaa1111" {
		t.Fatalf("refused write must leave the prior done+commit row intact, got: %+v", task)
	}
}
