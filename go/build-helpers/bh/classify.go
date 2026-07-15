package bh

import (
	"regexp"
	"strings"
	"time"
)

var (
	reFrontmatter = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---`)
	reStatusTag   = regexp.MustCompile(`status:(stub|complete)`)
	reUpdated     = regexp.MustCompile(`(?m)^\s*updated:\s*(.+?)\s*$`)
)

// ParseDesignFrontmatter extracts the design's status tag (stub|complete) and updated
// timestamp from its YAML frontmatter. Returns ("","") when absent.
func ParseDesignFrontmatter(text string) (status, updated string) {
	m := reFrontmatter.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	fm := m[1]
	if s := reStatusTag.FindStringSubmatch(fm); s != nil {
		status = s[1]
	}
	if u := reUpdated.FindStringSubmatch(fm); u != nil {
		updated = strings.Trim(u[1], `"'`)
	}
	return status, updated
}

// ClassifyInput is the observed on-disk state of a project's docs (the CLI gathers it).
type ClassifyInput struct {
	DesignPresent               bool
	DesignText                  string // raw design.md contents (for frontmatter parse)
	PlanPresent                 bool
	PlanProvenanceDesignUpdated string // plan.json provenance.design_updated ("" if none)
	ExecutionPresent            bool   // execution.json present
	MirrorPresent               bool   // execution.md present
}

type designState struct {
	Present bool   `json:"present"`
	Status  string `json:"status,omitempty"`
	Updated string `json:"updated,omitempty"`
}
type planState struct {
	Present                 bool   `json:"present"`
	ProvenanceDesignUpdated string `json:"provenance_design_updated,omitempty"`
}
type execStateInfo struct {
	Present       bool `json:"present"`
	MirrorPresent bool `json:"mirror_present"`
}

// ClassifyResult is the deterministic front-door route plus the observed state it derives from.
type ClassifyResult struct {
	Design    designState   `json:"design"`
	Plan      planState     `json:"plan"`
	Execution execStateInfo `json:"execution"`
	Route     string        `json:"route"`
}

// Routes the front door can take.
const (
	RouteInteractiveBuild = "interactive-build" // no design -> build one with the operator
	RouteResumeDraft      = "resume-draft"      // design is a stub -> resume the draft loop
	RouteDerive           = "derive"            // design complete, no plan/exec -> derive plan + exec
	RouteReconcile        = "reconcile"         // design newer than plan provenance -> regenerate + reconcile
	RouteReady            = "ready"             // all three consistent -> go straight to build
)

// Classify decides the route from observed doc state. Staleness compares the design's updated
// timestamp against the plan's recorded provenance; RFC3339 when both parse, else exact-string.
//
// Archive-aware by construction: this decision never inspects per-task
// completion (ExecState.Tasks/Archived are out of scope here), only doc presence + provenance
// timestamps — a plan/execution pair with an archived milestone routes identically to one
// without, since archiving never removes design.md, plan.json, or execution.json themselves,
// only shrinks their task lists. No archived-done task can misread as un-started here.
func Classify(in ClassifyInput) ClassifyResult {
	r := ClassifyResult{}
	r.Design.Present = in.DesignPresent
	if in.DesignPresent {
		r.Design.Status, r.Design.Updated = ParseDesignFrontmatter(in.DesignText)
	}
	r.Plan.Present = in.PlanPresent
	r.Plan.ProvenanceDesignUpdated = in.PlanProvenanceDesignUpdated
	r.Execution.Present = in.ExecutionPresent
	r.Execution.MirrorPresent = in.MirrorPresent

	switch {
	case !r.Design.Present:
		r.Route = RouteInteractiveBuild
	case r.Design.Status == "stub":
		r.Route = RouteResumeDraft
	case !r.Plan.Present:
		r.Route = RouteDerive
	default:
		if planIsStale(r.Design.Updated, r.Plan.ProvenanceDesignUpdated) {
			r.Route = RouteReconcile
		} else if !r.Execution.Present {
			r.Route = RouteDerive
		} else {
			r.Route = RouteReady
		}
	}
	return r
}

