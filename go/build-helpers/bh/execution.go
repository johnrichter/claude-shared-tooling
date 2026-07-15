package bh

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---- budget ----

// ParseBudget normalizes a budget string into a display label and an optional dollar ceiling.
// "unlimited" / "" -> (label "unlimited", ceiling nil). "$5" or "5" -> ("$5.00", &5).
func ParseBudget(b string) (label string, ceiling *float64, err error) {
	b = strings.TrimSpace(b)
	if b == "" || b == "unlimited" {
		return "unlimited", nil, nil
	}
	n, e := strconv.ParseFloat(strings.TrimPrefix(b, "$"), 64)
	if e != nil {
		return "", nil, fmt.Errorf("budget must be 'unlimited' or a dollar amount, got %q", b)
	}
	v := n
	return fmt.Sprintf("$%.2f", n), &v, nil
}

// ---- init ----

// InitExecOptions are the run-config inputs gathered up front by the skill.
type InitExecOptions struct {
	Slug          string
	Name          string // defaults to "<slug> — Execution"
	Topic         string // defaults to "tooling"
	DesignUpdated string
	PlanUpdated   string
	Pause         string // defaults to "phase"
	Budget        string // defaults to "unlimited"
	Rates         string // defaults to "list-price"
	Override      string
	At            string // now (ISO); caller supplies for determinism
}

// InitExec builds the canonical execution.json for a fresh plan: one not-started row per task,
// run config seeded, provenance filled. The skill writes the result and renders execution.md.
func InitExec(p Plan, opt InitExecOptions) (ExecState, error) {
	if strings.TrimSpace(opt.Slug) == "" {
		return ExecState{}, fmt.Errorf("init-exec requires --slug")
	}
	label, ceiling, err := ParseBudget(opt.Budget)
	if err != nil {
		return ExecState{}, err
	}
	name := cmp.Or(opt.Name, opt.Slug+" — Execution")
	topic := cmp.Or(opt.Topic, "tooling")
	pause := cmp.Or(opt.Pause, "phase")
	rates := cmp.Or(opt.Rates, "list-price")
	rows := []ExecTask{}
	for _, r := range WalkTasks(p) {
		rows = append(rows, ExecTask{
			ID: r.Task.ID, Summary: r.Task.Summary, Kind: r.Task.Kind.Resolve(), Model: r.Task.Model, Effort: r.Task.Effort,
			Status: StatusNotStarted, CostUSD: 0, TokensOut: 0, Updated: opt.At,
		})
	}
	return ExecState{
		Schema: ExecSchema, SchemaVersion: CurrentExecSchemaVersion, Project: opt.Slug, Name: name, Topic: topic, Goal: p.Goal,
		Provenance: Provenance{DesignUpdated: opt.DesignUpdated, PlanUpdated: opt.PlanUpdated, DerivedAt: opt.At},
		Started:    opt.At, Updated: opt.At,
		RunConfig: RunConfig{
			PauseMode: pause, Budget: label, BudgetCeilingUSD: ceiling, SpentUSD: 0,
			Rates: rates, Override: opt.Override,
		},
		Tasks: rows, Log: []string{},
	}, nil
}

// ---- migrate ----

// MigrateExec upgrades ex in place to CurrentExecSchemaVersion and reports whether ex was
// pre-current (so the caller can log/skip a rewrite when nothing changed). Called once, right
// after unmarshal, on every load path (readExec) — never scattered across record/next/batch.
//
// A missing schema_version (every execution.json written before this field existed) is treated
// as LegacyExecSchemaVersion; all existing data (tasks, run_config, log) is untouched — new
// fields added since (e.g. RunConfig.Accounting) are nil-safe pointers/omitempty and already
// tolerated downstream (Accounting.migrateLegacyLedger handles a nil Ledger). The next write
// (record/reconcile/log-note/render-exec's caller) stamps the current version because printJSON
// serializes whatever's in memory — no separate "upgrade write" step is needed.
//
// A version newer than CurrentExecSchemaVersion means this binary is older than the file; refuse
// rather than risk silently dropping fields this build doesn't know about yet.
func MigrateExec(ex *ExecState) error {
	v := ex.SchemaVersion
	if v == 0 {
		v = LegacyExecSchemaVersion
	}
	if v > CurrentExecSchemaVersion {
		return fmt.Errorf("execution.json schema_version %d is newer than this build-helpers supports (max %d) — upgrade build-helpers before resuming this execution", v, CurrentExecSchemaVersion)
	}
	// No field-level migrations exist yet: v2 added schema_version itself (plus the already-
	// nil-safe RunConfig.Accounting/Ledger); v3 adds ExecState.Archived (M8.P1.T2's tombstone
	// index), also nil-safe/omitempty — an absent field on load is simply an empty slice, no
	// explicit upgrade step needed. Extend this switch when a future version needs one.
	ex.SchemaVersion = CurrentExecSchemaVersion
	return nil
}

