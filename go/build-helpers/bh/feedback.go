package bh

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---- feedback.json (canonical register) / feedback.md (rendered mirror) — SC13 ----
//
// A per-project feedback
// register, populated by a session-finish wrap-up step, queried by the magistrate for
// threshold-gated plan amendment. feedback.json is canonical; feedback.md is a
// derived mirror that `feedback add` re-renders in the same write (main.go's runFeedbackAdd) so
// the two never diverge — there is no path that writes one without the other.

// FeedbackSchema marks feedback.json's shape, matching the ExecSchema/ArchiveSchema convention
// (a "schema" discriminator key) for any future doc-shape sniffing.
const FeedbackSchema = "feedback-register/v1"

// FeedbackEntry is one register row. ID and Criticality are always derived by AddFeedback, never
// caller-supplied — they are output-only fields on this type. Title is the short human name SC15
// requires alongside ID (every readout prints "<id> — <title>").
type FeedbackEntry struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	SourceTaskID     string `json:"source_task_id,omitempty"`
	Feedback         string `json:"feedback"`
	ProposedSolution string `json:"proposed_solution,omitempty"`
	WhyItMatters     string `json:"why_it_matters,omitempty"`
	Impact           int    `json:"impact"`
	Urgency          int    `json:"urgency"`
	Criticality      int    `json:"criticality"`
	Added            string `json:"added,omitempty"`
}

// FeedbackRegister is the canonical feedback.json document: an append-only, project-scoped list
// of FeedbackEntry. Entries are never reordered or removed by `add` — M13.P2.T2's threshold gate
// reads this list, ranks it, and routes each entry exactly once.
type FeedbackRegister struct {
	Schema  string          `json:"schema"`
	Project string          `json:"project,omitempty"`
	Entries []FeedbackEntry `json:"entries"`
}

// FeedbackInput is the caller-supplied subset of a FeedbackEntry's fields. ID and Criticality are
// deliberately absent — AddFeedback is the only place either is produced, so a caller can never
// forge a criticality score by supplying it directly.
type FeedbackInput struct {
	Title            string
	SourceTaskID     string
	Feedback         string
	ProposedSolution string
	WhyItMatters     string
	Impact           int
	Urgency          int
}

// ValidScore reports whether a 1-5 impact/urgency score is in range.
func ValidScore(v int) bool { return v >= 1 && v <= 5 }

// Criticality derives the single ranking score M13.P2.T2's threshold gate acts on. Documented
// rule: criticality = impact * urgency (risk-matrix convention — a fix must score high on BOTH
// axes to rank as truly critical; a bounded additive rule would let a lopsided high/low pair tie
// a balanced medium/medium pair, losing exactly the signal the gate needs). Range: 1-25.
func Criticality(impact, urgency int) int { return impact * urgency }

// AddFeedback validates in and appends one new entry to reg, returning the updated register.
// Invalid impact/urgency (outside 1-5) or an empty title/feedback body is rejected with no
// mutation. The new entry's id is FB<n>, n = len(reg.Entries)+1 — stable and monotonic within a
// project (register-length-derived, not content-derived, so it is never reused by a later add
// even if entries are re-rendered in a different order).
func AddFeedback(reg FeedbackRegister, in FeedbackInput, at string) (FeedbackRegister, error) {
	if strings.TrimSpace(in.Title) == "" {
		return reg, fmt.Errorf("feedback add: title is required")
	}
	if strings.TrimSpace(in.Feedback) == "" {
		return reg, fmt.Errorf("feedback add: feedback is required")
	}
	if !ValidScore(in.Impact) {
		return reg, fmt.Errorf("feedback add: impact %d out of range (must be 1-5)", in.Impact)
	}
	if !ValidScore(in.Urgency) {
		return reg, fmt.Errorf("feedback add: urgency %d out of range (must be 1-5)", in.Urgency)
	}
	if reg.Schema == "" {
		reg.Schema = FeedbackSchema
	}
	entry := FeedbackEntry{
		ID:               fmt.Sprintf("FB%d", len(reg.Entries)+1),
		Title:            in.Title,
		SourceTaskID:     in.SourceTaskID,
		Feedback:         in.Feedback,
		ProposedSolution: in.ProposedSolution,
		WhyItMatters:     in.WhyItMatters,
		Impact:           in.Impact,
		Urgency:          in.Urgency,
		Criticality:      Criticality(in.Impact, in.Urgency),
		Added:            at,
	}
	out := reg
	out.Entries = append(append([]FeedbackEntry{}, reg.Entries...), entry)
	return out, nil
}

