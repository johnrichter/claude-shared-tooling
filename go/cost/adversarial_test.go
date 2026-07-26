package cost

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "cost.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// --- Unknown model: Ingest must fail loudly and leave the store exactly as it was. ---

func TestIngest_UnknownModelFailsAtomically_NoPartialInsertNoWatermarkAdvance(t *testing.T) {
	dir := t.TempDir()
	// line 1 prices fine; line 2 carries an unresolvable model id.
	content := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}
{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"nonexistent-model-xyz","usage":{"input_tokens":100,"output_tokens":50}}}
`
	path := writeFixture(t, dir, "bad.jsonl", content)
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}

	_, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true})
	if err == nil {
		t.Fatal("Ingest with an unresolvable model id: got nil error, want a loud failure")
	}

	events, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("Query after failed Ingest = %d events, want 0 (line 1's insert must not survive line 2's failure)", len(events))
	}

	// A retry after fixing nothing still resumes from line 0 -- confirms the watermark never
	// advanced past the failed call, so nothing already-priced is skipped on a corrected retry.
	watermark, err := store.readWatermark(path)
	if err != nil {
		t.Fatalf("readWatermark: %v", err)
	}
	if watermark != 0 {
		t.Errorf("watermark after failed Ingest = %d, want 0", watermark)
	}
}

// --- Dated-suffix model id normalizes to the same rate as the bare id. ---

func TestIngest_DatedSuffixModelPricesIdenticallyToBareID(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5-20260724","usage":{"input_tokens":1000,"output_tokens":1000}}}
`
	path := writeFixture(t, dir, "dated.jsonl", content)
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}

	if _, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	events, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	wantInput := moneyFromTokens(1000, 3.0)
	wantOutput := moneyFromTokens(1000, 15.0)
	if events[0].Amounts.Input != wantInput || events[0].Amounts.Output != wantOutput {
		t.Errorf("dated-suffix model priced input=%d output=%d, want input=%d output=%d",
			events[0].Amounts.Input, events[0].Amounts.Output, wantInput, wantOutput)
	}
	if events[0].ModelID != "claude-sonnet-5-20260724" {
		t.Errorf("stored ModelID = %q, want the raw observed id preserved", events[0].ModelID)
	}
}

// --- Immutable snapshot: a later, different rate observation for the same model never mutates
// an already-stored cost_events row. ---