// ---- archive-aware status resolution (next/batch; archival-design.md §4) ----

// archiveAwareStatus returns a status lookup that resolves id from live Tasks first, falling back
// to the Archived tombstone index, and defaulting to not-started when id is in neither. next/batch
// both key dependency-done resolution off this: without the tombstone fallback, a live task
// depending on an archived (necessarily terminal — Archive's precondition) task would default that
// dep to not-started and stall forever, exactly the resume-safety risk the archive design flags.
func archiveAwareStatus(ex ExecState) func(id string) Status {
	statusOf := make(map[string]Status, len(ex.Tasks)+len(ex.Archived))
	for _, t := range ex.Tasks {
		statusOf[t.ID] = t.Status
	}
	for _, a := range ex.Archived {
		if _, ok := statusOf[a.ID]; !ok { // a live row (should never coexist with a tombstone) wins
			statusOf[a.ID] = a.Status
		}
	}
	return func(id string) Status {
		if s, ok := statusOf[id]; ok {
			return s
		}
		return StatusNotStarted
	}
}

// ---- next ----

// NextTaskInfo identifies the task the build loop should run next. Name is the short operator/
// engine-visible label (Task.Name, plan.json); Summary stays for the fuller one-line description.
type NextTaskInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Model   Model  `json:"model"`
	Effort  Effort `json:"effort"`
}

// NextResult is exactly one of: a runnable task, an orchestrator-only refusal, done, or blocked.
// OrchestratorOnly is set instead of Task (never both) when the next eligible task in dependency
// order declares orchestrator_only:true (design.md SCe) — Task is guaranteed nil in that case, so
// no caller can mistake the refusal for a dispatchable task. The CLI (`next`) turns a non-nil
// OrchestratorOnly into a hard exit-1 error: a subagent-dispatch attempt on that task is a
// structural refusal, not a prose caveat the caller could ignore.
type NextResult struct {
	Task             *NextTaskInfo `json:"task,omitempty"`
	OrchestratorOnly *NextTaskInfo `json:"orchestrator_only,omitempty"`
	Done             bool          `json:"done,omitempty"`
	DepsMet          bool          `json:"deps_met,omitempty"`
	Blocked          []string      `json:"blocked,omitempty"`
	Reason           string        `json:"reason,omitempty"`
}

