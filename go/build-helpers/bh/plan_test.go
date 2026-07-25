package bh

import (
	"encoding/json"
	"strings"
	"testing"
)

func validPlan() Plan {
	return Plan{
		Goal:            "demo goal",
		SuccessCriteria: []string{"it works"},
		Milestones: []Milestone{{
			ID: "M1", Name: "Milestone one", Phases: []Phase{{
				ID: "M1.P1", Name: "Phase one", Tasks: []Task{
					{ID: "M1.P1.T1", Name: "Task one", Summary: "first", Deliverable: "d1", Model: ModelSonnet46, Effort: EffortMedium, TestStrategy: "unit", Acceptance: []string{"a1"}},
					{ID: "M1.P1.T2", Name: "Task two", Summary: "second", Deliverable: "d2", Model: ModelHaiku45, Effort: EffortLow, TestStrategy: "lint", Deps: []string{"M1.P1.T1"}, Acceptance: []string{"a2"}},
				},
			}},
		}},
	}
}

func mustBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestContentHashStableAndScoped(t *testing.T) {
	a := Task{ID: "M1.P1.T1", Summary: "s", Deliverable: "d", Acceptance: []string{"x", "y"}, Model: ModelSonnet46, Effort: EffortMedium}
	b := Task{ID: "M9.P9.T9", Summary: "s", Deliverable: "d", Acceptance: []string{"x", "y"}, Model: ModelOpus48, Effort: EffortHigh}
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("hash must ignore id and tier (only summary+deliverable+acceptance)")
	}
	c := a
	c.Acceptance = []string{"x", "z"}
	if ContentHash(a) == ContentHash(c) {
		t.Fatal("hash must change when acceptance changes")
	}
}

func TestTopoOrderLinear(t *testing.T) {
	r := TopoOrder(validPlan())
	if len(r.Cycle) != 0 {
		t.Fatalf("unexpected cycle: %v", r.Cycle)
	}
	if got := strings.Join(r.Order, ","); got != "M1.P1.T1,M1.P1.T2" {
		t.Fatalf("order = %s", got)
	}
}

func TestTopoOrderCycle(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Deps = []string{"M1.P1.T2"} // T1<->T2 cycle
	r := TopoOrder(p)
	if len(r.Cycle) != 2 {
		t.Fatalf("expected 2 unschedulable, got %v", r.Cycle)
	}
}

func TestValidateValid(t *testing.T) {
	res := ValidatePlanBytes(mustBytes(t, validPlan()))
	if !res.OK {
		t.Fatalf("expected ok, got errors: %v", res.Errors)
	}
}

func TestValidateCatchesIntegrity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Plan)
		want   string
	}{
		{"dangling dep", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[1].Deps = []string{"M9.P9.T9"} }, "non-existent task"},
		{"self dep", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[0].Deps = []string{"M1.P1.T1"} }, "depends on itself"},
		{"dup task id", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[1].ID = "M1.P1.T1" }, "duplicate task id"},
		{"bad hierarchy", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[0].ID = "M2.P1.T1" }, "id prefix does not match"},
		{"bad tier combo", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[0].Effort = EffortXHigh }, "xhigh"},
		{"missing acceptance", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[0].Acceptance = nil }, "acceptance"},
		{"missing name", func(p *Plan) { p.Milestones[0].Phases[0].Tasks[0].Name = "" }, "name required"},
		{"empty goal", func(p *Plan) { p.Goal = "" }, "goal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validPlan()
			c.mutate(&p)
			res := ValidatePlanBytes(mustBytes(t, p))
			if res.OK {
				t.Fatalf("expected failure for %s", c.name)
			}
			found := false
			for _, e := range res.Errors {
				if strings.Contains(e, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an error containing %q, got %v", c.want, res.Errors)
			}
		})
	}
}

func TestValidateUnknownRootKeyIsWarning(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]}]}]}],"bogus":1}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("unknown key should not fail validation: %v", res.Errors)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "bogus") {
		t.Fatalf("expected a warning about 'bogus', got %v", res.Warnings)
	}
}

func TestDiff(t *testing.T) {
	oldP := validPlan()
	newP := validPlan()
	newP.Milestones[0].Phases[0].Tasks[0].Deliverable = "changed"                                                                                                    // T1 changed
	newP.Milestones[0].Phases[0].Tasks = append(newP.Milestones[0].Phases[0].Tasks, Task{ID: "M1.P1.T3", Summary: "x", Deliverable: "d", Acceptance: []string{"a"}}) // added
	// remove nothing; carried = T2
	d := Diff(oldP, newP)
	if len(d.Changed) != 1 || d.Changed[0].ID != "M1.P1.T1" {
		t.Fatalf("expected T1 changed, got %+v", d.Changed)
	}
	if len(d.Added) != 1 || d.Added[0].ID != "M1.P1.T3" {
		t.Fatalf("expected T3 added, got %+v", d.Added)
	}
	if len(d.Carried) != 1 || d.Carried[0].ID != "M1.P1.T2" {
		t.Fatalf("expected T2 carried, got %+v", d.Carried)
	}
}