// FeedbackFilter is the composable predicate set `feedback list` (M13.P1.T2) applies. Every
// non-zero field narrows the result; all supplied fields AND together — the zero value (no
// SourceTaskID, MinImpact/MinUrgency 0) matches every entry, giving list-all-ranked for free.
type FeedbackFilter struct {
	SourceTaskID string
	MinImpact    int
	MinUrgency   int
}

// matches reports whether e satisfies every filter set on f. MinImpact/MinUrgency 0 imposes no
// floor because ValidScore requires entries to be >=1 already, so a 0 threshold never excludes a
// real entry.
func (f FeedbackFilter) matches(e FeedbackEntry) bool {
	if f.SourceTaskID != "" && e.SourceTaskID != f.SourceTaskID {
		return false
	}
	if e.Impact < f.MinImpact {
		return false
	}
	if e.Urgency < f.MinUrgency {
		return false
	}
	return true
}

// ListFeedback returns reg's entries matching f (composed AND), ranked by criticality descending
// — the order M13.P2.T2's threshold gate and any operator readout (SC15: "<id> — <title>") rely
// on. Ties break by id ascending: ids are monotonic-by-add-order (FB<n>), so this also recovers
// append order among equal-criticality entries, deterministically, regardless of Entries' slice
// order. Never mutates reg.
func ListFeedback(reg FeedbackRegister, f FeedbackFilter) []FeedbackEntry {
	var out []FeedbackEntry
	for _, e := range reg.Entries {
		if f.matches(e) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Criticality != out[j].Criticality {
			return out[i].Criticality > out[j].Criticality
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RenderFeedback renders feedback.md, the faithful mirror of feedback.json: a summary table
// (register order — append-only, so also chronological) plus one detail section per entry. Pure
// string-out, so `feedback add`'s writer (main.go) can regenerate it deterministically from the
// same register it just wrote to feedback.json, in the same call, keeping the two in lockstep.
func RenderFeedback(reg FeedbackRegister) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	w("# Feedback register")
	w("")
	if len(reg.Entries) == 0 {
		w("_(no feedback entries yet)_")
		return b.String()
	}
	w("| ID | Title | Source task | Impact | Urgency | Criticality |")
	w("| --- | --- | --- | :--: | :--: | :--: |")
	for _, e := range reg.Entries {
		src := e.SourceTaskID
		if src == "" {
			src = "—"
		}
		w(fmt.Sprintf("| %s | %s | %s | %d | %d | %d |", e.ID, cell(e.Title), src, e.Impact, e.Urgency, e.Criticality))
	}
	w("")
	for _, e := range reg.Entries {
		w("## " + e.ID + " — " + line(e.Title))
		w("")
		w("- feedback: " + line(e.Feedback))
		if e.ProposedSolution != "" {
			w("- proposed solution: " + line(e.ProposedSolution))
		}
		if e.WhyItMatters != "" {
			w("- why it matters: " + line(e.WhyItMatters))
		}
		w("")
	}
	return b.String()
}

// ---- criticality-threshold gate (M13.P2.T2): magistrate-consumes-feedback logic ----
//
// The gate partitions the ranked register into two buckets against a configurable inclusive
// criticality threshold (ClassifyFeedbackCriticality, classify.go): amend-now (>= threshold) is
// emitted as ranked reconcile-exec amendment input the magistrate consumes; feedback-review (<
// threshold) is realized as schema-valid tasks appended to a standing feedback-review milestone.
// The partition is TOTAL, LOSSLESS, and EXACTLY-ONCE: ListFeedback with the zero filter yields
// every register entry exactly once, and each is routed to precisely one bucket — none dropped,
// none double-routed. The whole gate is a pure function of (plan, register, threshold), so a
// re-run on unchanged inputs is idempotent by construction.

// Standing feedback-review milestone identity. M999 is a sentinel id, deliberately far above any
// project's sequential milestone numbering so the appended milestone never collides with a real
// one. This is the source of truth the docs stub (M13.P2.T4) is kept consistent with; every
// sub-threshold entry lands here as a task rather than being lost.
const (
	FeedbackReviewMilestoneID   = "M999"
	FeedbackReviewMilestoneName = "Feedback review"
	FeedbackReviewPhaseID       = "M999.P1"
	FeedbackReviewPhaseName     = "Deferred feedback"
)

// FeedbackGate is the deterministic threshold partition of a ranked register. AmendNow and Deferred
// are each in criticality-descending, id-ascending rank order (ListFeedback's order), and together
// contain every register entry exactly once.
type FeedbackGate struct {
	Threshold int             `json:"threshold"`
	AmendNow  []FeedbackEntry `json:"amend_now"` // criticality >= threshold — ranked reconcile-exec amendment input
	Deferred  []FeedbackEntry `json:"deferred"`  // criticality <  threshold — realized as feedback-review tasks
}

// GateFeedback ranks the full register and partitions it by threshold via
// ClassifyFeedbackCriticality. Total + exactly-once: the zero-filter ListFeedback returns every
// entry once, and each routes to exactly one bucket. Threshold is a parameter — never hardcoded.
func GateFeedback(reg FeedbackRegister, threshold int) FeedbackGate {
	g := FeedbackGate{Threshold: threshold, AmendNow: []FeedbackEntry{}, Deferred: []FeedbackEntry{}}
	for _, e := range ListFeedback(reg, FeedbackFilter{}) {
		if ClassifyFeedbackCriticality(e.Criticality, threshold) == RouteFeedbackAmend {
			g.AmendNow = append(g.AmendNow, e)
		} else {
			g.Deferred = append(g.Deferred, e)
		}
	}
	return g
}

// feedbackTaskNum extracts the numeric suffix of a feedback-review task id (M999.P1.T<n>), used to
// order the milestone's tasks deterministically. A malformed id sorts as 0.
func feedbackTaskNum(taskID string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(taskID, FeedbackReviewPhaseID+".T"))
	return n
}

// feedbackReviewTaskID maps a feedback id (FB<n>) to its feedback-review task id (M999.P1.T<n>) —
// a stable 1:1 mapping that makes the generated task traceable to its source entry and keeps
// re-runs idempotent regardless of rank order. A non-FB<n> id falls back to a hash-free literal.
func feedbackReviewTaskID(entryID string) string {
	n := strings.TrimPrefix(entryID, "FB")
	if n == "" || n == entryID {
		n = "0"
	}
	return FeedbackReviewPhaseID + ".T" + n
}

// FeedbackReviewTask converts one sub-threshold entry into a schema-valid Task: id + name +
// summary + deliverable + test_strategy + acceptance (>=1), all non-empty so ValidatePlanBytes
// accepts it and reconcile-exec can restack it. Model/effort are inherit/medium — a neutral
// placeholder tier the operator or magistrate refines when the deferred item is picked up.
func FeedbackReviewTask(e FeedbackEntry) Task {
	deliverable := strings.TrimSpace(e.ProposedSolution)
	if deliverable == "" {
		deliverable = "Triage and address the deferred feedback: " + e.Feedback
	}
	accept := strings.TrimSpace(e.WhyItMatters)
	if accept == "" {
		accept = "The deferred feedback is resolved or explicitly re-deferred with a recorded rationale."
	}
	return Task{
		ID:           feedbackReviewTaskID(e.ID),
		Name:         e.Title,
		Summary:      "Deferred feedback (" + e.ID + "): " + e.Feedback,
		Deliverable:  deliverable,
		Model:        ModelInherit,
		Effort:       EffortMedium,
		TestStrategy: "Reviewer confirms the deferred feedback is addressed or explicitly re-deferred with a recorded rationale.",
		Acceptance:   []string{accept},
	}
}

// BuildFeedbackReviewMilestone assembles the standing feedback-review milestone from the deferred
// (sub-threshold) entries: one phase, one task per entry, tasks ordered by id ascending for a
// stable, deterministic layout. Returns false when deferred is empty — an empty milestone/phase is
// schema-invalid (both require >=1 child), so callers must omit it entirely rather than emit a hollow one.
func BuildFeedbackReviewMilestone(deferred []FeedbackEntry) (Milestone, bool) {
	if len(deferred) == 0 {
		return Milestone{}, false
	}
	tasks := make([]Task, 0, len(deferred))
	for _, e := range deferred {
		tasks = append(tasks, FeedbackReviewTask(e))
	}
	sort.SliceStable(tasks, func(i, j int) bool { return feedbackTaskNum(tasks[i].ID) < feedbackTaskNum(tasks[j].ID) })
	return Milestone{
		ID:     FeedbackReviewMilestoneID,
		Name:   FeedbackReviewMilestoneName,
		Phases: []Phase{{ID: FeedbackReviewPhaseID, Name: FeedbackReviewPhaseName, Tasks: tasks}},
	}, true
}

// ApplyFeedbackReview returns p with its feedback-review milestone regenerated wholesale from the
// deferred entries — this is the new-plan input to reconcile-exec for the sub-threshold side. Any
// existing feedback-review milestone is dropped first, so re-applying the same deferred set is
// idempotent (ApplyFeedbackReview(ApplyFeedbackReview(p,d),d) == ApplyFeedbackReview(p,d)). When
// deferred is empty the milestone is removed entirely rather than left hollow (schema-invalid).
// Never mutates p or p.Milestones' backing array.
func ApplyFeedbackReview(p Plan, deferred []FeedbackEntry) Plan {
	out := p
	kept := make([]Milestone, 0, len(p.Milestones)+1)
	for _, m := range p.Milestones {
		if m.ID != FeedbackReviewMilestoneID {
			kept = append(kept, m)
		}
	}
	if m, ok := BuildFeedbackReviewMilestone(deferred); ok {
		kept = append(kept, m)
	}
	out.Milestones = kept
	return out
}

// FeedbackGateResult is the full gate output the CLI emits and the magistrate consumes: the ranked
// amend-now amendment input, the deferred set, and the plan with the feedback-review milestone
// applied (the new-plan.json for reconcile-exec's sub-threshold restacking).
type FeedbackGateResult struct {
	Threshold int             `json:"threshold"`
	AmendNow  []FeedbackEntry `json:"amend_now"`
	Deferred  []FeedbackEntry `json:"deferred"`
	Plan      Plan            `json:"plan"`
}

// GatePlanFeedback is the single entry point: partition reg by threshold, emit the ranked amend-now
// set, and apply the deferred set to p as the feedback-review milestone. Pure and idempotent.
func GatePlanFeedback(p Plan, reg FeedbackRegister, threshold int) FeedbackGateResult {
	g := GateFeedback(reg, threshold)
	return FeedbackGateResult{
		Threshold: threshold,
		AmendNow:  g.AmendNow,
		Deferred:  g.Deferred,
		Plan:      ApplyFeedbackReview(p, g.Deferred),
	}
}