// NextTask returns the first task in dependency order that is not terminal and has all deps
// done. Done when no non-terminal tasks remain; blocked when non-terminal tasks remain but
// none are eligible (a stall or cycle). statusOrDefault is archive-aware (archiveAwareStatus): an
// archived dependency resolves to its preserved done/terminal status, never a re-selection risk
// since Archive only ever moves already-terminal tasks out of the live plan. When the eligible
// task is orchestrator_only, the result carries OrchestratorOnly instead of Task (never both) —
// see NextResult.
func NextTask(ex ExecState, p Plan) NextResult {
	statusOrDefault := archiveAwareStatus(ex)
	depsOf := map[string][]string{}
	nameOf := map[string]string{}
	orchOnlyOf := map[string]bool{}
	for _, r := range WalkTasks(p) {
		depsOf[r.Task.ID] = r.Task.Deps
		nameOf[r.Task.ID] = r.Task.Name
		orchOnlyOf[r.Task.ID] = r.Task.OrchestratorOnly
	}
	topo := TopoOrder(p)
	anyUnfinished := false
	for _, id := range topo.Order {
		if !statusOrDefault(id).Terminal() {
			anyUnfinished = true
			break
		}
	}
	if !anyUnfinished {
		if len(topo.Cycle) > 0 {
			return NextResult{Blocked: topo.Cycle, Reason: "unschedulable (cycle/dangling deps): " + strings.Join(topo.Cycle, ", ")}
		}
		return NextResult{Done: true}
	}
	rowByID := map[string]ExecTask{}
	for _, t := range ex.Tasks {
		rowByID[t.ID] = t
	}
	for _, id := range topo.Order {
		if statusOrDefault(id).Terminal() {
			continue
		}
		unmet := 0
		for _, d := range depsOf[id] {
			if statusOrDefault(d) != StatusDone {
				unmet++
			}
		}
		if unmet == 0 {
			row := rowByID[id]
			info := &NextTaskInfo{ID: id, Name: nameOf[id], Summary: row.Summary, Model: row.Model, Effort: row.Effort}
			if orchOnlyOf[id] {
				// Structural refusal (design.md SCe): the next eligible task is orchestrator_only —
				// never populate Task, so no caller can dispatch it to the build-engine. The CLI turns
				// this into a hard exit-1 error; the orchestrator runs the task inline instead.
				return NextResult{OrchestratorOnly: info, Reason: "task " + id + " is orchestrator_only — run inline, refused for subagent dispatch"}
			}
			return NextResult{Task: info, DepsMet: true}
		}
	}
	var unfinished []string
	for _, id := range topo.Order {
		if !statusOrDefault(id).Terminal() {
			unfinished = append(unfinished, id)
		}
	}
	reason := "no task has all deps done — dependency stall"
	if len(topo.Cycle) > 0 {
		reason += " (cycle: " + strings.Join(topo.Cycle, ", ") + ")"
	}
	return NextResult{Blocked: unfinished, Reason: reason}
}

// ---- record ----

