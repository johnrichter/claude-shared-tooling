package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func statusPtr(s Status) *Status  { return &s }
func stringPtr(s string) *string  { return &s }
func floatPtr(f float64) *float64 { return &f }

// TestRecordTaskRefusesDoneWithNoCommit is the worked rung-1 case: a done transition that
// supplies no commit sha, on a task that carries none already, is refused — the task left
// completely untouched.
func TestRecordTaskRefusesDoneWithNoCommit(t *testing.T) {
	task := Task{ID: "M1.T1", Status: StatusInProgress}
	before := task
	err := RecordTask(&task, statusPtr(StatusDone), nil, nil)
	if !errors.Is(err, ErrDoneRequiresCommit) {
		t.Fatalf("RecordTask error = %v, want ErrDoneRequiresCommit", err)
	}
	if task != before {
		t.Fatalf("task mutated on refusal: got %+v, want unchanged %+v", task, before)
	}
}

// TestRecordTaskRefusesClearingCommitOnDoneTask confirms the resulting-row check, not
// just this call's own fields: clearing the commit on an already-done task is refused too.
func TestRecordTaskRefusesClearingCommitOnDoneTask(t *testing.T) {
	task := Task{ID: "M1.T1", Status: StatusDone, CommitSHA: "abc123"}
	before := task
	err := RecordTask(&task, nil, stringPtr(""), nil)
	if !errors.Is(err, ErrDoneRequiresCommit) {
		t.Fatalf("RecordTask error = %v, want ErrDoneRequiresCommit", err)
	}
	if task != before {
		t.Fatalf("task mutated on refusal: got %+v, want unchanged %+v", task, before)
	}
}

// TestRecordTaskAllowsDoneWithCommit confirms a done transition supplying a commit sha
// succeeds and applies.
func TestRecordTaskAllowsDoneWithCommit(t *testing.T) {
	task := Task{ID: "M1.T1", Status: StatusInProgress}
	if err := RecordTask(&task, statusPtr(StatusDone), stringPtr("abc123"), floatPtr(1.5)); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	if task.Status != StatusDone || task.CommitSHA != "abc123" || task.CostUSD != 1.5 {
		t.Fatalf("task = %+v, want done/abc123/1.5", task)
	}
}

// TestRecordTaskAllowsDoneReRecordWithExistingCommit confirms a task already done with a
// commit may re-record done (or update other fields) without repeating the commit.
func TestRecordTaskAllowsDoneReRecordWithExistingCommit(t *testing.T) {
	task := Task{ID: "M1.T1", Status: StatusDone, CommitSHA: "abc123"}
	if err := RecordTask(&task, statusPtr(StatusDone), nil, floatPtr(2)); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	if task.CommitSHA != "abc123" || task.CostUSD != 2 {
		t.Fatalf("task = %+v, want commit preserved and cost updated", task)
	}
}

// TestWriteTasksRefusesDoneWithNoCommitEvenBypassingRecordTask confirms the on-disk choke
// point: a Task struct built by hand (not through RecordTask) that reads done with no
// commit is still refused at WriteTasks, and nothing is persisted.
func TestWriteTasksRefusesDoneWithNoCommitEvenBypassingRecordTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	tasks := []Task{
		{ID: "M1.T1", Status: StatusDone, CommitSHA: "abc123"},
		{ID: "M1.T2", Status: StatusDone}, // built by hand — no commit
	}
	err := WriteTasks(path, tasks, 0o644)
	if !errors.Is(err, ErrDoneRequiresCommit) {
		t.Fatalf("WriteTasks error = %v, want ErrDoneRequiresCommit", err)
	}
	got, readErr := ReadTasks(path)
	if readErr != nil {
		t.Fatalf("ReadTasks: %v", readErr)
	}
	if len(got) != 0 {
		t.Fatalf("WriteTasks refusal still persisted %d rows, want none", len(got))
	}
}

// TestWriteReadTasksRoundTrip confirms a valid task set round-trips through WriteTasks and
// ReadTasks.
func TestWriteReadTasksRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	want := []Task{
		{ID: "M1.T1", Status: StatusDone, CommitSHA: "abc123", CostUSD: 1.5},
		{ID: "M1.T2", Status: StatusNotStarted},
	}
	if err := WriteTasks(path, want, 0o644); err != nil {
		t.Fatalf("WriteTasks: %v", err)
	}
	got, err := ReadTasks(path)
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestNoWriterPathProducesAllDoneNoShaNoCost is the regression the rung-1 invariant
// closes: sweeping through RecordTask on a freshly not-started task set, no sequence of
// transitions can leave a done row with no sha and no cost — the FB6 fake-green scenario a
// reconcile could otherwise carry forward as completed.
func TestNoWriterPathProducesAllDoneNoShaNoCost(t *testing.T) {
	tasks := []Task{{ID: "M1.T1", Status: StatusNotStarted}, {ID: "M1.T2", Status: StatusNotStarted}}
	for i := range tasks {
		if err := RecordTask(&tasks[i], statusPtr(StatusDone), nil, nil); err == nil {
			t.Fatalf("RecordTask allowed done with no commit and no cost on %+v", tasks[i])
		}
	}
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := WriteTasks(path, tasks, 0o644); err != nil {
		t.Fatalf("WriteTasks on never-mutated not-started tasks: %v", err)
	}
	got, err := ReadTasks(path)
	if err != nil {
		t.Fatalf("ReadTasks: %v", err)
	}
	for _, row := range got {
		if row.Status == StatusDone && row.CommitSHA == "" {
			t.Fatalf("persisted state has a done task with no commit: %+v", row)
		}
	}
}
