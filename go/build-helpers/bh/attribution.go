package bh

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"
)

// This file implements MEASURED per-task cost attribution (SC2 committed goal) — replacing the
// batch even-split ESTIMATE with the true cost each subagent transcript actually incurred, keyed to
// the task that spawned it. It reads each subagent transcript once, extracts the spawning task ID
// from the first `user` turn (the leading `Task <id>` line of the dispatch prompt), sums that
// transcript's five priced token buckets per model (the same accounting math as Account/priceModels,
// shared not restated), and attributes the whole transcript's cost to that task. A task with several
// transcripts (impl + test + review agents) sums across them.
//
// The correlation contract is DECIDED in testdata/accounting/EXPECTED.md (M2.P1.T3): EXTRACT the
// FIRST `[A-Z][0-9]+\.P[0-9]+\.T[0-9]+` match in the first user turn — the dispatch prompt always
// begins `Task <task_id>`, so the leading match is the spawning task, and the greedy `\.T[0-9]+`
// captures the full digit run so `T31` never truncates to `T3`. MATCH is EXACT full-string equality
// against the known task-id set — never substring/prefix — so `M2.P1.T3` and `M2.P1.T31` never
// cross-attribute. A transcript whose first turn yields no ID, or an ID absent from the known set, is
// UNMAPPABLE: its cost is NOT dropped and NOT silently mis-attributed — it goes into a flagged
// even-split pool distributed across the batch's tasks, and the transcript is surfaced in Unmappable.
//
// Split of concerns matches the package contract: parsing takes an io.Reader and the attribution math
// takes in-memory values (both pure/testable). The CLI (package main) owns the filesystem —
// discovering the subagent transcripts (DiscoverSubagentTranscripts, accounting.go — the ONE seam
// shared with O-isolation, see ACC2/M13.P2.T2) and opening each.

// taskIDRE matches a build-with-team task ID as embedded in a dispatch prompt's leading `Task <id>`
// line. The greedy `[0-9]+` runs capture the FULL digit sequence, so `M2.P1.T31` is captured whole
// and never truncates to the shorter `M2.P1.T3` — the extraction half of the shared-prefix guard.
var taskIDRE = regexp.MustCompile(`[A-Z][0-9]+\.P[0-9]+\.T[0-9]+`)

// extractTaskID returns the FIRST (leftmost) task-ID match in text, or "" when none is present.
// FindString is leftmost-longest for a single alternation-free pattern: leftmost picks the leading
// `Task <id>` token even when the summary later embeds another ID; the greedy digit runs make it the
// full ID. "" is the "unmappable — no task-id" signal to Attribute.
func extractTaskID(text string) string { return taskIDRE.FindString(text) }

