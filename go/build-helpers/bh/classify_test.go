package bh

import "testing"

func TestParseDesignFrontmatter(t *testing.T) {
	doc := "---\nname: X\ntags:\n  - status:stub\nupdated: 2026-06-25T10:00:00Z\n---\n# body"
	st, up := ParseDesignFrontmatter(doc)
	if st != "stub" || up != "2026-06-25T10:00:00Z" {
		t.Fatalf("got status=%q updated=%q", st, up)
	}
	if s, u := ParseDesignFrontmatter("no frontmatter here"); s != "" || u != "" {
		t.Fatalf("expected empties, got %q %q", s, u)
	}
}

func TestClassifyRoutes(t *testing.T) {
	complete := "---\ntags:\n  - status:complete\nupdated: 2026-06-25T12:00:00Z\n---\n"
	stub := "---\ntags:\n  - status:stub\nupdated: 2026-06-25T09:00:00Z\n---\n"
	cases := []struct {
		name  string
		in    ClassifyInput
		route string
	}{
		{"no design", ClassifyInput{DesignPresent: false}, RouteInteractiveBuild},
		{"stub design", ClassifyInput{DesignPresent: true, DesignText: stub}, RouteResumeDraft},
		{"complete no plan", ClassifyInput{DesignPresent: true, DesignText: complete}, RouteDerive},
		{"plan stale", ClassifyInput{DesignPresent: true, DesignText: complete, PlanPresent: true, PlanProvenanceDesignUpdated: "2026-06-25T08:00:00Z", ExecutionPresent: true}, RouteReconcile},
		{"plan fresh no exec", ClassifyInput{DesignPresent: true, DesignText: complete, PlanPresent: true, PlanProvenanceDesignUpdated: "2026-06-25T12:00:00Z"}, RouteDerive},
		{"all consistent", ClassifyInput{DesignPresent: true, DesignText: complete, PlanPresent: true, PlanProvenanceDesignUpdated: "2026-06-25T12:00:00Z", ExecutionPresent: true}, RouteReady},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.in).Route; got != c.route {
				t.Fatalf("route = %q, want %q", got, c.route)
			}
		})
	}
}

// TestClassifyRouteUnaffectedByArchival — Classify decides from doc presence/staleness only,
// never per-task completion (see Classify's doc comment), so an archived milestone (which shrinks
// plan.json/execution.json's task lists but leaves the docs themselves present) must not change
// the route. The cross-selector regression in batch_test.go
// exercises this against an actual archived-milestone fixture end to end; this pins the
// doc-presence contract directly.
func TestClassifyRouteUnaffectedByArchival(t *testing.T) {
	complete := "---\ntags:\n  - status:complete\nupdated: 2026-07-04T00:00:00Z\n---\n"
	in := ClassifyInput{DesignPresent: true, DesignText: complete, PlanPresent: true, PlanProvenanceDesignUpdated: "2026-07-04T00:00:00Z", ExecutionPresent: true}
	if got := Classify(in).Route; got != RouteReady {
		t.Fatalf("route = %q, want %q", got, RouteReady)
	}
}

func TestPlanIsStale(t *testing.T) {
	if !planIsStale("2026-06-25T12:00:00Z", "2026-06-25T08:00:00Z") {
		t.Fatal("newer design over older provenance must be stale")
	}
	if planIsStale("2026-06-25T12:00:00Z", "2026-06-25T12:00:00Z") {
		t.Fatal("equal timestamps are not stale")
	}
	if planIsStale("2026-06-25T12:00:00Z", "") {
		t.Fatal("missing provenance cannot prove stale")
	}
}

// TestClassifyEscalationNamedTriggers — each of the four named triggers routes to the magistrate
// with its trigger and a non-empty tier (acceptance 1), and persists as a well-formed
// escalation-event {trigger,tier,route,at,task_id?} to execution.json — the recorded
// event, not just the pure classify result, is what the equal-magistrate-firing void-check
// reads (execution.go's MagistrateFiringCount).
func TestClassifyEscalationNamedTriggers(t *testing.T) {
	for trigger, wantTier := range escalationTiers {
		t.Run(string(trigger), func(t *testing.T) {
			r := ClassifyEscalation(EscalationInput{Condition: string(trigger)})
			if r.Route != RouteMagistrate {
				t.Fatalf("route = %q, want %q", r.Route, RouteMagistrate)
			}
			if r.Trigger != trigger {
				t.Fatalf("trigger = %q, want %q", r.Trigger, trigger)
			}
			if r.Tier != wantTier {
				t.Fatalf("tier = %q, want %q", r.Tier, wantTier)
			}
			if r.DesignLevel {
				t.Fatal("named trigger must not be design-level")
			}

			p := validPlan()
			ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
			if err := RecordEscalationEvent(&ex, r.Trigger, r.Tier, r.Route, "M1.P1.T1", at0); err != nil {
				t.Fatalf("named trigger must persist: %v", err)
			}
			if len(ex.EscalationEvents) != 1 {
				t.Fatalf("expected 1 recorded escalation event, got %d", len(ex.EscalationEvents))
			}
			got := ex.EscalationEvents[0]
			if got.Trigger != trigger || got.Tier != wantTier || got.Route != RouteMagistrate || got.At != at0 || got.TaskID != "M1.P1.T1" {
				t.Fatalf("bad recorded escalation event: %+v", got)
			}
			if MagistrateFiringCount(ex) != 1 {
				t.Fatalf("expected magistrate firing count 1, got %d", MagistrateFiringCount(ex))
			}
		})
	}
	// Guard the closed set is exactly four members — a stray addition/removal fails here.
	if got := len(escalationTiers); got != 4 {
		t.Fatalf("closed trigger set has %d members, want 4", got)
	}
	if got := len(EscalationTriggers()); got != 4 {
		t.Fatalf("EscalationTriggers() returns %d, want 4", got)
	}
}