// planIsStale reports whether the plan was derived from an older design than the current one.
func planIsStale(designUpdated, planProvenance string) bool {
	if planProvenance == "" {
		return false // no provenance recorded -> cannot prove stale; treat as fresh
	}
	dt, derr := time.Parse(time.RFC3339, designUpdated)
	pt, perr := time.Parse(time.RFC3339, planProvenance)
	if derr == nil && perr == nil {
		return pt.Before(dt)
	}
	return designUpdated != planProvenance // fallback: any difference means re-derive
}

// ---- escalation + scope classifiers ----
//
// Detect vs judge: these deterministic classifiers DETECT which situations warrant a specialist;
// the magistrate (Opus) JUDGES. The magistrate is expensive, so it fires ONLY on a CLOSED named
// trigger set with NO catch-all — an out-of-set condition can never route to it. Anything touching
// design success_criteria/scope is design-level and pauses + hands back to plan-with-team instead.

// EscalationTrigger is a member of the CLOSED named set the magistrate judges. Membership is by
// exact string; the set is exactly the four consts below and nothing else.
type EscalationTrigger string

const (
	TriggerSurpriseOverlap     EscalationTrigger = "surprise-overlap"      // octopus-merge conflict needing a 3-way union
	TriggerLocalDeltaReplan    EscalationTrigger = "local-delta-replan"    // tactical delta -> reconcile-exec, scope unchanged
	TriggerFailedTaskTriage    EscalationTrigger = "failed-task-triage"    // task failed after the fix-loop
	TriggerPhaseGateRegression EscalationTrigger = "phase-gate-regression" // full-suite FAIL at a boundary
)

// escalationTiers is the CLOSED trigger set AND each trigger's classifier-selected magistrate effort
// tier. This map is the SOLE gate to the magistrate: a condition routes to the magistrate iff it is
// a key here (exact-string lookup, no default branch). The two most delicate, highest-consequence
// triggers — the 3-way union merge (must preserve all work) and post-fix-loop triage — get xhigh;
// the tactical re-plan and gate regression get high.
var escalationTiers = map[EscalationTrigger]string{
	TriggerSurpriseOverlap:     "xhigh",
	TriggerFailedTaskTriage:    "xhigh",
	TriggerLocalDeltaReplan:    "high",
	TriggerPhaseGateRegression: "high",
}

// Escalation/scope routes. Distinct from the front-door Route* constants above.
const (
	RouteMagistrate   = "magistrate"     // named trigger -> Opus magistrate judges
	RoutePlanWithTeam = "plan-with-team" // design-level delta -> pause + hand back (never magistrate)
	RouteNoEscalation = "no-escalation"  // out-of-set condition -> orchestrator handles locally
	RouteInScope      = "in-scope"       // scope classifier: delta does NOT touch design success_criteria/scope
)

// EscalationTriggers returns the closed named set in a stable order (for help text and the
// magistrate agent's trigger list). Ordered by descending tier weight, then name.
func EscalationTriggers() []EscalationTrigger {
	return []EscalationTrigger{
		TriggerFailedTaskTriage,
		TriggerSurpriseOverlap,
		TriggerLocalDeltaReplan,
		TriggerPhaseGateRegression,
	}
}

// ScopeInput is the orchestrator's observation of whether a proposed mid-build delta touches the
// design's success_criteria or scope. The orchestrator sets these flags; the classifier is pure.
type ScopeInput struct {
	TouchesSuccessCriteria bool
	TouchesScope           bool
}

// ScopeResult is the deterministic scope route: any delta touching success_criteria/scope is
// design-level and must pause + hand back to plan-with-team; otherwise the delta is in-scope.
type ScopeResult struct {
	TouchesSuccessCriteria bool   `json:"touches_success_criteria"`
	TouchesScope           bool   `json:"touches_scope"`
	DesignLevel            bool   `json:"design_level"`
	Route                  string `json:"route"`
	Reason                 string `json:"reason"`
}