// RecordFields carries the per-task transition. Nil pointers mean "leave unchanged".
type RecordFields struct {
	Status    *Status
	Test      *string
	Review    *string
	Commit    *string
	Cost      *float64
	TokensOut *int64
	Usage     *Usage // this task's four measured token classes (input, cache-write, cache-read, output); nil == leave unchanged
	Note      *string
	RunID     *string
	Override  *string
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// recomputeTotals rebuilds the cumulative run-config aggregates from the per-task rows — spend
// (output-cost lower bound), measured output tokens, and the per-run four-class Usage total.
// Always recomputed, never hand-summed. Sums BOTH live Tasks and Archived tombstones so the
// archive op (which moves a row from one to the other) never changes the whole-project total —
// see archival-design.md "Accounting stays whole-project-true".
func recomputeTotals(ex *ExecState) {
	var usd float64
	var tok int64
	var u Usage
	haveUsage := false
	for _, x := range ex.Tasks {
		usd += x.CostUSD
		tok += x.TokensOut
		if x.Usage != nil {
			addUsage(&u, x.Usage)
			haveUsage = true
		}
	}
	for _, x := range ex.Archived {
		usd += x.CostUSD
		tok += x.TokensOut
		if x.Usage != nil {
			addUsage(&u, x.Usage)
			haveUsage = true
		}
	}
	ex.RunConfig.SpentUSD = round4(usd)
	ex.RunConfig.TokensOut = tok
	if haveUsage {
		ex.RunConfig.Usage = &u
	} else {
		ex.RunConfig.Usage = nil
	}
}

// addUsage folds src's four token classes (and total/turns) into dst. Shared by recomputeTotals
// (per-run Usage, Σ over per-task Usage) so the run-level total is a pure derived sum, never a
// separately hand-maintained figure.
func addUsage(dst *Usage, src *Usage) {
	dst.InputTokens += src.InputTokens
	dst.CacheCreationTokens += src.CacheCreationTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.Turns += src.Turns
}

// SetUsage records a transcript-derived true-usage snapshot (whole session, incl. subagents) into
// run config and logs it. This is the input+output+cache total the per-task measured output cannot
// see; the skill computes it with the `usage` parser and folds it in here.
func SetUsage(ex *ExecState, u Usage, at string) {
	u.At = at
	ex.RunConfig.TrueUsage = &u
	ex.Updated = at
	ex.Log = append(ex.Log, fmt.Sprintf("%s NOTE true-usage snapshot — %d in + %d out + %d cache = %d total tokens across %d turns (transcript-derived; internal format, best-effort)",
		at, u.InputTokens, u.OutputTokens, u.CacheCreationTokens+u.CacheReadTokens, u.TotalTokens, u.Turns))
}

// SetAccounting records a whole-session, per-model true-cost accounting snapshot (main transcript +
// every subagent transcript) into run config, refreshes the flat true_usage totals derived from it,
// derives orchestrator-only O (mainFileID's own ledger entry — see Accounting.PriceFile) as a
// distinct line item, computes and persists the ACC5 additive accounting identity (see below),
// clears any prior cost_status:unresolved marker (this run DID resolve the main transcript, so the
// marker no longer applies), and logs it. Unlike per-task SpentUSD (output-only, a lower bound),
// acct.CostUSD prices input + cache + output across all models. `final` marks the finish-time
// authoritative snapshot in the log; any model that matched no rate is surfaced in the log line
// rather than silently priced at $0. specsAsOf and buildHelpersSHA (both best-effort, empty string
// when unavailable) pin the rate-table snapshot and the binary that priced it — see
// Accounting.SpecsAsOf/BuildHelpersSHA.
//
// ACC5 identity (spikes/acc5-identity-basis.md): the known-subagent classification re-derives from
// DiscoverSubagentTranscripts(mainFileID) — the SAME discovery seam ACC2 uses for O-isolation and
// attribution — rather than trusting acct.Ledger's own keys, so a ledger entry that entered
// session_total by some OTHER path (e.g. a stale legacy-migration sentinel, or a future discovery
// drift between this call and the run that built acct) surfaces as an itemized residual instead of
// silently agreeing with itself. fixedSubagents is empty: no fixed-model (magistrate/reviewer)
// classifier exists yet (ACC5 handoff §7) — every discovered subagent lands in Σ(agent-*.jsonl) until
// one is added; the identity's closure guarantee is agnostic to that split (spike §2.1). A discovery
// error degrades to an empty known-subagent set rather than failing the run (accounting never blocks
// a build) — any real subagent then shows up as an itemized residual, loud rather than silent.
func SetAccounting(ex *ExecState, acct *Accounting, mainFileID string, rates RateTable, final bool, at string, specsAsOf, buildHelpersSHA string) {
	acct.At = at
	acct.CostStatus = ""
	acct.SpecsAsOf = specsAsOf
	acct.BuildHelpersSHA = buildHelpersSHA
	if o, ok := acct.PriceFile(mainFileID, rates); ok {
		acct.Orchestrator = &o
	}
	knownSubagents, _ := DiscoverSubagentTranscripts(mainFileID) // best-effort; nil on error, never fatal
	identity := acct.ComputeIdentity(mainFileID, knownSubagents, nil, rates)
	acct.Identity = &identity
	ex.RunConfig.Accounting = acct
	u := acct.Flatten()
	u.At = at
	ex.RunConfig.TrueUsage = &u
	ex.Updated = at
	label := "true-cost snapshot"
	if final {
		label = "final true-cost snapshot"
	}
	line := fmt.Sprintf("%s NOTE %s — $%.6f across %d turns, %d models (%d in + %d out + %d cache = %d total tokens; transcript-derived, best-effort)",
		at, label, acct.CostUSD, acct.Turns, len(acct.Models), u.InputTokens, u.OutputTokens, u.CacheCreationTokens+u.CacheReadTokens, u.TotalTokens)
	if acct.Orchestrator != nil {
		line += fmt.Sprintf(" — O (orchestrator-only) $%.6f", acct.Orchestrator.CostUSD)
	}
	if len(acct.Unpriced) > 0 {
		line += " — UNPRICED models (no rate matched, excluded from cost): " + strings.Join(acct.Unpriced, ", ")
	}
	line += fmt.Sprintf(" — identity residual $%.6f (tolerance $%.6f, %s)", identity.ResidualUSD, identity.ToleranceUSD, identity.CostStatus)
	if identity.CostStatus == "residual-exceeded" {
		line += fmt.Sprintf(" — UNCLASSIFIED transcripts: %d", len(identity.UnclassifiedTranscripts))
	}
	ex.Log = append(ex.Log, line)
}

// SetAccountingUnresolved records the non-fatal cost_status:unresolved marker (ACC1) when the main
// transcript could not be read this run — loud (logged, never silent) but never fatal to the build
// EXCEPT a baseline-capture run, which the caller (runRecordUsage) fails instead of calling this.
// Prior accounting (if any) is left untouched — a transient read failure must not erase or silently
// "recover" the last known-good snapshot, it must only flag that THIS run's snapshot did not update.
func SetAccountingUnresolved(ex *ExecState, transcript string, at string) {
	if ex.RunConfig.Accounting == nil {
		ex.RunConfig.Accounting = &Accounting{}
	}
	ex.RunConfig.Accounting.CostStatus = "unresolved"
	ex.RunConfig.Accounting.At = at
	ex.Updated = at
	ex.Log = append(ex.Log, fmt.Sprintf("%s NOTE cost_status:unresolved — main transcript %s could not be read this run; prior accounting (if any) left unchanged, O not updated (non-fatal; see ACC1)", at, transcript))
}

// RecordListVsActualNote appends the ACC5 §5 documentary list-vs-actual rate-basis note: the
// operator-entered actual-billed whole-session total (read from the Claude Code status line / `/cost`
// at a baseline finish — build-helpers cannot parse it from transcripts) set against this run's
// list-priced transcript figures (session_total + O from ex.RunConfig.Accounting). Append-only
// (ListVsActualNotes), so a re-capture is itemized rather than overwriting the prior one.
//
// PURELY DOCUMENTARY (spikes/acc5-identity-basis.md §5.2): this makes NO pass/fail assertion and no
// caller may gate a build on its delta fields — the status-line total is out-of-harness (the operator
// types it in), non-reproducible (actual billing: negotiated rates + account-level discounts, not
// obtainable in this workspace), and differs from the transcript figures in BOTH basis (list vs
// actual-billed) and scope (O is orchestrator-only; the status line is whole-session) — a nonzero
// delta is an expected artifact, never a correctness signal. Returns an error only when there is no
// accounting snapshot yet to compare against (record-usage must run first) — never for the delta
// itself, however large.
func RecordListVsActualNote(ex *ExecState, statusLineTotalUSD float64, capturedBy, at string) error {
	acct := ex.RunConfig.Accounting
	if acct == nil {
		return fmt.Errorf("record-list-vs-actual-note: no accounting snapshot recorded yet — run record-usage first")
	}
	var oUSD float64
	if acct.Orchestrator != nil {
		oUSD = acct.Orchestrator.CostUSD
	}
	delta := acct.CostUSD - statusLineTotalUSD
	var pct float64
	if statusLineTotalUSD != 0 {
		pct = delta / statusLineTotalUSD * 100
	}
	note := ListVsActualNote{
		StatusLineTotalUSD:        statusLineTotalUSD,
		TranscriptSessionTotalUSD: acct.CostUSD,
		TranscriptOUSD:            oUSD,
		ListVsActualDeltaUSD:      delta,
		ListVsActualDeltaPct:      pct,
		RateBasis:                 "list",
		SpecsAsOf:                 acct.SpecsAsOf,
		CapturedBy:                capturedBy,
		CapturedAt:                at,
		Note: "list-vs-actual rate-basis (and scope) artifact, not a computation bug — list price is >= actual " +
			"negotiated/discounted billing, and the status line is whole-session scope while O is orchestrator-only " +
			"(spikes/acc5-identity-basis.md §5-6); documentary only, does NOT gate the build.",
	}
	acct.ListVsActualNotes = append(acct.ListVsActualNotes, note)
	ex.Updated = at
	ex.Log = append(ex.Log, fmt.Sprintf(
		"%s NOTE list-vs-actual — status-line $%.6f vs transcript session_total $%.6f (Δ $%.6f, %.2f%%); documentary, does not gate the build",
		at, statusLineTotalUSD, acct.CostUSD, delta, pct))
	return nil
}

// RecordTask applies a per-task transition, recomputes cumulative spend from per-task costs
// (never hand-summed), bumps timestamps, and appends a log line. Mutates ex in place.
func RecordTask(ex *ExecState, taskID string, f RecordFields, at string) error {
	idx := -1
	for i := range ex.Tasks {
		if ex.Tasks[i].ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("record: task %q not in execution state", taskID)
	}
	t := &ex.Tasks[idx]
	if f.Status != nil {
		if !f.Status.Known() {
			return fmt.Errorf("record: invalid status %q", *f.Status)
		}
		t.Status = *f.Status
	}
	if f.Test != nil {
		t.Test = *f.Test
	}
	if f.Review != nil {
		t.Review = *f.Review
	}
	if f.Commit != nil {
		t.Commit = *f.Commit
	}
	if f.Cost != nil {
		t.CostUSD = *f.Cost
	}
	if f.TokensOut != nil {
		t.TokensOut = *f.TokensOut
	}
	if f.Usage != nil {
		t.Usage = f.Usage
	}
	if f.Note != nil {
		t.Notes = *f.Note
	}
	if f.RunID != nil {
		ex.RunConfig.LastRunID = *f.RunID
	}
	if f.Override != nil {
		ex.RunConfig.Override = *f.Override
	}
	t.Updated = at
	recomputeTotals(ex)
	ex.Updated = at

	parts := []string{}
	if f.Status != nil {
		parts = append(parts, "→ "+string(*f.Status))
	}
	if t.Test != "" {
		parts = append(parts, "test "+t.Test)
	}
	if t.Review != "" {
		parts = append(parts, "review "+t.Review)
	}
	if t.Commit != "" {
		parts = append(parts, t.Commit)
	}
	if f.Cost != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *f.Cost))
	}
	if f.TokensOut != nil {
		parts = append(parts, fmt.Sprintf("%d out-tok", *f.TokensOut))
	}
	ex.Log = append(ex.Log, strings.TrimSpace(at+" "+taskID+" "+strings.Join(parts, " ")))
	return nil
}

