package bh

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests pin the whole-session, per-model true-cost accounting (SC2) against the ground-truth
// fixtures in testdata/accounting/ (manifest: testdata/accounting/EXPECTED.md). They assert the
// multi-transcript walk (main + every subagent transcript) reproduces EXPECTED.md's exact per-model
// token buckets AND dollar totals, that the per-file byte watermark makes an incremental re-parse
// equal a fresh full parse (idempotent resume), that model IDs are matched suffix-tolerantly, that
// an absent nested cache_creation object does not crash, and that an unpriced model is surfaced
// rather than silently priced at $0.

const accountingDir = "testdata/accounting"

// loadTestRates loads the same rate table the production path uses, from the co-located specs file.
// Skips (does not fail) when the specs file isn't co-located, keeping the package portable.
func loadTestRates(t *testing.T) RateTable {
	t.Helper()
	b, err := os.ReadFile(specsPath) // "../../anthropic-specifications.json" (schema_sync_test.go)
	if err != nil {
		t.Skipf("anthropic-specifications.json not found at %s (skipping accounting rate tests): %v", specsPath, err)
	}
	table, err := LoadRateTable(b, false) // list-price (fixtures priced against pricing.list)
	if err != nil {
		t.Fatalf("LoadRateTable: %v", err)
	}
	return table
}

// openSource opens a fixture transcript seeked to startOffset and returns it as a TranscriptSource
// plus the handle to close. It mirrors main.openSources so the tests exercise the real read path.
func openSource(t *testing.T, fileID, path string, startOffset int64) (TranscriptSource, *os.File) {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if startOffset > 0 {
		if _, err := fh.Seek(startOffset, io.SeekStart); err != nil {
			t.Fatalf("seek %s to %d: %v", path, startOffset, err)
		}
	}
	return TranscriptSource{FileID: fileID, Reader: fh, StartOffset: startOffset}, fh
}

// discoverFixtureSources builds the whole-session source set (main + subagents) the same way
// main.discoverTranscripts does: main transcript plus everything DiscoverSubagentTranscripts finds
// — the ONE discovery seam, not the legacy fixed-depth SubagentGlobs.
func discoverFixtureSources(t *testing.T, mainPath string) ([]TranscriptSource, []*os.File) {
	t.Helper()
	var sources []TranscriptSource
	var handles []*os.File
	s, h := openSource(t, mainPath, mainPath, 0)
	sources, handles = append(sources, s), append(handles, h)
	subs, err := DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts(%s): %v", mainPath, err)
	}
	for _, m := range subs {
		s, h := openSource(t, m, m, 0)
		sources, handles = append(sources, s), append(handles, h)
	}
	return sources, handles
}

// legacySubagentGlobPaths mirrors the exact pre-fix discovery behavior — SubagentGlobs' two
// fixed-depth patterns run through filepath.Glob, with no recursion — so tests can pin, on a live
// fixture, that the new seam finds strictly more than the old one ever could, without relying on
// reverting any code.
func legacySubagentGlobPaths(t *testing.T, mainPath string) []string {
	t.Helper()
	var paths []string
	for _, pattern := range SubagentGlobs(mainPath) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	return paths
}

func closeHandles(handles []*os.File) {
	for _, h := range handles {
		h.Close()
	}
}

func wantBuckets(input, c5m, c1h, cread, output, turns int64) ModelBuckets {
	return ModelBuckets{Input: input, CacheWrite5m: c5m, CacheWrite1h: c1h, CacheRead: cread, Output: output, Turns: turns}
}

func assertBuckets(t *testing.T, got *ModelBuckets, want ModelBuckets, label string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: no buckets for model", label)
	}
	if *got != want {
		t.Fatalf("%s buckets = %+v, want %+v", label, *got, want)
	}
}

func assertClose(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.10f, want %.10f (Δ %.2e)", label, got, want, got-want)
	}
}

