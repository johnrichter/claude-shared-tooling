// Package bh is the build-with-team deterministic helper API: the exact mechanics the
// orchestrator skill must not do as LLM guesswork — plan validation, content-addressed
// reconciliation, tier checks, front-door routing, execution-state transitions, and
// rendering the human-readable plan.md / execution.md mirrors.
//
// This package is pure: every function takes parsed values and returns values or errors.
// It performs no file IO and never calls os.Exit — the CLI (package main) owns IO and
// process control. That split keeps the whole API unit-testable.
package bh

import (
	"bytes"
	"encoding/json"
)

// ---- plan.json (immutable build spec) ----

// Plan is the canonical build plan emitted by product-architect. Status is never tracked
// here — live per-task status lives in ExecState. Provenance is injected by the skill after
// the architect returns (the architect emits only the spec fields).
type Plan struct {
	Goal            string      `json:"goal"`
	SuccessCriteria []string    `json:"success_criteria"`
	Assumptions     []string    `json:"assumptions,omitempty"`
	Tradeoffs       []Tradeoff  `json:"tradeoffs,omitempty"`
	Milestones      []Milestone `json:"milestones"`
	Risks           []Risk      `json:"risks,omitempty"`
	OpenQuestions   []string    `json:"open_questions,omitempty"`
	Provenance      *Provenance `json:"provenance,omitempty"`
}

// Tradeoff is one consequential design fork the architect resolved.
type Tradeoff struct {
	Decision       string   `json:"decision"`
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation"`
	Why            string   `json:"why"`
}

// Risk maps to a build-time hazard; ForcesPause feeds the critical-surface escape hatch.
type Risk struct {
	Risk        string `json:"risk"`
	Mitigation  string `json:"mitigation,omitempty"`
	ForcesPause bool   `json:"forces_pause,omitempty"`
}

// Milestone -> Phase -> Task: the three-level decomposition. IDs are hierarchical
// (M1 / M1.P1 / M1.P1.T1) and validated for nesting consistency.
type Milestone struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Phases []Phase `json:"phases"`
}

type Phase struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
}

// Task is the smallest unit of build work. Model/Effort are the implementer tier; the test
// and review stages run on fixed tiers chosen by the engine. Kind routes the implementer role:
// code → software-engineer + adversarial test stage; docs → technical-writer + accuracy/language
// spot-check (no authored test suite). Absent/empty Kind defaults to code. OrchestratorOnly marks
// a task (e.g. an O-measurement/verdict gate, design.md SCe) that must run inline in the
// orchestrator, never as a subagent dispatch — next/batch (execution.go, batch.go) structurally
// refuse to hand one to the build-engine.
type Task struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Summary          string             `json:"summary"`
	Deliverable      string             `json:"deliverable"`
	Kind             DeliverableKind    `json:"deliverable_kind,omitempty"`
	Model            Model              `json:"model"`
	Effort           Effort             `json:"effort"`
	Thinking         string             `json:"thinking,omitempty"`
	TestStrategy     string             `json:"test_strategy"`
	Deps             []string           `json:"deps,omitempty"`
	Acceptance       []string           `json:"acceptance"`
	FileSurface      []FileSurfaceEntry `json:"file_surface,omitempty"`
	OrchestratorOnly bool               `json:"orchestrator_only,omitempty"`
}

// FileSurfaceKind is the match semantics for one file_surface entry. The set is closed; an
// empty Kind resolves to FSFile (Resolve) — a bare path names one file by default.
type FileSurfaceKind string

const (
	FSFile FileSurfaceKind = "file" // Path names exactly one file (must exist, be a non-directory)
	FSGlob FileSurfaceKind = "glob" // Path is a glob pattern — must match >=1 file
	FSDir  FileSurfaceKind = "dir"  // Path names a directory — must exist and be non-empty
)

func (k FileSurfaceKind) Known() bool {
	switch k {
	case FSFile, FSGlob, FSDir:
		return true
	}
	return false
}

// Resolve maps the empty/unset kind to the file default.
func (k FileSurfaceKind) Resolve() FileSurfaceKind {
	if k == "" {
		return FSFile
	}
	return k
}