// ---- pause events (E1/SC1) ----

// RecordPauseEvent appends a structured pause-event {reason_enum,at,task_id?} to ex.PauseEvents
// and a matching free-text Log line, so the machine-readable event and the human-readable
// narrative never diverge. reason must be one of the closed PauseReason values (Known()); an
// unrecognized reason is rejected rather than persisted as a valid-looking event that
// MechanicalSlipCount could then miscount. taskID is optional — empty for a plan-level pause.
func RecordPauseEvent(ex *ExecState, reason PauseReason, taskID, at string) error {
	if !reason.Known() {
		return fmt.Errorf("record-pause: invalid reason_enum %q", reason)
	}
	ex.PauseEvents = append(ex.PauseEvents, PauseEvent{ReasonEnum: reason, At: at, TaskID: taskID})
	ex.Updated = at
	line := fmt.Sprintf("%s PAUSE %s", at, reason)
	if taskID != "" {
		line += " " + taskID
	}
	ex.Log = append(ex.Log, line)
	return nil
}

// MechanicalSlipCount derives SC1's mechanical-slip count: the number of persisted pause events
// whose reason_enum is tooling-forced (git|state|merge — PauseReason.Mechanical). design-level,
// approval, budget, and signing pauses are operator-facing gates and are excluded — SC1 measures
// mechanical regression against the Opus baseline, not every pause the run ever took.
func MechanicalSlipCount(ex ExecState) int {
	n := 0
	for _, e := range ex.PauseEvents {
		if e.ReasonEnum.Mechanical() {
			n++
		}
	}
	return n
}