// TestAccounting_OrchestratorPerModel pins the orchestrator-only per-model buckets and dollar costs
// (EXPECTED.md "orchestrator.jsonl"). The orchestrator fixture has no subagents, so the walk over
// it alone is the two-model, four-turn main-transcript total.
func TestAccounting_OrchestratorPerModel(t *testing.T) {
	rates := loadTestRates(t)
	s, h := openSource(t, "orch", filepath.Join(accountingDir, "orchestrator.jsonl"), 0)
	defer h.Close()
	acct, err := Account(nil, []TranscriptSource{s}, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	assertBuckets(t, acct.Models["claude-sonnet-5"], wantBuckets(15000, 4000, 3000, 3000, 1500, 2), "orchestrator sonnet-5")
	assertBuckets(t, acct.Models["claude-opus-4-8"], wantBuckets(10000, 2000, 1000, 4500, 2500, 2), "orchestrator opus-4-8")
	assertClose(t, acct.CostByModel["claude-sonnet-5"], 0.1014, "orchestrator sonnet-5 cost")
	assertClose(t, acct.CostByModel["claude-opus-4-8"], 0.13725, "orchestrator opus-4-8 cost")
	assertClose(t, acct.CostUSD, 0.1014+0.13725, "orchestrator total cost")
	if len(acct.Unpriced) != 0 {
		t.Fatalf("unexpected unpriced models: %v", acct.Unpriced)
	}
}

// TestAccounting_WholeSessionGrandTotal is the primary golden test: the multi-transcript walk over
// testdata/accounting/ (orchestrator + all four subagent transcripts) must reproduce EXPECTED.md's
// grand-total per-model buckets and dollar totals exactly. This is the total the old
// orchestrator-only parser undercounted by dropping every subagent's input/cache tokens.
func TestAccounting_WholeSessionGrandTotal(t *testing.T) {
	rates := loadTestRates(t)
	sources, handles := discoverFixtureSources(t, filepath.Join(accountingDir, "orchestrator.jsonl"))
	defer closeHandles(handles)

	// The walk must find the main transcript + all four subagent transcripts.
	if len(sources) != 5 {
		t.Fatalf("discovered %d transcripts, want 5 (orchestrator + 4 subagents)", len(sources))
	}

	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	assertBuckets(t, acct.Models["claude-sonnet-5"], wantBuckets(23000, 5800, 3900, 3800, 2400, 6), "grand sonnet-5")
	assertBuckets(t, acct.Models["claude-opus-4-8"], wantBuckets(15000, 3100, 1200, 5900, 3200, 5), "grand opus-4-8")
	assertClose(t, acct.CostByModel["claude-sonnet-5"], 0.15129, "grand sonnet-5 cost")
	assertClose(t, acct.CostByModel["claude-opus-4-8"], 0.189325, "grand opus-4-8 cost")
	assertClose(t, acct.CostUSD, 0.340615, "grand total cost")
	if acct.Turns != 11 {
		t.Fatalf("grand turns = %d, want 11", acct.Turns)
	}
	if len(acct.Unpriced) != 0 {
		t.Fatalf("unexpected unpriced models: %v", acct.Unpriced)
	}
}

// nestedDir is the golden fixture tree at REAL nesting depth — a batch-engine's
// direct subagent plus two nested-workflow build-engine agents under workflows/wf_*/, the exact
// layout today's fixed-depth SubagentGlobs cannot reach. Manifest: testdata/nested/EXPECTED.md.
const nestedDir = "testdata/nested"

// TestDiscoverSubagentTranscripts_NestedWorkflowDepth is assertion 1 (EXPECTED.md): the seam must
// find all 3 subagent transcripts at their real depths — the depth-1 direct control AND both
// depth-3 nested-workflow agents — and must NEVER surface wf_batch/journal.jsonl (excluded by name,
// not by depth). It also pins the depth regression directly, without relying on reverting any code:
// the legacy SubagentGlobs two-fixed-depth-glob approach, run against this SAME fixture, finds only
// the depth-1 file — strictly fewer than the new seam — proving the new seam is not merely
// equivalent but a real widening to the depth today's globs cannot express.
func TestDiscoverSubagentTranscripts_NestedWorkflowDepth(t *testing.T) {
	mainPath := filepath.Join(nestedDir, "orchestrator.jsonl")

	got, err := DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("discovered %d subagent transcripts, want 3 (direct + e1 + e2); got %v", len(got), got)
	}
	wantSuffixes := []string{"agent-direct.jsonl", "agent-e1.jsonl", "agent-e2.jsonl"}
	for _, want := range wantSuffixes {
		found := false
		for _, g := range got {
			if strings.HasSuffix(g, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("discovered set %v missing %s", got, want)
		}
	}
	for _, g := range got {
		if strings.HasSuffix(g, "journal.jsonl") {
			t.Fatalf("discovered set %v must never include journal.jsonl (not an agent transcript)", got)
		}
	}

	// Regression pin: the legacy glob-only approach cannot see past depth 1 on this fixture.
	legacy := legacySubagentGlobPaths(t, mainPath)
	if len(legacy) != 1 || !strings.HasSuffix(legacy[0], "agent-direct.jsonl") {
		t.Fatalf("legacy SubagentGlobs on the nested fixture = %v, want exactly [agent-direct.jsonl] — if this fails, the fixture no longer pins the depth regression", legacy)
	}
	if len(got) <= len(legacy) {
		t.Fatalf("new seam found %d, legacy glob found %d — the new seam must find strictly more at real nesting depth", len(got), len(legacy))
	}
}

// TestAccounting_NestedOIsolationAndGrandTotal is assertion 2 (EXPECTED.md): O — PriceFile on the
// main transcript's OWN ledger entry — must equal ONLY orchestrator.jsonl's true cost regardless of
// how many subagents are folded alongside it, AND the grand total across all 4 discovered
// transcripts (main + 3 subagents) must equal the fixture's hand-verified total. Pre-change,
// discovery finds only 2 files (main + agent-direct) so the grand total is short by the two nested
// transcripts' cost — this assertion fails on that pre-change discovery.
func TestAccounting_NestedOIsolationAndGrandTotal(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(nestedDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)

	if len(sources) != 4 {
		t.Fatalf("discovered %d transcripts, want 4 (orchestrator + direct + e1 + e2)", len(sources))
	}

	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	// O: isolated to ONLY the main transcript's own ledger entry, unaffected by the 3 subagents
	// folded alongside it in this same Accounting.
	o, ok := acct.PriceFile(mainPath, rates)
	if !ok {
		t.Fatalf("PriceFile(%s) found no ledger entry", mainPath)
	}
	assertClose(t, o.CostUSD, 0.03087, "O (orchestrator-only, isolated)")
	if o.Turns != 1 {
		t.Fatalf("O turns = %d, want 1 (orchestrator.jsonl's own single turn)", o.Turns)
	}

	// Grand total: reachable only when all 3 subagents (incl. both depth-3 nested agents) are
	// actually discovered and folded in.
	assertClose(t, acct.CostUSD, 0.097755, "grand total (main + direct + e1 + e2)")
	if acct.Turns != 6 { // orchestrator 1 + direct 1 + e1 2 + e2 2
		t.Fatalf("grand turns = %d, want 6", acct.Turns)
	}
	if len(acct.Unpriced) != 0 {
		t.Fatalf("unexpected unpriced models: %v", acct.Unpriced)
	}
}

// TestAccounting_WatermarkIdempotency pins the incremental-resume invariant (EXPECTED.md "Watermark
// / rotation scenario"): parsing pass1 records a byte watermark at 1946; resuming pass2 from that
// offset reads only the appended bytes; the merged buckets equal a fresh full parse of pass2, and a
// further re-parse with no new bytes changes nothing. pass1 is an exact byte-prefix of pass2.
func TestAccounting_WatermarkIdempotency(t *testing.T) {
	rates := loadTestRates(t)
	pass1 := filepath.Join(accountingDir, "watermark", "session.pass1.jsonl")
	pass2 := filepath.Join(accountingDir, "watermark", "session.pass2.jsonl")
	const fileID = "session" // same logical file across passes (pass2 = pass1 + appended bytes)

	// Pass 1: fresh parse of the first-pass bytes.
	s1, h1 := openSource(t, fileID, pass1, 0)
	acct, err := Account(nil, []TranscriptSource{s1}, rates, "t")
	h1.Close()
	if err != nil {
		t.Fatalf("pass1 Account: %v", err)
	}
	if acct.Watermarks[fileID] != 1946 {
		t.Fatalf("pass1 watermark = %d, want 1946", acct.Watermarks[fileID])
	}

	// Pass 2 incremental: resume from the recorded watermark, reading only the appended bytes.
	s2, h2 := openSource(t, fileID, pass2, acct.Watermarks[fileID])
	acct, err = Account(acct, []TranscriptSource{s2}, rates, "t")
	h2.Close()
	if err != nil {
		t.Fatalf("pass2 incremental Account: %v", err)
	}
	if acct.Watermarks[fileID] != 3924 {
		t.Fatalf("pass2 watermark = %d, want 3924 (full pass2 size)", acct.Watermarks[fileID])
	}

	// Fresh full parse of pass2, no watermark.
	fs, fh := openSource(t, fileID, pass2, 0)
	fresh, err := Account(nil, []TranscriptSource{fs}, rates, "t")
	fh.Close()
	if err != nil {
		t.Fatalf("fresh pass2 Account: %v", err)
	}

	// Incremental(pass1)+incremental(pass2 from offset) == fresh(pass2) for every bucket and cost.
	for model, want := range fresh.Models {
		assertBuckets(t, acct.Models[model], *want, "incremental vs fresh "+model)
	}
	assertClose(t, acct.CostUSD, fresh.CostUSD, "incremental vs fresh total cost")

	// Idempotency: re-parse pass2 again from the current watermark (no new bytes) — nothing changes.
	s3, h3 := openSource(t, fileID, pass2, acct.Watermarks[fileID])
	before := acct.CostUSD
	acct, err = Account(acct, []TranscriptSource{s3}, rates, "t")
	h3.Close()
	if err != nil {
		t.Fatalf("re-parse Account: %v", err)
	}
	if acct.Watermarks[fileID] != 3924 {
		t.Fatalf("re-parse watermark moved to %d, want unchanged 3924", acct.Watermarks[fileID])
	}
	assertClose(t, acct.CostUSD, before, "re-parse cost drift")
	for model, want := range fresh.Models {
		assertBuckets(t, acct.Models[model], *want, "re-parse vs fresh "+model)
	}
}

// TestAccounting_RotationResetDoesNotDoubleCount is an adversarial regression case NOT modeled by
// EXPECTED.md: a transcript file is rotated/truncated so its recorded watermark now exceeds the
// file's current size (session.pass2.jsonl, watermark 3924, replaced on disk by the shorter
// session.pass1.jsonl, size 1946). The safe response mirrors main.openSources' documented contract
// ("A watermark past the current file size ... resets that file to a full re-parse from offset 0")
// — a full re-parse of the file's CURRENT bytes should REPLACE that file's prior contribution, not
// add on top of it. Account has no per-file bucket ledger to subtract from, so it just accumulates
// the fresh full-parse buckets onto whatever the file already contributed under its old (larger)
// watermark — silently double-counting surviving turns and leaving stale buckets from truncated-away
// turns. This test asserts the CORRECT post-rotation result (pass1-only content) and is expected to
// FAIL against the current implementation, pinning the defect for a fix.
func TestAccounting_RotationResetDoesNotDoubleCount(t *testing.T) {
	rates := loadTestRates(t)
	pass1 := filepath.Join(accountingDir, "watermark", "session.pass1.jsonl")
	pass2 := filepath.Join(accountingDir, "watermark", "session.pass2.jsonl")
	const fileID = "session"

	// Prior state: file was fully consumed as pass2 (sonnet-5 + opus-4-8), watermark = 3924.
	s1, h1 := openSource(t, fileID, pass2, 0)
	prior, err := Account(nil, []TranscriptSource{s1}, rates, "t")
	h1.Close()
	if err != nil {
		t.Fatalf("prior full-parse Account: %v", err)
	}
	if prior.Watermarks[fileID] != 3924 {
		t.Fatalf("prior watermark = %d, want 3924", prior.Watermarks[fileID])
	}

	// Rotation: the file on disk is now pass1 (1946 bytes) — shorter than the recorded watermark.
	// openSources resets StartOffset to 0 in this case (mirrored here directly).
	fi, err := os.Stat(pass1)
	if err != nil {
		t.Fatalf("stat pass1: %v", err)
	}
	if fi.Size() >= prior.Watermarks[fileID] {
		t.Fatalf("test fixture invariant broken: pass1 size %d must be < prior watermark %d", fi.Size(), prior.Watermarks[fileID])
	}
	s2, h2 := openSource(t, fileID, pass1, 0)
	got, err := Account(prior, []TranscriptSource{s2}, rates, "t")
	h2.Close()
	if err != nil {
		t.Fatalf("post-rotation Account: %v", err)
	}
	if got.Watermarks[fileID] != 1946 {
		t.Fatalf("post-rotation watermark = %d, want 1946 (current file size)", got.Watermarks[fileID])
	}

	// Correct result: the file's contribution reflects ONLY its current (post-rotation) content —
	// pass1 is sonnet-5-only, so opus-4-8 (only ever present in the truncated-away turns) must be
	// gone, and sonnet-5 must equal a single fresh parse of pass1, not double-added onto the old total.
	wantSonnet := wantBuckets(15000, 4000, 3000, 3000, 1500, 2)
	assertBuckets(t, got.Models["claude-sonnet-5"], wantSonnet, "post-rotation sonnet-5")
	if _, stillPresent := got.Models["claude-opus-4-8"]; stillPresent {
		t.Fatalf("post-rotation opus-4-8 buckets still present (stale from truncated-away turns): %+v", got.Models["claude-opus-4-8"])
	}
}

// TestRateTable_SuffixTolerantMatch proves a date-suffixed production model ID (the real quirk:
// haiku ships as claude-haiku-4-5-20251001) resolves to — and prices identically to — its bare
// spec key claude-haiku-4-5, via both the exact bare key and the date-suffix strip.
func TestRateTable_SuffixTolerantMatch(t *testing.T) {
	rates := loadTestRates(t)
	const bare = "claude-haiku-4-5"
	const suffixed = "claude-haiku-4-5-20251001"

	bareRate, bareKey, bareOK := rates.Match(bare)
	if !bareOK || bareKey != bare {
		t.Fatalf("bare match: key=%q ok=%v, want %q true", bareKey, bareOK, bare)
	}
	sufRate, sufKey, sufOK := rates.Match(suffixed)
	if !sufOK {
		t.Fatalf("date-suffixed ID %q matched no rate — a production ID must not fall through", suffixed)
	}
	if sufKey != bare {
		t.Fatalf("date-suffixed ID matched key %q, want it to resolve to bare %q", sufKey, bare)
	}
	if sufRate != bareRate {
		t.Fatalf("date-suffixed rate %+v != bare rate %+v", sufRate, bareRate)
	}
	// And the priced cost is identical for the same buckets.
	b := wantBuckets(1000, 100, 50, 200, 300, 1)
	assertClose(t, b.Cost(sufRate), b.Cost(bareRate), "suffixed vs bare cost")
}

// TestParseByModel_CacheCreationSplitAndAbsent proves the per-model parser keeps the 5m/1h cache-
// write split apart (for the differentiated rates) when the nested cache_creation object is present,
// and — the T2 nil-guard — assigns the flat cache_creation_input_tokens wholesale to the 5m bucket
// without crashing when the nested object is absent.
func TestParseByModel_CacheCreationSplitAndAbsent(t *testing.T) {
	const split = `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":300,"cache_creation_input_tokens":900,"cache_read_input_tokens":40,"output_tokens":120,"cache_creation":{"ephemeral_5m_input_tokens":600,"ephemeral_1h_input_tokens":300}}}}
`
	models, _, err := ParseByModel(strings.NewReader(split))
	if err != nil {
		t.Fatalf("ParseByModel(split): %v", err)
	}
	assertBuckets(t, models["claude-sonnet-5"], wantBuckets(300, 600, 300, 40, 120, 1), "split cache_creation")

	const absent = `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":300,"cache_creation_input_tokens":700,"cache_read_input_tokens":40,"output_tokens":120}}}
`
	models, _, err = ParseByModel(strings.NewReader(absent))
	if err != nil {
		t.Fatalf("ParseByModel(absent): %v — an absent nested cache_creation object must not crash", err)
	}
	assertBuckets(t, models["claude-sonnet-5"], wantBuckets(300, 700, 0, 40, 120, 1), "absent nested cache_creation")
}

// TestParseByModel_PartialTrailingLineNotConsumed proves the byte watermark lands on a line
// boundary: a complete line followed by a partial (un-newlined) line reports only the complete
// line's bytes as consumed and folds only the complete line, so the partial line is re-parsed once
// completed on the next pass (no double-count, no lost turn).
func TestParseByModel_PartialTrailingLineNotConsumed(t *testing.T) {
	complete := `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":10}}}` + "\n"
	partial := `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":999`
	models, consumed, err := ParseByModel(strings.NewReader(complete + partial))
	if err != nil {
		t.Fatalf("ParseByModel: %v", err)
	}
	if consumed != int64(len(complete)) {
		t.Fatalf("consumed = %d, want %d (only the complete newline-terminated line)", consumed, len(complete))
	}
	assertBuckets(t, models["claude-sonnet-5"], wantBuckets(100, 0, 0, 0, 10, 1), "partial-trailing")
}

// TestAccounting_UnknownModelSurfaced proves an unrecognized model ID is surfaced in Unpriced (and
// excluded from CostByModel / CostUSD), never silently dropped or priced at $0 as if it were free.
func TestAccounting_UnknownModelSurfaced(t *testing.T) {
	rates := loadTestRates(t)
	const line = `{"type":"assistant","message":{"model":"totally-unknown-model","usage":{"input_tokens":1000,"cache_read_input_tokens":100,"output_tokens":200,"cache_creation":{"ephemeral_5m_input_tokens":50,"ephemeral_1h_input_tokens":0}}}}
` + `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}
`
	acct, err := Account(nil, []TranscriptSource{{FileID: "x", Reader: strings.NewReader(line)}}, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if len(acct.Unpriced) != 1 || acct.Unpriced[0] != "totally-unknown-model" {
		t.Fatalf("Unpriced = %v, want [totally-unknown-model] surfaced", acct.Unpriced)
	}
	if _, priced := acct.CostByModel["totally-unknown-model"]; priced {
		t.Fatal("unknown model must not appear in CostByModel")
	}
	// Its buckets are still summed (tokens are real), but its cost is not invented.
	if acct.Models["totally-unknown-model"] == nil {
		t.Fatal("unknown model's token buckets must still be recorded")
	}
	// Total cost is exactly the known model's cost — the unknown contributes $0 but is flagged.
	assertClose(t, acct.CostUSD, acct.CostByModel["claude-sonnet-5"], "cost excludes unknown model")
}

// TestAccounting_LedgerRoundTripsAcrossProcessBoundary is a regression pin for the rotation fix's
// load-bearing assumption that the per-file Ledger the fix relies on actually survives a real resume
// (execution.json persisted to disk, process exits, a later invocation loads it back). The rotation
// fix was CLI-verified but never proven at the Go/JSON level that Ledger — an unexported-shape map of
// pointers — round-trips through encoding/json without losing entries or aliasing pointers across
// files. Without this, the ledger-replace-per-file logic Account depends on would silently degrade to
// the pre-fix accumulate-on-top bug the moment a real run resumes from disk.
func TestAccounting_LedgerRoundTripsAcrossProcessBoundary(t *testing.T) {
	rates := loadTestRates(t)
	pass1 := filepath.Join(accountingDir, "watermark", "session.pass1.jsonl")
	pass2 := filepath.Join(accountingDir, "watermark", "session.pass2.jsonl")
	const fileID = "session"

	// Build state in one "process": fresh parse of pass1.
	s1, h1 := openSource(t, fileID, pass1, 0)
	acct, err := Account(nil, []TranscriptSource{s1}, rates, "t")
	h1.Close()
	if err != nil {
		t.Fatalf("pass1 Account: %v", err)
	}

	// Cross the process boundary: marshal to JSON (what the CLI writes into execution.json) and
	// unmarshal into a fresh value (what the next invocation reads back) — no in-memory state carried.
	raw, err := json.Marshal(acct)
	if err != nil {
		t.Fatalf("marshal Accounting: %v", err)
	}
	var resumed Accounting
	if err := json.Unmarshal(raw, &resumed); err != nil {
		t.Fatalf("unmarshal Accounting: %v", err)
	}
	if resumed.Ledger == nil || resumed.Ledger[fileID] == nil {
		t.Fatalf("resumed.Ledger[%q] is nil after round-trip — per-file ledger lost across process boundary", fileID)
	}
	assertBuckets(t, resumed.Ledger[fileID]["claude-sonnet-5"], wantBuckets(15000, 4000, 3000, 3000, 1500, 2), "resumed ledger entry")
	if resumed.Watermarks[fileID] != 1946 {
		t.Fatalf("resumed watermark = %d, want 1946", resumed.Watermarks[fileID])
	}

	// Second "process": incremental resume on top of the round-tripped state.
	s2, h2 := openSource(t, fileID, pass2, resumed.Watermarks[fileID])
	got, err := Account(&resumed, []TranscriptSource{s2}, rates, "t")
	h2.Close()
	if err != nil {
		t.Fatalf("post-resume incremental Account: %v", err)
	}

	// Must equal a fresh full parse of pass2 — not lost (ledger forgotten -> only the delta) and not
	// doubled (ledger duplicated -> pass1 counted twice).
	fs, fh := openSource(t, fileID, pass2, 0)
	fresh, err := Account(nil, []TranscriptSource{fs}, rates, "t")
	fh.Close()
	if err != nil {
		t.Fatalf("fresh pass2 Account: %v", err)
	}
	for model, want := range fresh.Models {
		assertBuckets(t, got.Models[model], *want, "post-resume vs fresh "+model)
	}
	assertClose(t, got.CostUSD, fresh.CostUSD, "post-resume vs fresh total cost")
	if got.Watermarks[fileID] != 3924 {
		t.Fatalf("post-resume watermark = %d, want 3924", got.Watermarks[fileID])
	}
}

// TestAccounting_LegacyLedgerMigrationPreservesAggregate is a regression pin for resuming a
// pre-ledger on-disk Accounting (the shape execution.json carried before this fix: Models +
// Watermarks summed session-wide, no per-file Ledger). This was the ONLY path CLI-verified by the
// engineer; it was never proven that a legacy snapshot's already-summed aggregate survives migration
// with no loss and that a new file's full-parse delta adds onto it exactly once (not lost under the
// migration, not double-counted against a re-derived Models).
func TestAccounting_LegacyLedgerMigrationPreservesAggregate(t *testing.T) {
	rates := loadTestRates(t)

	// A pre-fix on-disk Accounting: Models + Watermarks present, Ledger absent (the JSON key is
	// entirely missing, exactly as an old execution.json would deserialize — "ledger" has
	// `omitempty` so a legacy snapshot's JSON never carries the key at all).
	legacyJSON := `{
		"watermarks": {"legacy-orchestrator.jsonl": 999},
		"models": {
			"claude-sonnet-5": {"input_tokens":15000,"cache_write_5m_tokens":4000,"cache_write_1h_tokens":3000,"cache_read_tokens":3000,"output_tokens":1500,"turns":2},
			"claude-opus-4-8": {"input_tokens":10000,"cache_write_5m_tokens":2000,"cache_write_1h_tokens":1000,"cache_read_tokens":4500,"output_tokens":2500,"turns":2}
		},
		"cost_usd": 0.23865,
		"turns": 4
	}`
	var legacy Accounting
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("unmarshal legacy fixture: %v", err)
	}
	if legacy.Ledger != nil {
		t.Fatalf("test fixture invariant broken: legacy.Ledger must be nil (pre-fix shape), got %v", legacy.Ledger)
	}

	// Resume: fold in a NEW file's full parse (a real subagent transcript fixture) on top of the
	// legacy snapshot.
	newFile := filepath.Join(accountingDir, "subagents", "agent-agenta2.jsonl") // opus-4-8, 1 turn
	s, h := openSource(t, newFile, newFile, 0)
	got, err := Account(&legacy, []TranscriptSource{s}, rates, "t")
	h.Close()
	if err != nil {
		t.Fatalf("post-migration Account: %v", err)
	}

	// The prior aggregate must be preserved verbatim under the sentinel legacy ledger key — no loss.
	seeded := got.Ledger[legacyLedgerFileID]
	if seeded == nil {
		t.Fatalf("legacy aggregate not seeded under sentinel key %q", legacyLedgerFileID)
	}
	assertBuckets(t, seeded["claude-sonnet-5"], wantBuckets(15000, 4000, 3000, 3000, 1500, 2), "seeded legacy sonnet-5")
	assertBuckets(t, seeded["claude-opus-4-8"], wantBuckets(10000, 2000, 1000, 4500, 2500, 2), "seeded legacy opus-4-8")

	// The new file's own ledger entry holds only its own contribution.
	assertBuckets(t, got.Ledger[newFile]["claude-opus-4-8"], wantBuckets(2000, 500, 0, 1000, 400, 1), "new file ledger entry")

	// Session totals are legacy aggregate + new file delta, added exactly once (no loss, no double).
	assertBuckets(t, got.Models["claude-sonnet-5"], wantBuckets(15000, 4000, 3000, 3000, 1500, 2), "post-migration sonnet-5 (unchanged, no new sonnet turns)")
	assertBuckets(t, got.Models["claude-opus-4-8"], wantBuckets(12000, 2500, 1000, 5500, 2900, 3), "post-migration opus-4-8 (legacy + new file, summed once)")
	if got.Turns != 5 { // sonnet 2 (legacy) + opus 3 (legacy 2 + new file 1)
		t.Fatalf("post-migration turns = %d, want 5", got.Turns)
	}
}

// ---- transcript-only additive accounting identity + documentary note ----
//
// These tests pin the identity against the SAME golden fixtures already committed
// for subagent discovery: the identity closes on a real multi-transcript walk (residual ≤ T), and
// a classifier that misses one already-Ledgered, already-discovered subagent transcript
// (simulating exactly the mis-globbed-nested-transcript risk the additive-O rewrite exists to
// catch) surfaces the gap as an itemized residual, never a silent O inflation.

// TestIdentity_GoldenFixtureResidualWithinTolerance is the primary closure test: O +
// Σ(agent-*.jsonl) + fixed-subagents + residual reproduces session_total with |residual| ≤ T on the
// real testdata/accounting/ walk, when the known-subagent classification agrees with what Account
// actually folded in (the production case — the single discovery seam feeds both).
func TestIdentity_GoldenFixtureResidualWithinTolerance(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	subs, err := DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(subs) != 4 {
		t.Fatalf("discovered %d subagents, want 4", len(subs))
	}

	id := acct.ComputeIdentity(mainPath, subs, nil, rates)

	if id.CostStatus != "ok" {
		t.Fatalf("cost_status = %q, want %q (unclassified: %+v)", id.CostStatus, "ok", id.UnclassifiedTranscripts)
	}
	if math.Abs(id.ResidualUSD) > id.ToleranceUSD {
		t.Fatalf("residual %.10f exceeds tolerance %.10f", id.ResidualUSD, id.ToleranceUSD)
	}
	assertClose(t, id.SessionTotalUSD, acct.CostUSD, "identity session_total vs acct.CostUSD")
	o, ok := acct.PriceFile(mainPath, rates)
	if !ok {
		t.Fatal("PriceFile: main transcript not found in ledger")
	}
	assertClose(t, id.OUSD, o.CostUSD, "identity O vs PriceFile")
	assertClose(t, id.OUSD+id.VariableAgentsUSD+id.FixedSubagentsUSD+id.ResidualUSD, id.SessionTotalUSD,
		"closure: O + variable-agents + fixed-subagents + residual must equal session_total")
	if id.FixedSubagentsUSD != 0 {
		t.Fatalf("fixed_subagents_usd = %v, want 0 (no fixed-subagent classifier data supplied)", id.FixedSubagentsUSD)
	}
	if len(id.UnclassifiedTranscripts) != 0 {
		t.Fatalf("unexpected unclassified transcripts on the golden fixture: %+v", id.UnclassifiedTranscripts)
	}
}

// TestIdentity_SyntheticOverCountSurfacesAsItemizedResidualNotOInflation is the adversarial case (spike
// §1/§4): a classifier that MISSES one real, already-discovered, already-Ledgered subagent transcript
// — exactly a mis-globbed-nested-transcript defect — must surface that transcript's cost as an itemized
// residual (cost_status: residual-exceeded, unclassified_transcripts naming it), and must NEVER inflate
// O. This is what a subtractive O definition could never catch (spike §1's tautology) and what silently
// folding residual into O would hide.
func TestIdentity_SyntheticOverCountSurfacesAsItemizedResidualNotOInflation(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	subs, err := DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(subs) < 2 {
		t.Fatalf("need at least 2 discovered subagents to drop one, got %d", len(subs))
	}
	sort.Strings(subs)
	dropped := subs[0]
	knownMinusOne := subs[1:] // the classifier's blind spot: a REAL, already-Ledgered transcript it never names

	droppedEntry := acct.Ledger[dropped]
	if droppedEntry == nil {
		t.Fatalf("dropped path %q has no ledger entry — test fixture assumption broken", dropped)
	}
	_, droppedCost, _, _ := priceModels(droppedEntry, rates)
	if droppedCost <= 0 {
		t.Fatalf("dropped transcript's own cost must be > 0 for this test to be meaningful, got %v", droppedCost)
	}

	id := acct.ComputeIdentity(mainPath, knownMinusOne, nil, rates)

	if id.CostStatus != "residual-exceeded" {
		t.Fatalf("cost_status = %q, want %q", id.CostStatus, "residual-exceeded")
	}
	assertClose(t, id.ResidualUSD, droppedCost, "residual must equal exactly the unclassified transcript's own cost")
	if len(id.UnclassifiedTranscripts) != 1 || id.UnclassifiedTranscripts[0].Path != dropped {
		t.Fatalf("unclassified_transcripts = %+v, want exactly one entry for %q", id.UnclassifiedTranscripts, dropped)
	}
	assertClose(t, id.UnclassifiedTranscripts[0].CostUSD, droppedCost, "itemized cost on the unclassified transcript")

	// The acceptance-2 invariant: O must be IDENTICAL to its golden-fixture value — the leak must never
	// be silently folded into O, regardless of the classification gap.
	o, ok := acct.PriceFile(mainPath, rates)
	if !ok {
		t.Fatal("PriceFile: main transcript not found in ledger")
	}
	assertClose(t, id.OUSD, o.CostUSD, "O must be unchanged — a classification gap must never inflate O")
}

// TestSetAccounting_PersistsIdentity pins that record-usage's SetAccounting path (not just the raw
// ComputeIdentity method) computes and persists the identity onto RunConfig.Accounting.Identity,
// re-deriving the known-subagent set from the same DiscoverSubagentTranscripts seam — so a
// production run gets the identity for free, with no extra caller wiring.
func TestSetAccounting_PersistsIdentity(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef")

	id := ex.RunConfig.Accounting.Identity
	if id == nil {
		t.Fatal("SetAccounting did not persist the identity onto RunConfig.Accounting.Identity")
	}
	if id.CostStatus != "ok" {
		t.Fatalf("cost_status = %q, want %q (unclassified: %+v)", id.CostStatus, "ok", id.UnclassifiedTranscripts)
	}
	if math.Abs(id.ResidualUSD) > id.ToleranceUSD {
		t.Fatalf("residual %.10f exceeds tolerance %.10f", id.ResidualUSD, id.ToleranceUSD)
	}
	assertClose(t, id.OUSD, ex.RunConfig.Accounting.Orchestrator.CostUSD, "persisted identity O vs Orchestrator")
}

// TestRecordListVsActualNote_RendersRecordedActualBilledTotalAndDoesNotGate pins spike §5: the note
// renders the operator-entered status-line total against this run's transcript figures, and recording
// it never touches cost_status/CostUSD/etc. — documentary only, no pass/fail assertion (spike §5.2).
func TestRecordListVsActualNote_RendersRecordedActualBilledTotalAndDoesNotGate(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef")
	sessionTotal := ex.RunConfig.Accounting.CostUSD
	oUSD := ex.RunConfig.Accounting.Orchestrator.CostUSD
	const statusLine = 0.25 // deliberately different from sessionTotal — a real list-vs-actual gap

	if err := RecordListVsActualNote(&ex, statusLine, "operator@example.com", "2026-07-05T01:00:00Z"); err != nil {
		t.Fatalf("RecordListVsActualNote: %v", err)
	}

	notes := ex.RunConfig.Accounting.ListVsActualNotes
	if len(notes) != 1 {
		t.Fatalf("ListVsActualNotes = %d entries, want 1", len(notes))
	}
	n := notes[0]
	if n.StatusLineTotalUSD != statusLine {
		t.Fatalf("status_line_total_usd = %v, want %v", n.StatusLineTotalUSD, statusLine)
	}
	assertClose(t, n.TranscriptSessionTotalUSD, sessionTotal, "transcript_session_total_usd")
	assertClose(t, n.TranscriptOUSD, oUSD, "transcript_o_usd")
	assertClose(t, n.ListVsActualDeltaUSD, sessionTotal-statusLine, "list_vs_actual_delta_usd")
	assertClose(t, n.ListVsActualDeltaPct, (sessionTotal-statusLine)/statusLine*100, "list_vs_actual_delta_pct")
	if n.RateBasis != "list" {
		t.Fatalf("rate_basis = %q, want %q", n.RateBasis, "list")
	}
	if n.SpecsAsOf != "2026-07-03" {
		t.Fatalf("specs_as_of = %q, want %q", n.SpecsAsOf, "2026-07-03")
	}
	if n.CapturedBy != "operator@example.com" || n.CapturedAt != "2026-07-05T01:00:00Z" {
		t.Fatalf("provenance not recorded: captured_by=%q captured_at=%q", n.CapturedBy, n.CapturedAt)
	}
	if n.Note == "" {
		t.Fatal("note text must not be empty")
	}

	// Documentary only: recording the note must never mutate the accounting figures it compares
	// against, and must never set/flip any pass-fail status.
	if ex.RunConfig.Accounting.CostStatus != "" {
		t.Fatalf("cost_status = %q, want unchanged %q — the note must never gate the build", ex.RunConfig.Accounting.CostStatus, "")
	}
	if ex.RunConfig.Accounting.CostUSD != sessionTotal {
		t.Fatal("recording the note must never mutate the transcript cost figures")
	}

	found := false
	for _, l := range ex.Log {
		if strings.Contains(l, "documentary, does not gate the build") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a documentary, non-gating log line, got: %v", ex.Log)
	}
}

// TestRecordListVsActualNote_ErrorsWithoutPriorAccounting pins that the note requires a prior
// accounting snapshot to compare against (there is nothing documentary to record otherwise) — the
// ONLY error path; it is never triggered by the size of a list-vs-actual delta itself.
func TestRecordListVsActualNote_ErrorsWithoutPriorAccounting(t *testing.T) {
	var ex ExecState
	if err := RecordListVsActualNote(&ex, 1.0, "operator@example.com", "2026-07-05T00:00:00Z"); err == nil {
		t.Fatal("expected an error when no accounting snapshot has been recorded yet")
	}
}