// FileSurfaceEntry is one file/glob/dir this task may read or write. VerifyFileSurface
// (surface.go) applies the pinned match semantics before a task is marked done: glob requires
// >=1 match, dir requires non-empty, and Required (any kind) additionally demands the matched
// content be non-trivial (non-zero byte size) — catching a task that creates an empty
// placeholder to fake completion. batch.go's overlap predicate reads only Path (kind-agnostic).
type FileSurfaceEntry struct {
	Path     string          `json:"path"`
	Required bool            `json:"required,omitempty"`
	Kind     FileSurfaceKind `json:"kind,omitempty"`
}

// UnmarshalJSON accepts either the typed object form {path,required,kind} or a bare JSON string
// (the legacy file_surface shape). A bare string maps to {Path:s} with Required=false and
// an empty Kind — which Resolve() treats as the file default — so every existing plain-string
// plan.json stays parseable with no migration and no data loss, while new plans emit the typed
// object form. batch.go's overlap predicate and VerifyFileSurface read the resolved struct either
// way, so both shapes behave identically downstream. Marshaling is unchanged: entries always
// re-serialize as the canonical object form.
func (e *FileSurfaceEntry) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*e = FileSurfaceEntry{Path: s}
		return nil
	}
	// Object form: the alias sheds the method set so json.Unmarshal does not recurse into this method.
	type alias FileSurfaceEntry
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*e = FileSurfaceEntry(a)
	return nil
}

// DeliverableKind selects the per-task build path the engine runs. The set is closed.
type DeliverableKind string

const (
	KindCode DeliverableKind = "code" // software-engineer implements; test-engineer authors/runs tests
	KindDocs DeliverableKind = "docs" // technical-writer drafts; spot-check for accuracy + language
)

// Known reports whether k is a recognized kind. Empty is allowed by the schema and means code
// (Resolve applies that default); Known is for explicit values only.
func (k DeliverableKind) Known() bool {
	switch k {
	case KindCode, KindDocs:
		return true
	}
	return false
}

// Resolve maps the empty/unset kind to the code default.
func (k DeliverableKind) Resolve() DeliverableKind {
	if k == "" {
		return KindCode
	}
	return k
}

// Provenance records the upstream timestamps a derivative was built from, so staleness is
// detectable. Optional in plan.json; always present in execution.json.
type Provenance struct {
	DesignUpdated string `json:"design_updated"`
	PlanUpdated   string `json:"plan_updated"`
	DerivedAt     string `json:"derived_at,omitempty"`
}

// ---- enums (the value sets a static type system lets us enforce) ----

// Model is a pinned full model ID. The selectable set is closed; update it here and in
// plan-schema.json together.
type Model string

const (
	ModelOpus48   Model = "claude-opus-4-8"
	ModelOpus47   Model = "claude-opus-4-7"
	ModelOpus46   Model = "claude-opus-4-6"
	ModelSonnet5  Model = "claude-sonnet-5"
	ModelSonnet46 Model = "claude-sonnet-4-6" // legacy
	ModelHaiku45  Model = "claude-haiku-4-5"
	ModelFable5   Model = "claude-fable-5"
	ModelInherit  Model = "inherit"
)

func (m Model) Known() bool {
	switch m {
	case ModelOpus48, ModelOpus47, ModelOpus46, ModelSonnet5, ModelSonnet46, ModelHaiku45, ModelFable5, ModelInherit:
		return true
	}
	return false
}

// Effort is a reasoning-effort tier; availability is model-dependent (see Tier checks).
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