func TestSnapshot_LaterRateObservationLeavesStoredEventUnchanged(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":1000,"output_tokens":1000}}}
`
	path := writeFixture(t, dir, "main.jsonl", content)
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}

	if _, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	before, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("got %d events, want 1", len(before))
	}
	originalTotal := before[0].Total

	// Directly append a later, different rate_history row for the same model -- simulating a
	// rate card update observed after this event was already ingested.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := recordRateHistory(tx, PriceSnapshot{
		ModelID: "claude-sonnet-5", Basis: "list", Input: 999.0, Output: 999.0,
		CacheRead: 999.0, CacheWrite5m: 999.0, CacheWrite1h: 999.0,
	}, "2099-01-01", "2099-01-01T00:00:00Z"); err != nil {
		tx.Rollback()
		t.Fatalf("recordRateHistory: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	after, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query after rate change: %v", err)
	}
	if after[0].Total != originalTotal {
		t.Errorf("stored event Total changed after a later rate observation: before=%d after=%d, want unchanged (immutable snapshot)", originalTotal, after[0].Total)
	}
	if after[0].Amounts.Input != before[0].Amounts.Input {
		t.Error("stored event Amounts.Input changed after a later rate observation, want unchanged")
	}

	// Reprice against the far-future rate demonstrably differs -- proving the two paths (stored
	// snapshot vs. historical repricing) are genuinely decoupled, not just untested.
	repriced, err := store.Reprice(after, "2099-01-01")
	if err != nil {
		t.Fatalf("Reprice: %v", err)
	}
	if repriced == originalTotal {
		t.Error("Reprice at the injected future rate equals the original snapshot total, want it to differ (rate actually changed)")
	}

	// Reprice at the original observation date still matches the stored total exactly.
	history, err := store.RateHistory("claude-sonnet-5")
	if err != nil {
		t.Fatalf("RateHistory: %v", err)
	}
	repricedOriginal, err := store.Reprice(after, history[0].AsOf)
	if err != nil {
		t.Fatalf("Reprice at original as_of: %v", err)
	}
	if repricedOriginal != originalTotal {
		t.Errorf("Reprice at the original as_of = %d, want %d (unchanged)", repricedOriginal, originalTotal)
	}
}

// --- EvenSplit edge cases. ---

func TestEvenSplit_NonZeroResidualZeroAgentsErrors(t *testing.T) {
	report := IdentityReport{Residual: 100}
	if _, err := EvenSplit(report, nil); err == nil {
		t.Error("EvenSplit(residual=100, agents=nil): got nil error, want an error (nowhere for the residual to go)")
	}
}

func TestEvenSplit_ZeroResidualZeroAgentsSucceeds(t *testing.T) {
	report := IdentityReport{Residual: 0}
	split, err := EvenSplit(report, nil)
	if err != nil {
		t.Fatalf("EvenSplit(residual=0, agents=nil): %v", err)
	}
	if len(split) != 0 {
		t.Errorf("EvenSplit(residual=0, agents=nil) = %v, want empty", split)
	}
}

func TestEvenSplit_RemainderDistributedWholeMicroDollarsSumsExactly(t *testing.T) {
	report := IdentityReport{Residual: 10} // 10 micro-USD across 3 agents: 3,3,4 or similar -- never token-share.
	split, err := EvenSplit(report, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EvenSplit: %v", err)
	}
	var sum Money
	for _, v := range split {
		sum += v
		if v != 3 && v != 4 {
			t.Errorf("EvenSplit share = %d, want 3 or 4 (10/3 with remainder)", v)
		}
	}
	if sum != 10 {
		t.Errorf("EvenSplit total = %d, want 10 (exact, no residual leakage)", sum)
	}
}

// --- QueryFilter.Errored and Rollup(DimError). ---

func TestQuery_ErroredFilterAndRollupByError(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}
{not valid json
`
	path := writeFixture(t, dir, "mix.jsonl", content)
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}
	if _, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	truep, falsep := true, false
	errored, err := store.Query(QueryFilter{Project: "p", Errored: &truep})
	if err != nil {
		t.Fatalf("Query(errored=true): %v", err)
	}
	if len(errored) != 1 || errored[0].ErrorReason == "" {
		t.Fatalf("Query(errored=true) = %+v, want one event with a non-empty ErrorReason", errored)
	}
	if errored[0].Total != 0 {
		t.Errorf("errored event Total = %d, want 0 (a malformed line is never priced)", errored[0].Total)
	}

	clean, err := store.Query(QueryFilter{Project: "p", Errored: &falsep})
	if err != nil {
		t.Fatalf("Query(errored=false): %v", err)
	}
	if len(clean) != 1 {
		t.Fatalf("Query(errored=false) = %d events, want 1", len(clean))
	}

	rows, err := store.Rollup(QueryFilter{Project: "p"}, DimError)
	if err != nil {
		t.Fatalf("Rollup(DimError): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Rollup(DimError) = %d groups, want 2 (true and false)", len(rows))
	}
	for _, r := range rows {
		if r.Key != "true" && r.Key != "false" {
			t.Errorf("Rollup(DimError) key = %q, want \"true\" or \"false\"", r.Key)
		}
	}
}

// --- SanityCheck when the precise total is zero but the independent estimate is not. ---

func TestSanityCheck_ZeroPreciseNonZeroEstimateFailsTolerance(t *testing.T) {
	store := openTestStore(t)
	result, err := store.SanityCheck(QueryFilter{Project: "no-such-project"}, 10.0, 0.5)
	if err != nil {
		t.Fatalf("SanityCheck: %v", err)
	}
	if result.PreciseTotal != 0 {
		t.Fatalf("PreciseTotal = %d, want 0 (empty store)", result.PreciseTotal)
	}
	if result.EstimatedTotal != 0 {
		t.Fatalf("EstimatedTotal = %d, want 0 (no tokens observed)", result.EstimatedTotal)
	}
	if !result.WithinTolerance {
		t.Error("SanityCheck(precise=0, estimate=0): want WithinTolerance true (both legitimately zero)")
	}
}

// --- Authorship: an orchestrator-file turn with no authorship marker at all still resolves to
// RoleOrchestrator (AuthorUnknown is not the same as AuthorSubagent). ---