// TestClassifyEscalationNoCatchAll — a battery of out-of-set conditions (near-misses, wrong case,
// substrings, junk) must NEVER route to the magistrate (acceptance 2, no catch-all), and — even if
// a caller tried anyway — RecordEscalationEvent refuses to persist any of them as an escalation-
// event, so the recorded-event count (what the void-check reads) can never be inflated by a
// catch-all either.
func TestClassifyEscalationNoCatchAll(t *testing.T) {
	outOfSet := []string{
		"",
		"surprise_overlap",       // wrong separator
		"SURPRISE-OVERLAP",       // wrong case
		"surprise-overlap ",      // trailing space
		" surprise-overlap",      // leading space
		"surprise",               // substring
		"overlap",                // substring
		"local-delta",            // prefix only
		"replan",                 // substring
		"failed-task",            // prefix only
		"triage",                 // substring
		"phase-gate",             // prefix only
		"regression",             // substring
		"merge-conflict",         // plausible but unnamed
		"lint-failure",           // plausible but unnamed
		"design-drift",           // plausible but unnamed
		"magistrate",             // the route name is not a trigger
		"escalate",               // arbitrary
		"local-delta-replan-now", // superstring
	}
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	for _, cond := range outOfSet {
		t.Run(cond, func(t *testing.T) {
			r := ClassifyEscalation(EscalationInput{Condition: cond})
			if r.Route == RouteMagistrate {
				t.Fatalf("out-of-set condition %q routed to magistrate (catch-all)", cond)
			}
			if r.Route != RouteNoEscalation {
				t.Fatalf("route = %q, want %q", r.Route, RouteNoEscalation)
			}
			if err := RecordEscalationEvent(&ex, EscalationTrigger(cond), "xhigh", RouteMagistrate, "", at0); err == nil {
				t.Fatalf("out-of-set condition %q must be rejected by RecordEscalationEvent (catch-all persistence)", cond)
			}
		})
	}
	if len(ex.EscalationEvents) != 0 {
		t.Fatalf("no out-of-set condition may persist as a recorded event, got %+v", ex.EscalationEvents)
	}
}

// TestClassifyEscalationDesignLevel — any condition touching success_criteria/scope routes to
// plan-with-team, never the magistrate, even when the condition names a trigger (acceptance 3).
func TestClassifyEscalationDesignLevel(t *testing.T) {
	cases := []struct {
		name string
		in   EscalationInput
	}{
		{"success_criteria only", EscalationInput{Condition: "some-delta", TouchesSuccessCriteria: true}},
		{"scope only", EscalationInput{Condition: "some-delta", TouchesScope: true}},
		{"both", EscalationInput{Condition: "some-delta", TouchesSuccessCriteria: true, TouchesScope: true}},
		{"trigger name but scope-touching", EscalationInput{Condition: string(TriggerLocalDeltaReplan), TouchesScope: true}},
		{"trigger name but sc-touching", EscalationInput{Condition: string(TriggerSurpriseOverlap), TouchesSuccessCriteria: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ClassifyEscalation(c.in)
			if r.Route == RouteMagistrate {
				t.Fatalf("design-level condition routed to magistrate: %+v", c.in)
			}
			if r.Route != RoutePlanWithTeam {
				t.Fatalf("route = %q, want %q", r.Route, RoutePlanWithTeam)
			}
			if !r.DesignLevel {
				t.Fatal("expected design_level = true")
			}
		})
	}
}

// TestClassifyScope — the standalone scope classifier: design-level vs in-scope.
func TestClassifyScope(t *testing.T) {
	if r := ClassifyScope(ScopeInput{}); r.Route != RouteInScope || r.DesignLevel {
		t.Fatalf("no-touch must be in-scope, got route=%q design_level=%v", r.Route, r.DesignLevel)
	}
	for _, in := range []ScopeInput{{TouchesSuccessCriteria: true}, {TouchesScope: true}, {TouchesSuccessCriteria: true, TouchesScope: true}} {
		r := ClassifyScope(in)
		if r.Route != RoutePlanWithTeam || !r.DesignLevel {
			t.Fatalf("touch must be design-level -> plan-with-team, got route=%q design_level=%v", r.Route, r.DesignLevel)
		}
	}
}