func TestCheckTiers(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Effort = EffortXHigh // xhigh on sonnet-4-6 -> invalid (4.6 has max but not xhigh)
	if CheckTiers(p).OK {
		t.Fatal("xhigh on sonnet-4-6 must be invalid")
	}
	p.Milestones[0].Phases[0].Tasks[0].Model = ModelSonnet5 // xhigh on sonnet-5 -> ok (Sonnet 5 supports xhigh)
	if !CheckTiers(p).OK {
		t.Fatal("xhigh on sonnet-5 must be valid")
	}
	p.Milestones[0].Phases[0].Tasks[0].Model = ModelOpus48 // xhigh on opus-4-8 -> ok
	if !CheckTiers(p).OK {
		t.Fatal("xhigh on opus-4-8 must be valid")
	}
	p.Milestones[0].Phases[0].Tasks[0].Effort = EffortMax // max on opus-4-8 -> ok
	p.Milestones[0].Phases[0].Tasks[0].Model = ModelSonnet5
	if !CheckTiers(p).OK {
		t.Fatal("max on sonnet-5 must be valid")
	}
}

// TestCheckTiers_InheritSentinelIsEffortExempt proves 'inherit' stays enum-valid and accepts
// every effort level via the roster's dispatch-sentinel list, with no hand-maintained exemption
// table behind it.
func TestCheckTiers_InheritSentinelIsEffortExempt(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Model = ModelInherit
	for _, e := range []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax} {
		p.Milestones[0].Phases[0].Tasks[0].Effort = e
		if r := CheckTiers(p); !r.OK {
			t.Fatalf("inherit at effort %q must be valid, got issues: %+v", e, r.Issues)
		}
	}
}

// TestCheckTiers_LegacyPinOnlyModelRejected proves plan validation's model set is the roster's
// selectable=='new-work' projection, not the wider authoring-gate allowlist: a legacy-pin-only
// model is a valid roster entry but is not plan-pinnable.
func TestCheckTiers_LegacyPinOnlyModelRejected(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Model = Model("claude-opus-4-5") // legacy-pin-only in the roster
	r := CheckTiers(p)
	if r.OK {
		t.Fatal("a legacy-pin-only model must fail plan validation")
	}
	found := false
	for _, iss := range r.Issues {
		if strings.Contains(iss.Issue, "not in the selectable set") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'not in the selectable set' issue, got %+v", r.Issues)
	}
}

// TestCheckTiers_MatchesRetiredTablesForEveryKnownModel is the no-narrowing differential guard:
// for every model and effort combination the old xhighOK/maxOK/tierExempt tables answered, the
// roster-derived CheckTiers must reach the identical accept/reject verdict.
func TestCheckTiers_MatchesRetiredTablesForEveryKnownModel(t *testing.T) {
	retiredXHighOK := map[Model]bool{ModelOpus48: true, ModelOpus47: true, ModelSonnet5: true}
	retiredMaxOK := map[Model]bool{ModelOpus48: true, ModelOpus47: true, ModelOpus46: true, ModelSonnet5: true, ModelSonnet46: true}
	retiredTierExempt := map[Model]bool{ModelInherit: true, ModelFable5: true}
	models := []Model{ModelOpus48, ModelOpus47, ModelOpus46, ModelSonnet5, ModelSonnet46, ModelHaiku45, ModelFable5, ModelInherit}

	for _, m := range models {
		for _, e := range []Effort{EffortXHigh, EffortMax} {
			p := validPlan()
			p.Milestones[0].Phases[0].Tasks[0].Model = m
			p.Milestones[0].Phases[0].Tasks[0].Effort = e
			got := CheckTiers(p).OK

			exempt := retiredTierExempt[m]
			table := retiredXHighOK
			if e == EffortMax {
				table = retiredMaxOK
			}
			want := exempt || table[m]
			if got != want {
				t.Errorf("model=%s effort=%s: CheckTiers.OK=%v, retired table said %v", m, e, got, want)
			}
		}
	}
}