// ClassifyScope routes a mid-build delta by whether it touches the design's success_criteria or
// scope. Touching either is design-level -> pause + hand back to plan-with-team; neither -> in-scope.
func ClassifyScope(in ScopeInput) ScopeResult {
	r := ScopeResult{TouchesSuccessCriteria: in.TouchesSuccessCriteria, TouchesScope: in.TouchesScope}
	if in.TouchesSuccessCriteria || in.TouchesScope {
		r.DesignLevel = true
		r.Route = RoutePlanWithTeam
		r.Reason = "design-level delta (touches success_criteria/scope) -> pause + hand back to plan-with-team"
		return r
	}
	r.Route = RouteInScope
	r.Reason = "delta does not touch design success_criteria/scope -> in-scope"
	return r
}

// EscalationInput is a single observed condition the orchestrator wants classified: a condition
// label plus the same design-level flags ClassifyScope reads. The label is matched exactly against
// the closed trigger set; the flags gate the design-level override.
type EscalationInput struct {
	Condition              string
	TouchesSuccessCriteria bool
	TouchesScope           bool
}

// EscalationResult is the deterministic escalation route. Trigger/Tier are set only for the
// magistrate route.
type EscalationResult struct {
	Condition   string            `json:"condition"`
	DesignLevel bool              `json:"design_level"`
	Route       string            `json:"route"`
	Trigger     EscalationTrigger `json:"trigger,omitempty"`
	Tier        string            `json:"tier,omitempty"`
	Reason      string            `json:"reason"`
}

// ClassifyEscalation routes an observed condition to exactly one of: magistrate (named trigger),
// plan-with-team (design-level), or no-escalation (out-of-set). The magistrate is reachable ONLY
// through the closed-set map lookup — there is no default/catch-all path to it.
func ClassifyEscalation(in EscalationInput) EscalationResult {
	r := EscalationResult{Condition: in.Condition}

	// Design-level override runs FIRST and is absolute: a delta touching success_criteria/scope
	// pauses + hands back to plan-with-team and can NEVER reach the magistrate — even if the
	// condition string also names a trigger (a scope-touching delta is by definition not a
	// scope-unchanged local-delta-replan).
	if scope := ClassifyScope(ScopeInput{TouchesSuccessCriteria: in.TouchesSuccessCriteria, TouchesScope: in.TouchesScope}); scope.DesignLevel {
		r.DesignLevel = true
		r.Route = RoutePlanWithTeam
		r.Reason = scope.Reason
		return r
	}

	// Closed-set membership is the SOLE gate to the magistrate: an exact-string lookup, never a
	// default branch. Any condition not in escalationTiers falls through to no-escalation below.
	if tier, ok := escalationTiers[EscalationTrigger(in.Condition)]; ok {
		r.Route = RouteMagistrate
		r.Trigger = EscalationTrigger(in.Condition)
		r.Tier = tier
		r.Reason = "named escalation trigger -> fire magistrate"
		return r
	}

	r.Route = RouteNoEscalation
	r.Reason = "out-of-set condition -> orchestrator handles locally; no magistrate"
	return r
}

// ---- feedback criticality gate ----
//
// The magistrate-consumes-feedback route. Parallels ClassifyEscalation's magistrate-vs-local split:
// an above-threshold entry becomes ranked amendment input the magistrate JUDGES (reconcile-exec
// restacking); a sub-threshold entry is DEFERRED deterministically to the standing feedback-review
// milestone — never magistrate-judged. GateFeedback (feedback.go) applies this route across a
// ranked register; the two-file split keeps the route decision here (classify) and the register
// mechanics there (feedback), matching the detect-vs-judge convention above.

// Feedback criticality routes. Distinct from the escalation Route* constants.
const (
	RouteFeedbackAmend  = "feedback-amend"  // criticality >= threshold -> magistrate reconcile-exec amendment input
	RouteFeedbackReview = "feedback-review" // criticality < threshold  -> standing feedback-review milestone (deterministic, not judged)
)

// ClassifyFeedbackCriticality routes one entry's criticality against the amend-now threshold. The
// threshold is an INCLUSIVE floor and a parameter, never hardcoded: criticality >= threshold amends
// now, strictly below defers. Total and deterministic — every criticality lands in exactly one
// route, and the boundary case (criticality == threshold) routes to amend deterministically.
func ClassifyFeedbackCriticality(criticality, threshold int) string {
	if criticality >= threshold {
		return RouteFeedbackAmend
	}
	return RouteFeedbackReview
}
