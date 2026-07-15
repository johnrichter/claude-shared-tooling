package bh

import (
	"strings"
	"testing"
)

// TestReadoutsSurfaceIDAndName is the durable enforcement for M12.P1.T2: every human/engine
// readout that names a task must expose both Task.ID and Task.Name — not Summary alone, not ID
// alone. Uses a named fixture (validPlan's M1.P1.T1 = "Task one", M1.P1.T2 = "Task two") so a
// regression that drops Name (or silently substitutes Summary) fails loudly and specifically.

func TestRenderPlanEmitsIDDashName(t *testing.T) {
	p := validPlan()
	md := RenderPlan(p, PlanDocMeta{Slug: "demo"})
	for _, want := range []string{"M1.P1.T1 — Task one", "M1.P1.T2 — Task two"} {
		if !strings.Contains(md, want) {
			t.Fatalf("plan.md missing %q:\n%s", want, md)
		}
	}
}

func TestRenderExecutionEmitsIDDashName(t *testing.T) {
	p := validPlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	md := RenderExecution(ex, p)
	// Resume pointer: fresh state resumes at M1.P1.T1.
	if !strings.Contains(md, "Resume here →** M1.P1.T1 — Task one") {
		t.Fatalf("execution.md resume pointer missing id-dash-name:\n%s", md)
	}
	// Per-task progress row.
	for _, want := range []string{"M1.P1.T1 — Task one", "M1.P1.T2 — Task two"} {
		if !strings.Contains(md, want) {
			t.Fatalf("execution.md progress table missing %q:\n%s", want, md)
		}
	}
}

func TestNextTaskInfoCarriesName(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	r := NextTask(ex, p)
	if r.Task == nil {
		t.Fatal("expected an eligible task")
	}
	if r.Task.ID != "M1.P1.T1" || r.Task.Name != "Task one" {
		t.Fatalf("NextTaskInfo = %+v, want ID=M1.P1.T1 Name=\"Task one\"", r.Task)
	}
	// JSON round-trip: the CLI's `next` command prints this struct directly — the "name" key
	// must survive marshaling, not just exist on the Go struct.
	b := mustBytes(t, r)
	if !strings.Contains(string(b), `"name":"Task one"`) {
		t.Fatalf("next JSON output missing name field: %s", b)
	}
}

func TestBatchTaskCarriesName(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	res := BatchTasks(ex, p, MaxBatch)
	if len(res.Tasks) != 1 || res.Tasks[0].ID != "M1.P1.T1" || res.Tasks[0].Name != "Task one" {
		t.Fatalf("BatchTasks = %+v, want single task M1.P1.T1/\"Task one\"", res.Tasks)
	}
	b := mustBytes(t, res)
	if !strings.Contains(string(b), `"name":"Task one"`) {
		t.Fatalf("batch JSON output missing name field: %s", b)
	}
}

// TestBatchTaskNameNotSummary guards against a copy-paste regression that silently populates
// BatchTask.Name from Summary instead of the plan's Task.Name — the two fields have visibly
// different fixture values (Name="Task one", Summary="first"), so the two can never be confused
// if this assertion holds.
func TestBatchTaskNameNotSummary(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	res := BatchTasks(ex, p, MaxBatch)
	got := res.Tasks[0]
	if got.Name == got.Summary {
		t.Fatalf("Name and Summary collapsed to the same value %q — fixture regression, not a real pass", got.Name)
	}
	if got.Name != "Task one" {
		t.Fatalf("BatchTask.Name = %q, want %q (Task.Name, not Summary %q)", got.Name, "Task one", got.Summary)
	}
}

func TestRetrieveOutlineUsesTaskName(t *testing.T) {
	p := validPlan()
	entries := planOutline(p)
	found := false
	for _, e := range entries {
		if e.ID != "M1.P1.T1" {
			continue
		}
		found = true
		if e.Name != "Task one" {
			t.Fatalf("outline entry for M1.P1.T1 has Name=%q, want %q", e.Name, "Task one")
		}
	}
	if !found {
		t.Fatal("M1.P1.T1 not found in plan outline")
	}
}

func TestTaskNameHelperReturnsName(t *testing.T) {
	task := Task{ID: "X", Name: "Distinct Name", Summary: "distinct summary"}
	if got := taskName(task); got != "Distinct Name" {
		t.Fatalf("taskName() = %q, want Task.Name %q (not Summary %q)", got, "Distinct Name", task.Summary)
	}
}