// userTurnProbe reads just enough of a transcript line to detect a `user` turn and reach its text
// content, whether content is a plain string or the array-of-blocks shape Claude Code writes.
type userTurnProbe struct {
	Type    string `json:"type"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// firstUserText returns the concatenated text of a line IF it is a `user` turn (ok=false otherwise,
// so the caller keeps scanning for the first user turn). Content is tried as a bare string first,
// then as an array of `{type,text}` blocks — the two shapes real transcripts use.
func firstUserText(line []byte) (string, bool) {
	var p userTurnProbe
	if json.Unmarshal(line, &p) != nil || p.Message == nil {
		return "", false
	}
	if p.Type != "user" && p.Message.Role != "user" {
		return "", false
	}
	return contentText(p.Message.Content), true
}

// contentText extracts the human-readable text from a `message.content` value that may be a bare
// string or an array of typed blocks; non-text blocks contribute nothing.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Text != "" {
				b.WriteString(blk.Text)
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	return ""
}

// ParseTranscriptForAttribution reads one subagent transcript (JSONL) fully and returns the task ID
// extracted from its FIRST `user` turn ("" if none) plus the per-model token buckets summed across
// every usage-bearing turn. Unlike ParseByModel (watermark-resumable, leaves a trailing partial line
// unconsumed), this is a whole-file measured parse: the final line is folded even without a trailing
// newline. Usage folding reuses foldLine, so the bucket math is identical to the whole-session path.
func ParseTranscriptForAttribution(r io.Reader) (taskID string, models map[string]*ModelBuckets, err error) {
	models = map[string]*ModelBuckets{}
	br := bufio.NewReaderSize(r, 1<<20)
	gotUser := false
	for {
		line, e := br.ReadBytes('\n')
		if len(line) > 0 {
			foldLine(models, line)
			if !gotUser {
				if text, ok := firstUserText(line); ok {
					taskID = extractTaskID(text)
					gotUser = true
				}
			}
		}
		if e == io.EOF {
			return taskID, models, nil
		}
		if e != nil {
			return taskID, models, e
		}
	}
}

// AttribSource is one subagent transcript to attribute: a stable FileID (its path) and a Reader
// positioned at the start of the file (attribution is a whole-file measured parse, no watermark).
type AttribSource struct {
	FileID string
	Reader io.Reader
}

// TaskCost is one task's MEASURED cost: the per-model buckets summed across every subagent transcript
// that mapped to it, priced from the rate table. CostAttribution is always "measured" — this is real
// per-transcript spend, not an even-split estimate.
type TaskCost struct {
	Models          map[string]*ModelBuckets `json:"models"`
	CostByModel     map[string]float64       `json:"cost_by_model,omitempty"`
	CostUSD         float64                  `json:"cost_usd"`
	Turns           int64                    `json:"turns"`
	Unpriced        []string                 `json:"unpriced_models,omitempty"`
	Transcripts     []string                 `json:"transcripts"`
	CostAttribution string                   `json:"cost_attribution"` // always "measured"
}

// EvenSplitPool is the flagged fallback: the summed cost of every UNMAPPABLE transcript, distributed
// evenly across the batch's known tasks (PerTaskCostUSD == CostUSD / len(Tasks)). This cost is never
// dropped and never silently attributed to a wrong task — it is quarantined here and flagged
// "batch-even-split" so a consumer records it as an estimate, distinct from the measured per-task cost.
type EvenSplitPool struct {
	Models          map[string]*ModelBuckets `json:"models"`
	CostByModel     map[string]float64       `json:"cost_by_model,omitempty"`
	CostUSD         float64                  `json:"cost_usd"`
	PerTaskCostUSD  float64                  `json:"per_task_cost_usd"`
	Turns           int64                    `json:"turns"`
	Unpriced        []string                 `json:"unpriced_models,omitempty"`
	Tasks           []string                 `json:"tasks"`            // the known tasks the pool is split across
	Transcripts     []string                 `json:"transcripts"`      // the unmappable transcripts that fed the pool
	CostAttribution string                   `json:"cost_attribution"` // always "batch-even-split"
}

// UnmappableTranscript surfaces a transcript that could not be attributed to a known task, with why —
// so an unmappable transcript is visible, never a silent gap.
type UnmappableTranscript struct {
	Transcript  string `json:"transcript"`
	ExtractedID string `json:"extracted_id,omitempty"` // "" when the first user turn carried no task ID
	Reason      string `json:"reason"`
}

// Attribution is the per-task measured breakdown for a batch of subagent transcripts, plus the flagged
// even-split pool for any unmappable transcript. Tasks holds only tasks that received measured cost;
// KnownTasks is the full batch set matched against (so a task with zero mapped transcripts is still
// visible as absent from Tasks).
type Attribution struct {
	Tasks      map[string]*TaskCost   `json:"tasks"`
	KnownTasks []string               `json:"known_tasks"`
	EvenSplit  *EvenSplitPool         `json:"even_split,omitempty"`
	Unmappable []UnmappableTranscript `json:"unmappable,omitempty"`
	At         string                 `json:"at,omitempty"`
}

// Attribute maps each subagent transcript to its spawning task by the DECIDED correlation contract
// (see file header): extract the first task ID from the first user turn, match it by EXACT equality
// against known. Mapped transcripts accumulate MEASURED per-task cost; unmappable transcripts feed
// the flagged even-split pool distributed across known. Blank/whitespace entries in known are ignored.
// Pricing is delegated to priceModels (shared with Account) so attribution can never drift from the
// whole-session accounting on how a bucket is priced or an unmatched model surfaced.
func Attribute(sources []AttribSource, known []string, rates RateTable, at string) (*Attribution, error) {
	knownSet := map[string]bool{}
	for _, k := range known {
		if k = strings.TrimSpace(k); k != "" {
			knownSet[k] = true
		}
	}
	knownList := sortedSet(knownSet)

	attr := &Attribution{Tasks: map[string]*TaskCost{}, KnownTasks: knownList, At: at}
	poolModels := map[string]*ModelBuckets{}
	var poolTranscripts []string

	for _, s := range sources {
		id, models, err := ParseTranscriptForAttribution(s.Reader)
		if err != nil {
			return nil, err
		}
		// EXACT full-string equality — never substring/prefix. Combined with taskIDRE's greedy digit
		// capture, this is the shared-prefix guard: an extracted `M2.P1.T31` can only ever key the map
		// entry `M2.P1.T31`, never the shorter `M2.P1.T3`, and vice versa.
		if id != "" && knownSet[id] {
			tc := attr.Tasks[id]
			if tc == nil {
				tc = &TaskCost{Models: map[string]*ModelBuckets{}, CostAttribution: "measured"}
				attr.Tasks[id] = tc
			}
			mergeModels(tc.Models, models)
			tc.Transcripts = append(tc.Transcripts, s.FileID)
			continue
		}
		reason := "extracted task-id not in known set"
		if id == "" {
			reason = "no task-id in first user turn"
		}
		attr.Unmappable = append(attr.Unmappable, UnmappableTranscript{Transcript: s.FileID, ExtractedID: id, Reason: reason})
		mergeModels(poolModels, models)
		poolTranscripts = append(poolTranscripts, s.FileID)
	}

	for _, tc := range attr.Tasks {
		tc.CostByModel, tc.CostUSD, tc.Turns, tc.Unpriced = priceModels(tc.Models, rates)
		sort.Strings(tc.Transcripts)
	}
	if len(poolTranscripts) > 0 {
		sort.Strings(poolTranscripts)
		byModel, total, turns, unpriced := priceModels(poolModels, rates)
		pool := &EvenSplitPool{
			Models: poolModels, CostByModel: byModel, CostUSD: total, Turns: turns, Unpriced: unpriced,
			Tasks: knownList, Transcripts: poolTranscripts, CostAttribution: "batch-even-split",
		}
		if n := len(knownList); n > 0 {
			pool.PerTaskCostUSD = total / float64(n)
		}
		attr.EvenSplit = pool
	}
	return attr, nil
}

// mergeModels adds every model bucket in src into dst (allocating dst entries as needed), so a task's
// multiple transcripts sum per model into one ledger.
func mergeModels(dst, src map[string]*ModelBuckets) {
	for m, b := range src {
		agg := dst[m]
		if agg == nil {
			agg = &ModelBuckets{}
			dst[m] = agg
		}
		agg.add(*b)
	}
}

// sortedSet returns the set's keys sorted, for deterministic output and even-split ordering.
func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
