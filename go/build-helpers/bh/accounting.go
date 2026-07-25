package bh

import (
	"bufio"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/roster"
)

// This file implements whole-session, per-model TRUE-COST accounting (SC2). It closes the old
// single-transcript bug where only the orchestrator transcript was summed and every subagent's
// input/cache tokens went uncounted: Account walks the main transcript AND every subagent
// transcript, sums the five priced token buckets PER MODEL, prices each model from the embedded
// model roster (ai-shared-lib/go/roster, contract preferred over list — see rosterRate), and
// tracks a per-file byte watermark so re-runs parse only appended bytes and add the delta
// (idempotent resume — no full re-parse).
//
// Split of concerns matches the package contract: parsing takes an io.Reader and the accounting
// math takes in-memory values (both pure/testable). The CLI (package main) owns the filesystem —
// discovering the transcript files, seeking each to its watermark, and the atomic write-back —
// using SubagentGlobs to locate the sibling subagent transcripts.

// ModelBuckets holds the five priced token buckets summed for one model across the turns counted.
// The buckets mirror the rate dimensions in anthropic-specifications.json's pricing map.
type ModelBuckets struct {
	Input        int64 `json:"input_tokens"`
	CacheWrite5m int64 `json:"cache_write_5m_tokens"`
	CacheWrite1h int64 `json:"cache_write_1h_tokens"`
	CacheRead    int64 `json:"cache_read_tokens"`
	Output       int64 `json:"output_tokens"`
	Turns        int64 `json:"turns"`
}

func (b *ModelBuckets) add(o ModelBuckets) {
	b.Input += o.Input
	b.CacheWrite5m += o.CacheWrite5m
	b.CacheWrite1h += o.CacheWrite1h
	b.CacheRead += o.CacheRead
	b.Output += o.Output
	b.Turns += o.Turns
}

// Cost is this model's dollar cost at rate r: Σ(bucket_tokens × per-MTok rate) / 1e6.
func (b ModelBuckets) Cost(r Rate) float64 {
	return (float64(b.Input)*r.Input +
		float64(b.CacheWrite5m)*r.CacheWrite5m +
		float64(b.CacheWrite1h)*r.CacheWrite1h +
		float64(b.CacheRead)*r.CacheRead +
		float64(b.Output)*r.Output) / 1e6
}