func (e Effort) Known() bool {
	switch e {
	case EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}

// ---- pause events ----

// PauseReason categorizes why a build paused. The set is closed so the mechanical-slip count
// is machine-derivable instead of grepped from free-text: git|state|merge are tooling-forced
// (mechanical) pauses; design-level|approval|budget|signing are operator-facing gates and are
// never counted as mechanical slips (see Mechanical).
type PauseReason string

const (
	PauseGit         PauseReason = "git"
	PauseState       PauseReason = "state"
	PauseMerge       PauseReason = "merge"
	PauseDesignLevel PauseReason = "design-level"
	PauseApproval    PauseReason = "approval"
	PauseBudget      PauseReason = "budget"
	PauseSigning     PauseReason = "signing"
)

func (r PauseReason) Known() bool {
	switch r {
	case PauseGit, PauseState, PauseMerge, PauseDesignLevel, PauseApproval, PauseBudget, PauseSigning:
		return true
	}
	return false
}

// Mechanical reports whether r is one of the tooling-forced pause reasons (git|state|merge) the
// mechanical-slip count is derived from. design-level/approval/budget/signing are operator-facing
// gates, never mechanical slips.
func (r PauseReason) Mechanical() bool {
	switch r {
	case PauseGit, PauseState, PauseMerge:
		return true
	}
	return false
}

// PauseEvent is one structured pause occurrence. TaskID is set when the pause is tied to
// a specific task; empty for a plan-level pause. Append-only — RecordPauseEvent is the sole writer.
type PauseEvent struct {
	ReasonEnum PauseReason `json:"reason_enum"`
	At         string      `json:"at"`
	TaskID     string      `json:"task_id,omitempty"`
}

// EscalationEvent is one structured magistrate-firing occurrence: a named trigger from
// classify.go's closed EscalationTrigger set (never an out-of-set condition — RecordEscalationEvent
// rejects those) fired the magistrate at the given tier via the given route. TaskID is set when the
// firing is tied to a specific task; empty for a plan-level firing. Append-only —
// RecordEscalationEvent is the sole writer. Counting these events is the equal-magistrate-firing
// void-check input (MagistrateFiringCount): the two runs of a model-tier pair must fire the
// magistrate an EQUAL number of times, or the pair is voided as workload-divergent rather than
// tier-comparable.
type EscalationEvent struct {
	Trigger EscalationTrigger `json:"trigger"`
	Tier    string            `json:"tier"`
	Route   string            `json:"route"`
	At      string            `json:"at"`
	TaskID  string            `json:"task_id,omitempty"`
}

// ---- execution.json (canonical live state; execution.md is its rendered mirror) ----

// Status is the per-task lifecycle state. The set is closed so the renderer's emoji mapping
// and the next-task logic are exhaustive.
type Status string

const (
	StatusNotStarted Status = "not-started"
	StatusInProgress Status = "in-progress"
	StatusBlocked    Status = "blocked"
	StatusFailed     Status = "failed"
	StatusDone       Status = "done"
	StatusSuperseded Status = "superseded"
)

func (s Status) Known() bool {
	switch s {
	case StatusNotStarted, StatusInProgress, StatusBlocked, StatusFailed, StatusDone, StatusSuperseded:
		return true
	}
	return false
}

// Terminal reports whether a task in this status is finished for scheduling purposes.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusSuperseded }

// Emoji is the mirror glyph (local to the execution doc; distinct from the workspace legend).
func (s Status) Emoji() string {
	switch s {
	case StatusNotStarted:
		return "🟢"
	case StatusInProgress:
		return "🟡"
	case StatusBlocked:
		return "⛔"
	case StatusFailed:
		return "🔴"
	case StatusDone:
		return "✅"
	case StatusSuperseded:
		return "⊘"
	}
	return "🟢"
}

// LegacyExecSchemaVersion is the implicit version of every execution.json written before this
// field existed (no schema_version key at all). CurrentExecSchemaVersion is the version InitExec
// stamps today; bump it and extend MigrateExec whenever a future change needs field-level upgrade
// logic. Both are exported so main.go's future-version guard can report exact numbers.
//
// v3 adds ExecState.Archived, the archive op's compact tombstone index.
const (
	LegacyExecSchemaVersion  = 1
	CurrentExecSchemaVersion = 3
)

// ExecState is the canonical, mutable build state. It is produced by InitExec and mutated
// only through RecordTask / ReconcileExec, then rendered to execution.md by RenderExecution.
// SchemaVersion is absent (zero value) on any execution.json written before it existed; MigrateExec
// treats that as LegacyExecSchemaVersion and upgrades in place — see execution.go.
type ExecState struct {
	Schema        string     `json:"schema"`
	SchemaVersion int        `json:"schema_version,omitempty"`
	Project       string     `json:"project"`
	Name          string     `json:"name"`
	Topic         string     `json:"topic"`
	Goal          string     `json:"goal"`
	Provenance    Provenance `json:"provenance"`
	Started       string     `json:"started"`
	Updated       string     `json:"updated"`
	RunConfig     RunConfig  `json:"run_config"`
	Tasks         []ExecTask `json:"tasks"`
	// Archived is the compact tombstone index for tasks the explicit `archive` op has moved out
	// of Tasks into archive.json — exactly the fields the resume hot path (dependency-done
	// resolution, whole-project cost recompute) needs in-band, so archive-awareness costs no
	// extra file read. Full fidelity for these tasks lives only in archive.json. Absent/nil on
	// any execution.json never archived.
	Archived []Tombstone `json:"archived,omitempty"`
	Log      []string    `json:"log"`
	// PauseEvents is the structured pause-event log: every pause records
	// {reason_enum,at,task_id?} here, so the mechanical-slip count (git|state|merge,
	// MechanicalSlipCount) is machine-derivable instead of grepped from free-text Log lines.
	// Append-only; absent/nil on any execution.json with no pause events yet.
	PauseEvents []PauseEvent `json:"pause_events,omitempty"`
	// EscalationEvents is the structured magistrate-firing log: every magistrate firing
	// records {trigger,tier,route,at,task_id?} here, so the equal-magistrate-firing void-check
	// (MagistrateFiringCount) is machine-derivable instead of un-countable. Append-only; absent/nil
	// on any execution.json with no escalation events yet.
	EscalationEvents []EscalationEvent `json:"escalation_events,omitempty"`
}

// RunConfig is the resumable run configuration; it survives pause-gate turns.
type RunConfig struct {
	PauseMode        string      `json:"pause_mode"`           // task | phase | milestone | none
	Budget           string      `json:"budget"`               // "unlimited" | "$<amount>"
	BudgetCeilingUSD *float64    `json:"budget_ceiling_usd"`   // nil == unlimited
	SpentUSD         float64     `json:"spent_usd"`            // recomputed from per-task costs; never hand-summed
	TokensOut        int64       `json:"tokens_out"`           // cumulative MEASURED output tokens; recomputed from per-task tokens_out
	Usage            *Usage      `json:"usage,omitempty"`      // Σ per-task Usage across Tasks+Archived (all four classes); recomputed in recomputeTotals, never hand-summed. Distinct from TrueUsage below (transcript-derived, independent source) — see recomputeTotals.
	TrueUsage        *Usage      `json:"true_usage,omitempty"` // transcript-derived true input+output+cache totals (whole session, incl. subagents)
	Accounting       *Accounting `json:"accounting,omitempty"` // per-model true-cost accounting + per-file watermarks (resume-safe; whole session incl. subagents)
	Rates            string      `json:"rates"`                // "list-price" | "negotiated"
	Override         string      `json:"override,omitempty"`
	LastRunID        string      `json:"last_run_id,omitempty"` // SAME-SESSION fast-resume only
}

// Usage is a transcript-derived true token total for the session, including all subagent turns.
// Unlike the per-task SpentUSD/TokensOut (which the engine measures as OUTPUT only), this counts
// input + cache + output by summing each turn's `usage` object in the session transcript JSONL.
// Source format is internal to Claude Code and may change between releases — treat as best-effort.
type Usage struct {
	InputTokens         int64  `json:"input_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	TotalTokens         int64  `json:"total_tokens"` // input + cache_creation + cache_read + output
	Turns               int64  `json:"turns"`        // assistant turns counted
	At                  string `json:"at,omitempty"` // when this snapshot was taken
}

// ExecTask is one task's live row. cost_usd is OUTPUT-token cost (a lower bound); tokens_out is
// the engine's MEASURED output-token count for the task (the same basis as cost_usd).
type ExecTask struct {
	ID        string          `json:"id"`
	Summary   string          `json:"summary"`
	Kind      DeliverableKind `json:"deliverable_kind,omitempty"` // code (default) | docs
	Model     Model           `json:"model"`
	Effort    Effort          `json:"effort"`
	Status    Status          `json:"status"`
	Test      string          `json:"test,omitempty"`   // PASS | FAIL | ""
	Review    string          `json:"review,omitempty"` // ACCEPT | FIX-APPLIED | RETURN | ""
	Commit    string          `json:"commit,omitempty"`
	CostUSD   float64         `json:"cost_usd"`
	TokensOut int64           `json:"tokens_out"`      // measured output tokens for this task (lower-bound basis, == cost_usd) — kept for backward compatibility
	Usage     *Usage          `json:"usage,omitempty"` // this task's four measured token classes (input, cache_creation aka cache-write, cache_read, output); nil until recorded. Output-only TokensOut above is a lower-bound basis — cache_read commonly DOMINATES a task's true token count, so Usage is the correct accounting basis, not TokensOut.
	Updated   string          `json:"updated"`
	Notes     string          `json:"notes,omitempty"`
}

const ExecSchema = "execution-state/v1"