// ---- escalation events (E2/SC8) ----

// RecordEscalationEvent appends a structured escalation-event {trigger,tier,route,at,task_id?} to
// ex.EscalationEvents and a matching free-text Log line, so the machine-readable firing count
// (MagistrateFiringCount) and the human-readable narrative never diverge. trigger must be a member
// of the closed named set (classify.go's escalationTiers) — the SAME closed-set gate
// ClassifyEscalation uses to reach the magistrate — so an out-of-set condition can never be
// persisted as a firing, mirroring RecordPauseEvent's reason_enum guard. taskID is optional — empty
// for a plan-level firing.
func RecordEscalationEvent(ex *ExecState, trigger EscalationTrigger, tier, route, taskID, at string) error {
	if _, ok := escalationTiers[trigger]; !ok {
		return fmt.Errorf("record-escalation: trigger %q is not in the closed named set", trigger)
	}
	ex.EscalationEvents = append(ex.EscalationEvents, EscalationEvent{Trigger: trigger, Tier: tier, Route: route, At: at, TaskID: taskID})
	ex.Updated = at
	line := fmt.Sprintf("%s ESCALATE %s -> %s (tier %s)", at, trigger, route, tier)
	if taskID != "" {
		line += " " + taskID
	}
	ex.Log = append(ex.Log, line)
	return nil
}

// MagistrateFiringCount derives SC3a's equal-magistrate-firing void-check input: the number of
// persisted escalation-events, i.e. how many times this run fired the magistrate. Both runs of an
// SC3a model-tier pair must report an EQUAL count — a caller comparing two ExecState firing counts
// is comparing this derived value, never a hand-tallied or free-text-grepped one.
func MagistrateFiringCount(ex ExecState) int {
	return len(ex.EscalationEvents)
}

