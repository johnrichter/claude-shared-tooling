package bh

import (
	"encoding/json"
	"testing"
)

// Adversarial coverage for M13.P2.T3: per-task/per-run recording of all four token classes
// (input, cache_write, cache_read, output) — never output-only, and per-run totals always
// recomputed from per-task rows, never hand-summed.

// usageOf builds a Usage with distinct, cache_read-dominated values so a test that merely summed
// output tokens (the historical basis) cannot pass by accident.
func usageOf(in, cw, cr, out, turns int64) *Usage {
	return &Usage{
		InputTokens:         in,
		CacheCreationTokens: cw,
		CacheReadTokens:     cr,
		OutputTokens:        out,
		TotalTokens:         in + cw + cr + out,
		Turns:               turns,
	}
}

func TestRecordTask_RecordsAllFourTokenClassesPerTask(t *testing.T) {
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	u := usageOf(500, 200, 9000, 300, 4) // cache_read (9000) dwarfs output (300) — the O-is-cache-read-dominated case
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Cost: ptrF(0.10), TokensOut: ptrI(300), Usage: u}, at0); err != nil {
		t.Fatal(err)
	}
	row := ex.Tasks[0]
	if row.Usage == nil {
		t.Fatal("ExecTask.Usage is nil after RecordTask with Usage set")
	}
	if row.Usage.InputTokens != 500 || row.Usage.CacheCreationTokens != 200 || row.Usage.CacheReadTokens != 9000 || row.Usage.OutputTokens != 300 {
		t.Fatalf("per-task Usage mismatch: %+v", row.Usage)
	}
	if row.Usage.CacheReadTokens <= row.Usage.OutputTokens {
		t.Fatal("fixture invariant broken: cache_read must dominate output for this test to be meaningful")
	}
	// Output-only field remains intact for backward compatibility.
	if row.TokensOut != 300 {
		t.Fatalf("TokensOut (backward-compat field) = %d, want 300", row.TokensOut)
	}
}

func TestRecordTask_RunUsageIsRecomputedSumOfTaskUsage_NeverHandSummed(t *testing.T) {
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	u1 := usageOf(100, 10, 1000, 50, 2)
	u2 := usageOf(200, 20, 2000, 60, 3)
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), TokensOut: ptrI(50), Usage: u1}, at0); err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), TokensOut: ptrI(60), Usage: u2}, at0); err != nil {
		t.Fatal(err)
	}
	got := ex.RunConfig.Usage
	if got == nil {
		t.Fatal("RunConfig.Usage is nil after two per-task Usage records")
	}
	want := usageOf(300, 30, 3000, 110, 5)
	if got.InputTokens != want.InputTokens || got.CacheCreationTokens != want.CacheCreationTokens ||
		got.CacheReadTokens != want.CacheReadTokens || got.OutputTokens != want.OutputTokens ||
		got.TotalTokens != want.TotalTokens || got.Turns != want.Turns {
		t.Fatalf("run Usage = %+v, want %+v (must equal Σ per-task Usage)", got, want)
	}

	// Mutate a task's stored Usage directly (simulating a hand-summed drift) and re-record a
	// no-op transition on the OTHER task: recomputeTotals must rebuild from the (unmutated) rows
	// again, proving the run total is a pure function of current per-task state, not a running
	// accumulator that could silently diverge.
	ex.Tasks[0].Usage.OutputTokens = 99999 // simulate external corruption of a per-task row
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{Note: ptrS("no-op re-record")}, at0); err != nil {
		t.Fatal(err)
	}
	if ex.RunConfig.Usage.OutputTokens != 99999+60 {
		t.Fatalf("run total did not re-derive from current per-task rows: got %d, want %d", ex.RunConfig.Usage.OutputTokens, 99999+60)
	}
}

func TestRecordTask_NoUsageFlag_LeavesUsageNilAndCostFieldsIntact(t *testing.T) {
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Cost: ptrF(0.25), TokensOut: ptrI(1000)}, at0); err != nil {
		t.Fatal(err)
	}
	if ex.Tasks[0].Usage != nil {
		t.Fatalf("Usage should stay nil when RecordFields.Usage is nil, got %+v", ex.Tasks[0].Usage)
	}
	if ex.Tasks[0].CostUSD != 0.25 || ex.Tasks[0].TokensOut != 1000 {
		t.Fatalf("legacy cost/tokens_out fields must remain intact: %+v", ex.Tasks[0])
	}
	if ex.RunConfig.Usage != nil {
		t.Fatalf("RunConfig.Usage should stay nil when no task has recorded Usage yet, got %+v", ex.RunConfig.Usage)
	}
	// Legacy aggregate (output-only, hand-recomputed the old way) must still work unchanged.
	if ex.RunConfig.SpentUSD != 0.25 || ex.RunConfig.TokensOut != 1000 {
		t.Fatalf("legacy run aggregates must remain intact: spent=%v tokensOut=%v", ex.RunConfig.SpentUSD, ex.RunConfig.TokensOut)
	}
}

