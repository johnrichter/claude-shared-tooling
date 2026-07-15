package bh

import (
	"strings"
	"testing"
)

// consumedField describes one Task field that a downstream, non-render consumer reads — the
// seam rule:tooling:plan-md-field-parity enumerates: Task.Thinking is read by the build
// engine's announce/tierLine (spawn-tier cue); Task.FileSurface is read by batch.go's
// fileSurfaceOverlap (batch-conflict detection). Every entry here is a claim that RenderPlan
// must also surface the field into the plan.md mirror, so an operator approving plan.md sees
// the same data the engine/batch selector actually acts on.
type consumedField struct {
	name   string
	marker string
	set    func(t *Task, marker string)
}

var engineBatchConsumedFields = []consumedField{
	{
		name:   "Thinking",
		marker: "SENTINEL-THINKING-CUE-9f3a2c",
		set:    func(t *Task, marker string) { t.Thinking = marker },
	},
	{
		name:   "FileSurface",
		marker: "sentinel/file-surface-9f3a2c/*.go",
		set:    func(t *Task, marker string) { t.FileSurface = []FileSurfaceEntry{{Path: marker}} },
	},
	{
		// Name became operator/engine-visible in the same change that adds it to render/renderexec/
		// next/batch/retrieve readouts (M12.P1.T2) — next's NextTaskInfo.Name and batch's
		// BatchTask.Name both read Task.Name, so the plan.md mirror must also surface it.
		name:   "Name",
		marker: "Sentinel Task Name 9f3a2c",
		set:    func(t *Task, marker string) { t.Name = marker },
	},
}

// TestRenderPlanFieldParity is the durable enforcement for rule:tooling:plan-md-field-parity.
// It enumerates every Task field a downstream engine/batch consumer reads (see
// engineBatchConsumedFields) and asserts RenderPlan interpolates that field's value somewhere
// in the plan.md mirror it produces. On the current render.go this FAILS for both Thinking and
// FileSurface — RenderPlan's per-task table and bullet block never reference t.Thinking or
// t.FileSurface — which empirically proves the divergence the rule documents: an operator
// approving plan.md would not see the spawn-tier cue or file-surface data the engine/batch
// selector actually consumes. It must pass once RenderPlan is updated to emit both fields.
func TestRenderPlanFieldParity(t *testing.T) {
	for _, f := range engineBatchConsumedFields {
		t.Run(f.name, func(t *testing.T) {
			p := validPlan()
			task := &p.Milestones[0].Phases[0].Tasks[0]
			f.set(task, f.marker)
			md := RenderPlan(p, PlanDocMeta{})
			if !strings.Contains(md, f.marker) {
				t.Fatalf("RenderPlan output does not include Task.%s (marker %q) anywhere in the plan.md mirror; the field is consumed by the engine/batch selector but silently dropped from the operator-approved mirror:\n%s", f.name, f.marker, md)
			}
		})
	}
}

// TestRenderPlanTypedFileSurface pins acceptance #3's render clause (M13.P3.T1): the plan.md
// mirror must show the TYPED file_surface shape — path plus its kind, and ", required" only for
// a required entry — not just the bare path. An empty Kind renders as the "file" default.
func TestRenderPlanTypedFileSurface(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].FileSurface = []FileSurfaceEntry{
		{Path: "cmd/report/*.go", Kind: FSGlob, Required: true},
		{Path: "docs/notes.md"}, // empty kind -> "file", not required
	}
	md := RenderPlan(p, PlanDocMeta{})
	for _, want := range []string{"cmd/report/*.go (glob, required)", "docs/notes.md (file)"} {
		if !strings.Contains(md, want) {
			t.Fatalf("RenderPlan output missing typed file_surface annotation %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "docs/notes.md (file, required)") {
		t.Fatalf("non-required entry must NOT render as required:\n%s", md)
	}
}