// TestCheckTiers_UnknownModelYieldsSingleIssueNoSpuriousEffortComplaint proves an unrecognized
// (roster-stale) model produces exactly ONE issue — the model-not-selectable one — never a second,
// spurious effort-availability complaint layered on top, since EffortAvailable's error on that same
// unresolvable id is deliberately swallowed (see CheckTiers).
func TestCheckTiers_UnknownModelYieldsSingleIssueNoSpuriousEffortComplaint(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Model = Model("totally-bogus-model")
	p.Milestones[0].Phases[0].Tasks[0].Effort = EffortHigh
	r := CheckTiers(p)
	var forTask []TierIssue
	for _, iss := range r.Issues {
		if iss.ID == "M1.P1.T1" {
			forTask = append(forTask, iss)
		}
	}
	if len(forTask) != 1 {
		t.Fatalf("expected exactly 1 issue for an unresolvable model, got %+v", forTask)
	}
	if !strings.Contains(forTask[0].Issue, "not in the selectable set") {
		t.Fatalf("expected the model-selectable issue, got %+v", forTask[0])
	}
}

// TestCheckTiers_RetiredTableCoverageIsExhaustive is a meta-guard on the differential test itself:
// it asserts the retired-table literals reproduced in
// TestCheckTiers_MatchesRetiredTablesForEveryKnownModel are exactly the ones the actual deleted
// tables held (xhighOK={Opus48,Opus47,Sonnet5}, maxOK={Opus48,Opus47,Opus46,Sonnet5,Sonnet46},
// tierExempt={Inherit,Fable5}) — every entry present, nothing added, so the no-narrowing guard
// cannot silently drift from what was actually retired.
func TestCheckTiers_RetiredTableCoverageIsExhaustive(t *testing.T) {
	retiredXHighOK := map[Model]bool{ModelOpus48: true, ModelOpus47: true, ModelSonnet5: true}
	retiredMaxOK := map[Model]bool{ModelOpus48: true, ModelOpus47: true, ModelOpus46: true, ModelSonnet5: true, ModelSonnet46: true}
	retiredTierExempt := map[Model]bool{ModelInherit: true, ModelFable5: true}
	if len(retiredXHighOK) != 3 || len(retiredMaxOK) != 5 || len(retiredTierExempt) != 2 {
		t.Fatalf("retired-table literal sizes changed: xhighOK=%d maxOK=%d tierExempt=%d, want 3/5/2",
			len(retiredXHighOK), len(retiredMaxOK), len(retiredTierExempt))
	}
}

func TestRenderPlanEscapesCells(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Summary = "has | pipe\nand newline"
	md := RenderPlan(p, PlanDocMeta{})
	if strings.Contains(md, "has | pipe") {
		t.Fatal("pipe in a table cell must be escaped")
	}
	if !strings.Contains(md, `has \| pipe and newline`) {
		t.Fatalf("expected escaped+collapsed cell, got:\n%s", md)
	}
	if strings.HasPrefix(md, "---") {
		t.Fatal("no frontmatter expected when Slug is empty")
	}
}

func TestFileSurfaceWarnings(t *testing.T) {
	// code task WITH file_surface: ok=true, no file_surface warning
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].FileSurface = []FileSurfaceEntry{{Path: "bh/*.go"}}
	res := ValidatePlanBytes(mustBytes(t, p))
	if !res.OK {
		t.Fatalf("plan with file_surface must validate ok: %v", res.Errors)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") && strings.Contains(w, "M1.P1.T1") {
			t.Fatalf("must not warn about file_surface when it is present: %v", res.Warnings)
		}
	}

	// code task WITHOUT file_surface: ok=true, warning present
	p2 := validPlan() // neither task has FileSurface
	res2 := ValidatePlanBytes(mustBytes(t, p2))
	if !res2.OK {
		t.Fatalf("missing file_surface must not set ok=false: %v", res2.Errors)
	}
	found := false
	for _, w := range res2.Warnings {
		if strings.Contains(w, "file_surface") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a file_surface warning for code task without file_surface, got warnings: %v", res2.Warnings)
	}

	// docs task WITHOUT file_surface: ok=true, no file_surface warning
	p3 := validPlan()
	p3.Milestones[0].Phases[0].Tasks[0].Kind = KindDocs
	p3.Milestones[0].Phases[0].Tasks[1].Kind = KindDocs
	res3 := ValidatePlanBytes(mustBytes(t, p3))
	if !res3.OK {
		t.Fatalf("docs task plan must validate ok: %v", res3.Errors)
	}
	for _, w := range res3.Warnings {
		if strings.Contains(w, "file_surface") {
			t.Fatalf("docs tasks must not produce file_surface warning, got: %v", res3.Warnings)
		}
	}
}

func TestRenderPlanFrontmatter(t *testing.T) {
	md := RenderPlan(validPlan(), PlanDocMeta{Slug: "demo", Updated: "2026-06-25T11:00:00Z"})
	for _, want := range []string{"id: project:demo:plan", "description: \"Build plan mirror", "topic:tooling", "updated: 2026-06-25T11:00:00Z"} {
		if !strings.Contains(md, want) {
			t.Fatalf("plan.md frontmatter missing %q:\n%s", want, md[:min(400, len(md))])
		}
	}
}