func TestRecordTask_MixedUsageAndNoUsageTasks_RunTotalIsSumOfOnlyRecorded(t *testing.T) {
	// One task with Usage, one without — the run total must equal exactly the recorded task's
	// Usage (nil rows contribute zero, not an error, and never poison the sum).
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	u := usageOf(10, 1, 100, 5, 1)
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), TokensOut: ptrI(5), Usage: u}, at0); err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Cost: ptrF(0.05)}, at0); err != nil {
		t.Fatal(err)
	}
	got := ex.RunConfig.Usage
	if got == nil || got.InputTokens != 10 || got.CacheReadTokens != 100 || got.OutputTokens != 5 || got.TotalTokens != 116 {
		t.Fatalf("run Usage should equal the single recorded task's Usage exactly, got %+v", got)
	}
}

func TestArchive_CarriesUsageIntoTombstoneAndArchivedTask_WholeProjectTotalUnchanged(t *testing.T) {
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	u1 := usageOf(100, 10, 5000, 40, 3)
	u2 := usageOf(50, 5, 2500, 20, 2)
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Commit: ptrS("aaa1111"), Cost: ptrF(0.30), TokensOut: ptrI(40), Usage: u1}, at0); err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{Status: ptr(StatusDone), Commit: ptrS("bbb2222"), Cost: ptrF(0.20), TokensOut: ptrI(20), Usage: u2}, at0); err != nil {
		t.Fatal(err)
	}
	preUsage := *ex.RunConfig.Usage

	out, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatalf("archive should succeed on a wholly-done milestone: %v", err)
	}

	// Whole-project total is unchanged by archiving (it's a Tasks+Archived recompute, not a
	// running accumulator archiving could reset).
	if out.Exec.RunConfig.Usage == nil || *out.Exec.RunConfig.Usage != preUsage {
		t.Fatalf("Usage total changed by archiving: pre=%+v post=%+v", preUsage, out.Exec.RunConfig.Usage)
	}

	tomb := map[string]Tombstone{}
	for _, x := range out.Exec.Archived {
		tomb[x.ID] = x
	}
	if tomb["M1.P1.T1"].Usage == nil || *tomb["M1.P1.T1"].Usage != *u1 {
		t.Fatalf("tombstone dropped task Usage: got %+v, want %+v", tomb["M1.P1.T1"].Usage, u1)
	}

	found := false
	for _, m := range out.Archive.Milestones {
		for _, ph := range m.Phases {
			for _, at := range ph.Tasks {
				if at.ID == "M1.P1.T1" {
					found = true
					if at.Usage == nil || *at.Usage != *u1 {
						t.Fatalf("ArchivedTask dropped Usage: got %+v, want %+v", at.Usage, u1)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("archived task M1.P1.T1 not found in archive.json milestones")
	}
}

func TestUsage_JSONRoundTrip_OmitsNilAndPreservesAllFourClasses(t *testing.T) {
	// Nil Usage must not appear in serialized output (backward-compat with every pre-existing
	// execution.json that has no "usage" key at all).
	et := ExecTask{ID: "x", Status: StatusDone}
	b, err := json.Marshal(et)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatal(err)
	}
	if _, present := probe["usage"]; present {
		t.Fatalf("usage key should be omitted when nil, got raw: %s", b)
	}

	// Present Usage round-trips every one of the four classes exactly.
	u := usageOf(111, 22, 3333, 44, 5)
	et.Usage = u
	b2, err := json.Marshal(et)
	if err != nil {
		t.Fatal(err)
	}
	var et2 ExecTask
	if err := json.Unmarshal(b2, &et2); err != nil {
		t.Fatal(err)
	}
	if et2.Usage == nil || *et2.Usage != *u {
		t.Fatalf("Usage did not round-trip: got %+v, want %+v", et2.Usage, u)
	}
}

func TestReconcileExec_CarriesPerTaskUsageForward(t *testing.T) {
	p := archivePlan()
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	if err != nil {
		t.Fatal(err)
	}
	u := usageOf(10, 1, 100, 5, 1)
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Usage: u}, at0); err != nil {
		t.Fatal(err)
	}
	// Reconcile against the identical plan (no changes) — a carried row must keep its Usage.
	newP := archivePlan()
	ReconcileExec(&ex, p, newP, "", "", at0)
	var row *ExecTask
	for i := range ex.Tasks {
		if ex.Tasks[i].ID == "M1.P1.T1" {
			row = &ex.Tasks[i]
		}
	}
	if row == nil {
		t.Fatal("M1.P1.T1 missing after reconcile of an unchanged plan")
	}
	if row.Usage == nil || *row.Usage != *u {
		t.Fatalf("carried row lost its Usage across reconcile: got %+v, want %+v", row.Usage, u)
	}
	if ex.RunConfig.Usage == nil || ex.RunConfig.Usage.CacheReadTokens != 100 {
		t.Fatalf("run Usage not recomputed correctly after reconcile: %+v", ex.RunConfig.Usage)
	}
}