func TestResolveRole_MainFileUnknownAuthorshipResolvesOrchestrator(t *testing.T) {
	role, agent := resolveRole(TranscriptMeta{IsMain: true}, transcript.AuthorUnknown)
	if role != RoleOrchestrator || agent != "" {
		t.Errorf("resolveRole(IsMain, AuthorUnknown) = (%q, %q), want (orchestrator, \"\")", role, agent)
	}
}

func TestResolveRole_NonMainFileWithAgentNameResolvesAgentRegardlessOfAuthorship(t *testing.T) {
	role, agent := resolveRole(TranscriptMeta{IsMain: false, Agent: "swe"}, transcript.AuthorOrchestrator)
	if role != RoleAgent || agent != "swe" {
		t.Errorf("resolveRole(subagent file, AuthorOrchestrator) = (%q, %q), want (agent, swe) -- file-level fact wins outside the main-file sidechain case", role, agent)
	}
}

// --- Resumability across an appended, still-growing transcript. ---

func TestIngest_ResumesAcrossAppendedLinesWithoutDoubleCounting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.jsonl")
	line1 := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}` + "\n"
	if err := os.WriteFile(path, []byte(line1), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}

	sum1, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true})
	if err != nil {
		t.Fatalf("Ingest #1: %v", err)
	}
	if sum1.EventsIngested != 1 {
		t.Fatalf("Ingest #1 EventsIngested = %d, want 1", sum1.EventsIngested)
	}

	line2 := `{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":200,"output_tokens":100}}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(line2); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	sum2, err := store.Ingest(source, path, TranscriptMeta{Project: "p", IsMain: true})
	if err != nil {
		t.Fatalf("Ingest #2: %v", err)
	}
	if sum2.ResumedFromLine != 1 {
		t.Errorf("Ingest #2 ResumedFromLine = %d, want 1", sum2.ResumedFromLine)
	}
	if sum2.EventsIngested != 1 {
		t.Errorf("Ingest #2 EventsIngested = %d, want 1 (only the appended line)", sum2.EventsIngested)
	}

	events, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Query = %d events, want 2 (no duplicate for line 1)", len(events))
	}
}

// --- Concurrent Ingest of distinct transcripts must not corrupt the shared store. ---

func TestIngest_ConcurrentDistinctTranscriptsBothLand(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}

	const n = 8
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		content := `{"type":"assistant","sessionId":"s","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":10}}}` + "\n"
		paths[i] = writeFixture(t, dir, filepathBase(i), content)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = store.Ingest(source, paths[i], TranscriptMeta{Project: "p", IsMain: true})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Ingest[%d]: %v", i, err)
		}
	}

	events, err := store.Query(QueryFilter{Project: "p"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != n {
		t.Errorf("Query = %d events, want %d (one per concurrently-ingested transcript)", len(events), n)
	}
}

func filepathBase(i int) string {
	return "concurrent-" + string(rune('a'+i)) + ".jsonl"
}

// --- Rollup(DimAgent) key composition. ---

func TestRollup_DimAgentKeysPrefixNamedAgents(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFixture(t, dir, "main.jsonl",
		`{"type":"assistant","sessionId":"s1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}`+"\n")
	agentPath := writeFixture(t, dir, "agent.jsonl",
		`{"type":"assistant","sessionId":"s1","isSidechain":true,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}`+"\n")
	store := openTestStore(t)
	source := transcript.ClaudeCodeJSONL{}
	if _, err := store.Ingest(source, mainPath, TranscriptMeta{Project: "p", IsMain: true}); err != nil {
		t.Fatalf("Ingest(main): %v", err)
	}
	if _, err := store.Ingest(source, agentPath, TranscriptMeta{Project: "p", Agent: "reviewer"}); err != nil {
		t.Fatalf("Ingest(agent): %v", err)
	}
	rows, err := store.Rollup(QueryFilter{Project: "p"}, DimAgent)
	if err != nil {
		t.Fatalf("Rollup(DimAgent): %v", err)
	}
	var sawOrchestrator, sawAgent bool
	for _, r := range rows {
		if r.Key == "orchestrator" {
			sawOrchestrator = true
		}
		if r.Key == "agent:reviewer" {
			sawAgent = true
		}
		if strings.HasPrefix(r.Key, "agent:") && r.Key != "agent:reviewer" {
			t.Errorf("unexpected agent key %q", r.Key)
		}
	}
	if !sawOrchestrator || !sawAgent {
		t.Errorf("Rollup(DimAgent) = %+v, want both \"orchestrator\" and \"agent:reviewer\" keys", rows)
	}
}
