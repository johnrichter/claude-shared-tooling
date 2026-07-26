package state

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// Status is a task's lifecycle state.
type Status string

// The closed set of statuses RecordTask and WriteTasks reason about. A consumer's own
// task model may carry additional statuses; only Done participates in the rung-1
// invariant below.
const (
	StatusNotStarted Status = "not-started"
	StatusInProgress Status = "in-progress"
	StatusDone       Status = "done"
	StatusBlocked    Status = "blocked"
)

// Task is one unit of tracked work. The rung-1 invariant this package enforces is scoped
// to Status and CommitSHA: a task can never be persisted with Status done while CommitSHA
// is empty (and none is already recorded on it) — see ErrDoneRequiresCommit. CostUSD carries
// no independent guard; a done task with a real commit SHA and CostUSD left at 0 is valid.
type Task struct {
	ID        string
	Status    Status
	CommitSHA string
	CostUSD   float64
}

// ErrDoneRequiresCommit is the rung-1 refusal: a caller tried to leave (or create) a task
// row reading done with no commit SHA and none already recorded. Named and returned, never
// merely logged — a warning here is exactly the failure mode this invariant exists to
// close: a doc set where every task reads done with no SHA and no cost renders a build
// falsely green, and a reconcile then carries that fake work forward as completed.
var ErrDoneRequiresCommit = errors.New("state: status done requires a commit sha — none supplied and none already recorded")

// RecordTask applies a status/commit-sha/cost transition to t. Nil fields mean "leave
// unchanged". The write is refused — t left completely untouched, ErrDoneRequiresCommit
// returned — whenever the RESULTING row would read done with an empty commit sha: whether
// because a done transition supplies no sha (and t carries none already), or because a
// write clears the sha on a task already done. Checking the resolved end-state, not just
// this call's own fields, closes both paths into the same bad state.
func RecordTask(t *Task, status *Status, commitSHA *string, costUSD *float64) error {
	resultStatus := t.Status
	if status != nil {
		resultStatus = *status
	}
	resultCommit := t.CommitSHA
	if commitSHA != nil {
		resultCommit = *commitSHA
	}
	if resultStatus == StatusDone && strings.TrimSpace(resultCommit) == "" {
		return fmt.Errorf("%w: task %q", ErrDoneRequiresCommit, t.ID)
	}
	if status != nil {
		t.Status = *status
	}
	if commitSHA != nil {
		t.CommitSHA = *commitSHA
	}
	if costUSD != nil {
		t.CostUSD = *costUSD
	}
	return nil
}

// WriteTasks validates every row in tasks against the same rung-1 invariant RecordTask
// enforces, refusing the entire write (nothing persisted) if any row reads done with an
// empty commit sha, then atomically persists tasks to path. This is the on-disk choke
// point: even a task built by hand rather than mutated through RecordTask cannot reach
// disk in the bad state, so no writer path in this package can ever produce a state file
// where every task reads done with no sha and no cost.
func WriteTasks(path string, tasks []Task, perm fs.FileMode) error {
	for _, t := range tasks {
		if t.Status == StatusDone && strings.TrimSpace(t.CommitSHA) == "" {
			return fmt.Errorf("%w: task %q", ErrDoneRequiresCommit, t.ID)
		}
	}
	rows := make([]any, len(tasks))
	for i, t := range tasks {
		rows[i] = map[string]any{
			"id":         t.ID,
			"status":     string(t.Status),
			"commit_sha": t.CommitSHA,
			"cost_usd":   t.CostUSD,
		}
	}
	doc := Doc{versionKey: TasksSchemaVersion, "tasks": rows}
	return Write(path, doc, perm)
}

// TasksSchemaVersion is the current schema version of a WriteTasks document.
const TasksSchemaVersion = 1

// ReadTasks loads path and returns its decoded tasks. A missing, empty, or corrupt file
// degrades to an empty slice with a nil error, consistent with Read's safe-degradation
// contract — a damaged task-state file never blocks a caller's run.
func ReadTasks(path string) ([]Task, error) {
	doc, err := Read(path, TasksSchemaVersion, Migrations{})
	if err != nil {
		return nil, err
	}
	raw, ok := doc["tasks"].([]any)
	if !ok {
		return nil, nil
	}
	tasks := make([]Task, 0, len(raw))
	for _, v := range raw {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		t := Task{}
		if s, ok := row["id"].(string); ok {
			t.ID = s
		}
		if s, ok := row["status"].(string); ok {
			t.Status = Status(s)
		}
		if s, ok := row["commit_sha"].(string); ok {
			t.CommitSHA = s
		}
		if n, ok := row["cost_usd"].(float64); ok {
			t.CostUSD = n
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