// ---- log-note ----

// LogNote appends a plan-level entry to the execution log and bumps `updated`. For gate
// results and other state changes that are not tied to a single task (the plan-acceptance
// gate, phase/milestone integration gates) — `record` is per-task, this is plan-level.
func LogNote(ex *ExecState, note, at string) {
	ex.Log = append(ex.Log, strings.TrimSpace(at+" NOTE "+note))
	ex.Updated = at
}

// ---- reconcile ----

// ReconcileExec applies a plan diff to execution state, preserving completed work: carried
// rows keep their status + SHA; changed/added rows reset to not-started (changed-was-done is
// noted as possibly-orphaned); removed rows become superseded (kept for history). Archive-aware
// (archival-design.md §4): oldP is the pruned live plan, so a design regenerate that still derives
// an already-archived milestone (design.md unchanged there) makes Diff see its tasks as freshly
// "Added" — archived ids are excluded from the rebuild below instead, so they are never re-admitted
// as a fresh not-started live row; their tombstone in ex.Archived (untouched by this function)
// remains the sole live-facing record. Mutates ex.
func ReconcileExec(ex *ExecState, oldP, newP Plan, designUpdated, planUpdated, at string) {
	d := Diff(oldP, newP)
	changed := map[string]bool{}
	for _, c := range d.Changed {
		changed[c.ID] = true
	}
	added := map[string]bool{}
	for _, a := range d.Added {
		added[a.ID] = true
	}
	oldByID := map[string]ExecTask{}
	for _, t := range ex.Tasks {
		oldByID[t.ID] = t
	}
	archived := map[string]bool{}
	for _, x := range ex.Archived {
		archived[x.ID] = true
	}

	rows := []ExecTask{}
	for _, r := range WalkTasks(newP) {
		spec := r.Task
		if archived[spec.ID] {
			continue // already archived — stays out of the live doc regardless of the diff verdict
		}
		prior, hadPrior := oldByID[spec.ID]
		switch {
		case added[spec.ID] || !hadPrior:
			rows = append(rows, ExecTask{ID: spec.ID, Summary: spec.Summary, Kind: spec.Kind.Resolve(), Model: spec.Model, Effort: spec.Effort, Status: StatusNotStarted, Updated: at, Notes: "added by design change"})
		case changed[spec.ID]:
			note := fmt.Sprintf("changed by design @ %s", at)
			if prior.Status == StatusDone {
				commit := prior.Commit
				if commit == "" {
					commit = "?"
				}
				note += fmt.Sprintf(" (was ✅ — rebuilt; prior commit %s may be orphaned)", commit)
			}
			rows = append(rows, ExecTask{ID: spec.ID, Summary: spec.Summary, Kind: spec.Kind.Resolve(), Model: spec.Model, Effort: spec.Effort, Status: StatusNotStarted, Updated: at, Notes: note})
		default: // carried: keep status + SHA; refresh tier/summary/kind display (retune carries)
			prior.Summary = spec.Summary
			prior.Kind = spec.Kind.Resolve()
			prior.Model = spec.Model
			prior.Effort = spec.Effort
			rows = append(rows, prior)
		}
	}
	for _, rem := range d.Removed {
		prior, ok := oldByID[rem.ID]
		if !ok {
			continue
		}
		prior.Status = StatusSuperseded
		prior.Updated = at
		prior.Notes = fmt.Sprintf("superseded — removed by design @ %s", at)
		if oldByID[rem.ID].Status == StatusDone {
			prior.Notes += " (built work may be orphaned — operator review)"
		}
		rows = append(rows, prior)
	}
	ex.Tasks = rows
	recomputeTotals(ex)
	if designUpdated != "" {
		ex.Provenance.DesignUpdated = designUpdated
	}
	if planUpdated != "" {
		ex.Provenance.PlanUpdated = planUpdated
	}
	ex.Provenance.DerivedAt = at
	ex.Updated = at
	ex.Log = append(ex.Log, fmt.Sprintf("%s reconcile — carried %d, changed %d, added %d, superseded %d", at, len(d.Carried), len(d.Changed), len(d.Added), len(d.Removed)))
}