// Rate is the per-MTok (per 1,000,000 tokens) USD rate for the five buckets, as stored under
// anthropic-specifications.json's pricing.list.<model> / pricing.contract.<model>.
type Rate struct {
	Input        float64 `json:"input"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
	Output       float64 `json:"output"`
}

// RateTable maps a model ID to its per-MTok rate. Lookups go through Match, which is tolerant of
// the date suffix real transcript model IDs carry.
type RateTable map[string]Rate

// LoadRateTable parses a RateTable from anthropic-specifications.json bytes. When preferContract is
// set, a model present in pricing.contract overrides its pricing.list entry (contract-preferred,
// else list); otherwise only pricing.list is used. Unknown JSON keys on a rate object (e.g. the
// _introductory_pricing note) are ignored.
func LoadRateTable(specs []byte, preferContract bool) (RateTable, error) {
	var doc struct {
		Pricing struct {
			List     map[string]Rate `json:"list"`
			Contract map[string]Rate `json:"contract"`
		} `json:"pricing"`
	}
	if err := json.Unmarshal(specs, &doc); err != nil {
		return nil, err
	}
	table := RateTable{}
	for k, v := range doc.Pricing.List {
		table[k] = v
	}
	if preferContract {
		for k, v := range doc.Pricing.Contract {
			table[k] = v
		}
	}
	return table, nil
}

// SpecsAsOf extracts anthropic-specifications.json's own `_as_of` date from its raw bytes, for
// pinning into Accounting.SpecsAsOf at record-usage time (see main.runRecordUsage). Best-effort:
// unreadable/invalid specs or a missing `_as_of` key yields "" rather than an error — accounting
// must never block a run over a provenance field.
func SpecsAsOf(specs []byte) string {
	var doc struct {
		AsOf string `json:"_as_of"`
	}
	if json.Unmarshal(specs, &doc) != nil {
		return ""
	}
	return doc.AsOf
}

// dateSuffixRE matches the trailing -YYYYMMDD real transcript model IDs carry, e.g. the
// `-20251001` on `claude-haiku-4-5-20251001` that the bare spec key `claude-haiku-4-5` lacks.
var dateSuffixRE = regexp.MustCompile(`-\d{8}$`)

// Match resolves a model ID (as it appears in a transcript) to a rate, tolerant of the date suffix
// production IDs carry. It tries, in order: exact key; the ID with a trailing -YYYYMMDD stripped;
// the longest table key that is a hyphen-bounded prefix of the ID. The matched key is returned for
// traceability. ok is false when no key matches — the caller must surface such an ID (see
// Accounting.Unpriced) rather than silently pricing it at $0.
func (t RateTable) Match(model string) (rate Rate, matched string, ok bool) {
	if r, found := t[model]; found {
		return r, model, true
	}
	if stripped := dateSuffixRE.ReplaceAllString(model, ""); stripped != model {
		if r, found := t[stripped]; found {
			return r, stripped, true
		}
	}
	best := ""
	for k := range t {
		if strings.HasPrefix(model, k) && len(model) > len(k) && model[len(k)] == '-' && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return t[best], best, true
	}
	return Rate{}, "", false
}

// TranscriptSource is one transcript file to fold into an Accounting. Reader is positioned at
// StartOffset (the caller seeks the file there from the prior watermark); ParseByModel reads from
// there to EOF and the new watermark is StartOffset + bytes-consumed. FileID is a stable key for
// the watermark map (the file's absolute path).
type TranscriptSource struct {
	FileID      string
	Reader      io.Reader
	StartOffset int64
}

// Accounting is the resumable, whole-session true-cost state persisted in RunConfig. It carries a
// per-file byte watermark (bytes parsed so far, so a re-run reads only appended bytes), a per-file
// bucket ledger (each file's own accumulated per-model buckets), the session-total per-model buckets
// (derived — always Σ over Ledger, never hand-summed), the derived per-model and total dollar cost,
// and any model IDs that matched no rate (surfaced, never silently priced at $0).
//
// The per-file Ledger is what makes rotation/truncation correct: session totals are the SUM over
// per-file entries, so a rotation reset REPLACES one file's entry (its vanished turns drop out and
// its surviving turns are not double-added) rather than accumulating a fresh full-parse on top of a
// stale contribution. See Account for the replace-vs-add-delta rule.
type Accounting struct {
	Watermarks   map[string]int64                    `json:"watermarks"`
	Ledger       map[string]map[string]*ModelBuckets `json:"ledger,omitempty"` // fileID -> model -> that file's accumulated buckets; session Models is Σ over this
	Models       map[string]*ModelBuckets            `json:"models"`           // derived: Σ Ledger across files (kept for readers/Flatten)
	CostByModel  map[string]float64                  `json:"cost_by_model,omitempty"`
	Unpriced     []string                            `json:"unpriced_models,omitempty"`
	CostUSD      float64                             `json:"cost_usd"`
	Turns        int64                               `json:"turns"`
	At           string                              `json:"at,omitempty"`
	Orchestrator *OrchestratorCost                   `json:"orchestrator,omitempty"` // O — the top-level orchestrator transcript's OWN true-cost, isolated per-transcript (see PriceFile); nil until a run resolves the main transcript
	CostStatus   string                              `json:"cost_status,omitempty"`  // "" (resolved) | "unresolved" (main transcript unreadable this run — loud, non-fatal; see SetAccounting/runRecordUsage)

	// SpecsAsOf and BuildHelpersSHA pin WHAT produced this snapshot: the pinned rate-table
	// snapshot's own `_as_of` date (anthropic-specifications.json) and the sha256 of the
	// build-helpers binary that ran the accounting math. Both are populated together at
	// record-usage time (see SetAccounting) so a cost figure is always traceable to the rate
	// table + code that priced it; omitted (never zero-valued) on any pre-upgrade execution.json.
	SpecsAsOf       string `json:"specs_as_of,omitempty"`
	BuildHelpersSHA string `json:"build_helpers_sha,omitempty"`

	// Identity is the transcript-only additive accounting identity (see the Identity method),
	// populated by SetAccounting alongside O/specs_as_of so the closure result and any residual
	// travel with the run record. nil until a run resolves it.
	Identity *IdentityResult `json:"identity,omitempty"`

	// ListVsActualNotes is the documentary list-vs-actual rate-basis note history — one entry
	// per operator-captured status-line reading (RecordListVsActualNote); append-only so a re-capture
	// (e.g. a retried baseline) is itemized, never overwritten. NEVER gates a build — see
	// RecordListVsActualNote.
	ListVsActualNotes []ListVsActualNote `json:"list_vs_actual_notes,omitempty"`
}

// OrchestratorCost is O: the top-level orchestrator transcript's own true-cost, priced from
// ONLY that one file's ledger entry — never a per-model total, which would also lump in same-model
// subagents (e.g. a fixed-Opus reviewer/magistrate running alongside an Opus orchestrator). See
// Accounting.PriceFile, the single place this isolation happens.
type OrchestratorCost struct {
	Usage
	CostUSD float64 `json:"cost_usd"`
}

// legacyLedgerFileID is the synthetic ledger key under which a pre-ledger Accounting's already-summed
// Models are preserved on first resume (older execution.json carried Models + Watermarks but no
// per-file Ledger). It cannot collide with a real FileID (production FileIDs are cleaned absolute
// paths; a leading NUL byte can never appear in one). This bridges resume from a legacy snapshot: the
// prior aggregate is retained and new per-file deltas add on top. A --final full re-parse rebuilds the
// ledger from scratch (prior=nil) and drops this entry, self-healing any imprecision. The one edge it
// cannot resolve is a legacy file that rotates on its very first post-upgrade run — its pre-rotation
// bytes remain folded into this aggregate with no per-file entry to replace; --final corrects it.
const legacyLedgerFileID = "\x00migrated-legacy-aggregate"

// Flatten collapses the per-model buckets into the model-agnostic Usage totals (input +
// cache_creation + cache_read + output, plus turns) that the `usage` command and the true_usage
// snapshot report. cache_creation is the 5m + 1h buckets recombined.
func (a *Accounting) Flatten() Usage {
	return flattenBuckets(a.Models)
}

// flattenBuckets collapses ANY per-model bucket map (a whole session's Models, or a single file's
// own ledger entry — see PriceFile) into the model-agnostic Usage totals. Shared so the session-wide
// and per-transcript (O) views can never drift on how buckets become a Usage total.
func flattenBuckets(models map[string]*ModelBuckets) Usage {
	var u Usage
	for _, b := range models {
		u.InputTokens += b.Input
		u.CacheCreationTokens += b.CacheWrite5m + b.CacheWrite1h
		u.CacheReadTokens += b.CacheRead
		u.OutputTokens += b.Output
		u.Turns += b.Turns
	}
	u.TotalTokens = u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
	return u
}

// PriceFile derives O — the true-cost of ONE ledger file in isolation, typically the top-level
// orchestrator/main transcript's FileID (session grand total minus every subagent transcript,
// isolated per-transcript). Because it prices only that file's own ledger
// entry, it is correct regardless of whether every sibling subagent transcript was discovered: a
// nested subagent's cost lives in ITS OWN file's ledger entry, never inside the main transcript's,
// so it can never leak into O by construction. ok is false when fileID has no ledger entry (the
// file was never folded into this Accounting — e.g. the main transcript could not be read this run;
// see the cost_status:unresolved path in runRecordUsage).
func (a *Accounting) PriceFile(fileID string, rates RateTable) (cost OrchestratorCost, ok bool) {
	entry, found := a.Ledger[fileID]
	if !found {
		return OrchestratorCost{}, false
	}
	_, total, _, _ := priceModels(entry, rates)
	return OrchestratorCost{Usage: flattenBuckets(entry), CostUSD: total}, true
}

// ---- transcript-only additive accounting identity ----

// IdentityResult is the closed-form check `session_total = O + Σ(agent-*.jsonl) + fixed-subagents +
// residual`. Unlike a subtractive O (`session_total − Σ(subagents)`, a tautology that
// structurally zeroes the gap), every term here is priced from its OWN ledger entry, so a
// ledger entry outside the caller-supplied classification surfaces as a nonzero, itemized residual
// instead of silently vanishing into O. CostStatus is "ok" (|residual| ≤ tolerance; still recorded,
// never dropped) or "residual-exceeded" (tolerance breached; UnclassifiedTranscripts itemized) — see
// Identity. O is NEVER adjusted in either branch (acceptance-2: no silent absorption into O).
type IdentityResult struct {
	SessionTotalUSD   float64 `json:"session_total_usd"`
	OUSD              float64 `json:"o_usd"`
	VariableAgentsUSD float64 `json:"variable_agents_usd"` // Σ(agent-*.jsonl) — variable per-task role agents
	FixedSubagentsUSD float64 `json:"fixed_subagents_usd"` // fixed-model escalation/review subagents (e.g. magistrate, fixed reviewer)
	ResidualUSD       float64 `json:"residual_usd"`        // session_total − (O + variable + fixed); asserted, never folded into O
	ToleranceUSD      float64 `json:"tolerance_usd"`       // T = max(1e-6 × session_total, 1e-6 USD)
	CostStatus        string  `json:"cost_status"`         // "ok" | "residual-exceeded"

	// UnclassifiedTranscripts itemizes every ledger entry (other than the O file) not covered by the
	// caller's known-subagent/fixed classification — populated ONLY when CostStatus is
	// "residual-exceeded": the residual's exact attribution, so a leak is actionable, not
	// just flagged.
	UnclassifiedTranscripts []UnclassifiedTranscript `json:"unclassified_transcripts,omitempty"`
}

// UnclassifiedTranscript is one ledger entry the identity's classification could not place into
// {O, agent-*, fixed} — the exact failure mode this identity replaces silent O-absorption with.
type UnclassifiedTranscript struct {
	Path    string  `json:"path"`
	CostUSD float64 `json:"cost_usd"`
	Model   string  `json:"model,omitempty"` // sorted, comma-joined model keys of this entry; "" if none priced
}

// ComputeIdentity computes the additive accounting identity over a's ledger. mainFileID is O's own
// ledger entry (priced via PriceFile, never adjusted). knownSubagents is the independently-discovered
// set of non-orchestrator transcript paths (e.g. DiscoverSubagentTranscripts' output) that fold into
// Σ(agent-*.jsonl); fixedSubagents is the subset of those classified as fixed-model escalation/review
// subagents instead (excluded from the variable sum; may be nil, since no
// fixed-subagent classifier exists yet). A ledger entry that is neither
// mainFileID nor named in either list is UNCLASSIFIED: its cost is excluded from every bucket, so it
// inflates residual and is itemized rather than silently landing in O — this is what makes the check
// a real one, not `O + Σ(subagents) ≡ session_total`'s structural tautology.
//
// All four terms are priced from THIS ONE rates argument (basis-invariant): session_total
// is Σ over a.Models (itself Σ over the full Ledger, independent of classification), never a's
// possibly-stale CostUSD field, so a caller cannot silently mix bases across the identity's terms.
//
// residual = session_total − (O + Σ(agent-*) + fixed-subagents). |residual| ≤ T = max(1e-6 ×
// session_total, 1e-6 USD) (a two-sided bracket: float64 regrouping noise sits ~7 orders of
// magnitude below T; the cheapest realistic un-globbed subagent sits orders of magnitude above it)
// sets cost_status "ok" — residual_usd is still recorded, never omitted, even at ~0. A
// breach sets cost_status "residual-exceeded" and itemizes UnclassifiedTranscripts; the caller
// (loud, non-fatal — fails a baseline-capture run only) decides whether that fails the run. O
// is identical in both branches — see PriceFile, the single place its isolation happens.
func (a *Accounting) ComputeIdentity(mainFileID string, knownSubagents, fixedSubagents []string, rates RateTable) IdentityResult {
	fixedSet := fileIDSet(fixedSubagents)
	knownSet := fileIDSet(knownSubagents)

	o, _ := a.PriceFile(mainFileID, rates)

	var variableUSD, fixedUSD float64
	var unclassified []UnclassifiedTranscript
	for fileID, entry := range a.Ledger {
		if fileID == mainFileID {
			continue
		}
		_, cost, _, _ := priceModels(entry, rates)
		switch {
		case fixedSet[fileID]:
			fixedUSD += cost
		case knownSet[fileID]:
			variableUSD += cost
		default:
			unclassified = append(unclassified, UnclassifiedTranscript{Path: fileID, CostUSD: cost, Model: modelKeysJoined(entry)})
		}
	}
	sort.Slice(unclassified, func(i, j int) bool { return unclassified[i].Path < unclassified[j].Path })

	_, sessionTotal, _, _ := priceModels(a.Models, rates)
	residual := sessionTotal - (o.CostUSD + variableUSD + fixedUSD)
	tolerance := math.Max(1e-6*sessionTotal, 1e-6)

	res := IdentityResult{
		SessionTotalUSD: sessionTotal, OUSD: o.CostUSD, VariableAgentsUSD: variableUSD,
		FixedSubagentsUSD: fixedUSD, ResidualUSD: residual, ToleranceUSD: tolerance, CostStatus: "ok",
	}
	if math.Abs(residual) > tolerance {
		res.CostStatus = "residual-exceeded"
		res.UnclassifiedTranscripts = unclassified
	}
	return res
}

// fileIDSet builds a lookup set from a FileID list, tolerating a nil/empty input (no classifier data
// available yet — e.g. no fixed-subagent classifier exists yet).
func fileIDSet(ids []string) map[string]bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// modelKeysJoined returns entry's model keys, sorted and comma-joined, for UnclassifiedTranscript's
// best-effort model label ("" when entry has no priced/keyed models — should not occur in practice,
// since foldLine only ever creates a keyed entry).
func modelKeysJoined(entry map[string]*ModelBuckets) string {
	keys := make([]string, 0, len(entry))
	for m := range entry {
		keys = append(keys, m)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// ListVsActualNote is the documentary list-vs-actual rate-basis note: the actual-billed
// whole-session total the operator reads from the Claude Code status line (`/cost`) at a baseline
// finish, set against this run's list-priced transcript figures. PURELY DOCUMENTARY — no
// field here is asserted against; see RecordListVsActualNote, the sole writer.
type ListVsActualNote struct {
	StatusLineTotalUSD        float64 `json:"status_line_total_usd"`        // operator-entered; build-helpers cannot parse this from transcripts
	TranscriptSessionTotalUSD float64 `json:"transcript_session_total_usd"` // session_total (list-priced)
	TranscriptOUSD            float64 `json:"transcript_o_usd"`             // O, orchestrator-only (list-priced)
	ListVsActualDeltaUSD      float64 `json:"list_vs_actual_delta_usd"`     // transcript_session_total_usd − status_line_total_usd
	ListVsActualDeltaPct      float64 `json:"list_vs_actual_delta_pct"`     // the same delta as a percent of status_line_total_usd
	RateBasis                 string  `json:"rate_basis"`                   // "list" (this note's basis; "contract"-ready)
	SpecsAsOf                 string  `json:"specs_as_of,omitempty"`        // the pinned rate-snapshot timestamp this run's transcript pricing used
	CapturedBy                string  `json:"captured_by"`                  // provenance: who captured the status-line reading
	CapturedAt                string  `json:"captured_at"`                  // provenance: when
	Note                      string  `json:"note"`                         // states the delta is a basis/scope artifact, not a computation bug
}

// ParseByModel reads a Claude Code transcript (JSONL) from r, summing the five priced token buckets
// per model across every usage-bearing (assistant) line. It returns the per-model buckets and the
// number of bytes consumed, counting ONLY complete newline-terminated lines: a trailing partial
// line (a transcript mid-append) is left unconsumed so the watermark lands on a line boundary and
// the partial line is re-parsed once completed. Malformed lines and lines with no usage object are
// tolerated/skipped (best-effort — the transcript format is internal to Claude Code).
func ParseByModel(r io.Reader) (map[string]*ModelBuckets, int64, error) {
	models := map[string]*ModelBuckets{}
	br := bufio.NewReaderSize(r, 1<<20)
	var consumed int64
	for {
		line, err := br.ReadBytes('\n')
		if err == nil {
			// Complete, newline-terminated line: count its bytes and fold it in.
			consumed += int64(len(line))
			foldLine(models, line)
			continue
		}
		if err == io.EOF {
			// Trailing bytes without a newline are an incomplete line — do NOT consume them.
			return models, consumed, nil
		}
		return models, consumed, err
	}
}

// foldLine adds one transcript line's usage to the per-model buckets, keyed by the line's model
// string (empty when the line carries no model — e.g. a top-level usage object). A line that does
// not parse as JSON, or carries no usage object, contributes nothing.
func foldLine(models map[string]*ModelBuckets, line []byte) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return
	}
	var lp lineProbe
	if json.Unmarshal(line, &lp) != nil {
		return
	}
	model, ur, ok := lp.resolve()
	if !ok {
		return
	}
	agg := models[model]
	if agg == nil {
		agg = &ModelBuckets{}
		models[model] = agg
	}
	agg.add(ur.buckets())
}

// Account folds the given transcript sources into prior (a fresh Accounting when prior is nil),
// keeping a per-file bucket ledger so session totals are Σ over per-file entries. Each source is
// applied by StartOffset:
//   - StartOffset == 0 (fresh file, --final full re-parse, OR rotation/truncation reset — see
//     main.openSources) REPLACES that file's ledger entry with the fresh full parse: vanished turns
//     drop out and surviving turns are not double-added.
//   - StartOffset > 0 (incremental resume) ADDS only the delta (offset→EOF) onto that file's entry.
//
// The watermark advances to StartOffset + bytes-read. It is idempotent on re-run: a source seeked to
// a watermark with no appended bytes contributes an empty delta and leaves its entry and watermark
// unchanged. After folding, Models is rebuilt from the ledger and per-model + total dollar cost are
// recomputed from rates (contract-preferred, else list); a model matching no rate is recorded in
// Unpriced rather than dropped or priced at $0. When rates is empty, buckets are still accumulated
// but no cost is computed. Mutates and returns the (possibly newly allocated) Accounting.
func Account(prior *Accounting, sources []TranscriptSource, rates RateTable, at string) (*Accounting, error) {
	acct := prior
	if acct == nil {
		acct = &Accounting{}
	}
	if acct.Watermarks == nil {
		acct.Watermarks = map[string]int64{}
	}
	acct.migrateLegacyLedger()
	for _, s := range sources {
		models, consumed, err := ParseByModel(s.Reader)
		if err != nil {
			return acct, err
		}
		if s.StartOffset == 0 {
			// Full parse from the start: this file's fresh contribution REPLACES its prior entry.
			acct.Ledger[s.FileID] = models
			acct.Watermarks[s.FileID] = consumed
			continue
		}
		// Incremental resume: add only the appended-bytes delta onto this file's ledger entry.
		entry := acct.Ledger[s.FileID]
		if entry == nil {
			entry = map[string]*ModelBuckets{}
			acct.Ledger[s.FileID] = entry
		}
		for m, b := range models {
			agg := entry[m]
			if agg == nil {
				agg = &ModelBuckets{}
				entry[m] = agg
			}
			agg.add(*b)
		}
		acct.Watermarks[s.FileID] = s.StartOffset + consumed
	}
	acct.rebuildModels()
	acct.recompute(rates)
	acct.At = at
	return acct, nil
}

// migrateLegacyLedger ensures Ledger is non-nil and, when resuming a pre-ledger snapshot (Models
// present but Ledger absent), preserves the already-summed Models under legacyLedgerFileID so the
// prior aggregate survives and new per-file deltas add on top. See legacyLedgerFileID for the one
// edge (a legacy file rotating on its first post-upgrade run) that only --final fully corrects.
func (a *Accounting) migrateLegacyLedger() {
	if a.Ledger != nil {
		return
	}
	a.Ledger = map[string]map[string]*ModelBuckets{}
	if len(a.Models) == 0 {
		return
	}
	seed := map[string]*ModelBuckets{}
	for m, b := range a.Models {
		cp := *b
		seed[m] = &cp
	}
	a.Ledger[legacyLedgerFileID] = seed
}

// rebuildModels recomputes the session-total per-model buckets as Σ over every per-file ledger entry.
// Models is always derived here, never mutated directly, so a rotation reset (a replaced ledger entry)
// yields the correct total with no stale carry-over.
func (a *Accounting) rebuildModels() {
	models := map[string]*ModelBuckets{}
	for _, entry := range a.Ledger {
		for m, b := range entry {
			agg := models[m]
			if agg == nil {
				agg = &ModelBuckets{}
				models[m] = agg
			}
			agg.add(*b)
		}
	}
	a.Models = models
}

// recompute derives Turns, per-model cost, total cost, and the unpriced-model list from the summed
// buckets and the rate table. Always recomputed from Models, never hand-summed — it is the single
// pricing derivation (priceModels), shared with per-task attribution (see attribution.go) so the
// two paths can never drift on how a model bucket is priced or how an unmatched model is surfaced.
func (a *Accounting) recompute(rates RateTable) {
	a.CostByModel, a.CostUSD, a.Turns, a.Unpriced = priceModels(a.Models, rates)
}

// rosterRate resolves model's per-MTok rate from the embedded model roster
// (ai-shared-lib/go/roster), contract preferred over list — the roster's own Price() already
// applies that preference, so this only translates its PriceTable into the local Rate shape.
// ok is false for anything the roster can't price (unrecognized id, a sentinel, or a row sourced
// on neither basis): the caller records that as unpriced rather than guessing a rate.
func rosterRate(model string) (rate Rate, ok bool) {
	pt, err := roster.Price(model)
	if err != nil {
		return Rate{}, false
	}
	return Rate{
		Input:        pt.Input,
		CacheWrite5m: pt.CacheWrite5m,
		CacheWrite1h: pt.CacheWrite1h,
		CacheRead:    pt.CacheRead,
		Output:       pt.Output,
	}, true
}

// priceModels is the one place buckets become dollars: for each model it sums turns and, when
// pricing is requested, prices it from the roster (rosterRate — contract preferred over list) and
// collects any model the roster can't price into unpriced (sorted, surfaced — never silently
// priced at $0 or assumed onto a neighboring tier). byModel is nil when nothing priced; unpriced
// is nil when every model resolved. rates gates pricing on/off only: an empty table is the
// caller's documented opt-out (e.g. the `usage` command, which discards cost fields), leaving
// buckets counted for Turns with no cost math run; a non-empty table's per-model values are no
// longer consulted for the actual rate — see LoadRateTable's doc for the specs-file path that
// role is retained for.
func priceModels(models map[string]*ModelBuckets, rates RateTable) (byModel map[string]float64, total float64, turns int64, unpriced []string) {
	byModel = map[string]float64{}
	for m, b := range models {
		turns += b.Turns
		if len(rates) == 0 {
			continue
		}
		rate, ok := rosterRate(m)
		if !ok {
			unpriced = append(unpriced, m)
			continue
		}
		c := b.Cost(rate)
		byModel[m] = c
		total += c
	}
	if len(byModel) == 0 {
		byModel = nil
	}
	if len(unpriced) > 0 {
		sort.Strings(unpriced)
	}
	return byModel, total, turns, unpriced
}

// SubagentGlobs returns the glob patterns under which a session's subagent transcripts live,
// relative to the main transcript path. Two layouts are covered: the fixture / sibling layout
// (<dir>/subagents/agent-*.jsonl) and the live Claude Code layout, where subagents sit under a
// directory named after the session id — the main transcript's basename without its extension
// (<dir>/<session-id>/subagents/agent-*.jsonl).
//
// Superseded by DiscoverSubagentTranscripts: both patterns here are FIXED-depth, and Go's
// filepath.Glob has no `**` recursion, so this misses every nested-workflow subagent — a
// batch-engine task's build-engine spawns its impl/test/review/magistrate agents one level
// deeper, at <dir>/<session-id>/subagents/workflows/wf_*/agent-*.jsonl. Retained ONLY as the
// literal pre-fix behavior for the regression pin in accounting_test.go (proving
// DiscoverSubagentTranscripts finds strictly more than this ever could) — do not use for new
// discovery. DiscoverSubagentTranscripts is the one seam both O-isolation and attribution now
// consume.
func SubagentGlobs(mainPath string) []string {
	dir := filepath.Dir(mainPath)
	stem := strings.TrimSuffix(filepath.Base(mainPath), filepath.Ext(mainPath))
	return []string{
		filepath.Join(dir, "subagents", "agent-*.jsonl"),
		filepath.Join(dir, stem, "subagents", "agent-*.jsonl"),
	}
}

// cleanAbs returns path's cleaned absolute form. Local to bh (main.go, which imports bh, carries
// its own identical absClean) so DiscoverSubagentTranscripts's output is a stable FileID key
// (cleaned absolute path) regardless of whether mainPath was passed relatively or absolutely —
// matching the normalization main.discoverTranscripts already applies to the main transcript.
func cleanAbs(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(path)
}

// DiscoverSubagentTranscripts is the ONE discovery seam: it recursively walks
// every candidate subagents/ root under mainPath and returns every agent-*.jsonl transcript found
// at ANY depth — direct subagents AND nested-workflow subagents (a batch-engine task's build-engine
// spawning impl/test/review/magistrate agents under workflows/wf_*/) AND any deeper nesting a future
// spawn pattern introduces, with no fixed-depth assumption. This is the single producer both
// O-isolation (Account/PriceFile, keyed by TranscriptSource.FileID) and attribution (Attribute,
// keyed by AttribSource.FileID) consume — callers build both source slices from this SAME returned
// path list, so the two views can never disagree on what counts as a subagent.
//
// Walk roots (both walked, every run, to serve the fixture sibling layout AND the live layout):
//   - <dir>/subagents             — fixture / sibling layout (bh/testdata/*)
//   - <dir>/<stem>/subagents      — live Claude Code layout; stem = mainPath's basename minus
//     its extension (the session id)
//
// Match rule: base name matches `agent-*.jsonl`, at any depth (filepath.WalkDir, never
// filepath.Glob — Go's Glob has no `**` recursion, which is the defect this seam fixes). Any
// other name (notably a workflow run's journal.jsonl, which carries no billable usage) is excluded
// by construction — never walked into the returned set. A root that does not exist on disk
// contributes nothing (not an error: a run with no fan-out, or a fixture with no live-layout dir).
// Results are deduped and returned as cleaned absolute paths, sorted for a stable, reproducible key
// order across runs.
func DiscoverSubagentTranscripts(mainPath string) ([]string, error) {
	dir := filepath.Dir(mainPath)
	stem := strings.TrimSuffix(filepath.Base(mainPath), filepath.Ext(mainPath))
	roots := []string{
		filepath.Join(dir, "subagents"),
		filepath.Join(dir, stem, "subagents"),
	}
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		if err := walkAgentTranscripts(root, seen, &out); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// walkAgentTranscripts walks one subagents/ root (if present) and appends every agent-*.jsonl file
// found at any depth into out, deduping by cleaned absolute path via seen. A missing/non-directory
// root is skipped silently — it is a valid "no subagents under this root" state, not an error.
func walkAgentTranscripts(root string, seen map[string]bool, out *[]string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if matched, _ := filepath.Match("agent-*.jsonl", d.Name()); !matched {
			return nil
		}
		a := cleanAbs(path)
		if !seen[a] {
			seen[a] = true
			*out = append(*out, a)
		}
		return nil
	})
}
