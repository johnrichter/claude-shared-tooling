package cost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// mainFixture is one orchestrator-authored assistant turn with a TTL-split cache write (line 1),
// a malformed line (line 2), a subagent-authored turn inlined with no separate file/name (line
// 3 — the unmappable case), and a turn using the roster's "[1m]" long-context-window selector
// plus a single tool_use block (line 4).
const mainFixture = `{"type":"assistant","sessionId":"sess-1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":300,"ephemeral_1h_input_tokens":0}}}}
{not valid json
{"type":"assistant","sessionId":"sess-1","isSidechain":true,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50}}}
{"type":"assistant","sessionId":"sess-1","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5[1m]","usage":{"input_tokens":10,"output_tokens":10},"content":[{"type":"tool_use","name":"Bash","input":{}}]}}
`

// agentFixture is one subagent-authored turn in its own file, attributed via TranscriptMeta.Agent
// rather than Authorship.
const agentFixture = `{"type":"assistant","sessionId":"sess-1","isSidechain":true,"message":{"role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":200,"output_tokens":100}}}
`

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}

func TestSanityIngestRollupQueryIdentity(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFixture(t, dir, "main.jsonl", mainFixture)
	agentPath := writeFixture(t, dir, "agent-swe.jsonl", agentFixture)

	store, err := Open(filepath.Join(dir, "cost.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	source := transcript.ClaudeCodeJSONL{}

	sum, err := store.Ingest(source, mainPath, TranscriptMeta{Project: "proj", IsMain: true})
	if err != nil {
		t.Fatalf("Ingest(main): %v", err)
	}
	if sum.EventsIngested != 3 {
		t.Errorf("main EventsIngested = %d, want 3", sum.EventsIngested)
	}
	if sum.UnmappableEvents != 1 {
		t.Errorf("main UnmappableEvents = %d, want 1", sum.UnmappableEvents)
	}
	if sum.ErrorsFlagged != 1 {
		t.Errorf("main ErrorsFlagged = %d, want 1", sum.ErrorsFlagged)
	}

	if _, err := store.Ingest(source, agentPath, TranscriptMeta{Project: "proj", Agent: "software-engineer"}); err != nil {
		t.Fatalf("Ingest(agent): %v", err)
	}

	// Resumability: re-ingesting the same file must not re-price or double-count anything.
	resumed, err := store.Ingest(source, mainPath, TranscriptMeta{Project: "proj", IsMain: true})
	if err != nil {
		t.Fatalf("re-Ingest(main): %v", err)
	}
	if resumed.EventsIngested != 0 || resumed.ErrorsFlagged != 0 {
		t.Errorf("re-Ingest(main) = %+v, want a fully-skipped resume", resumed)
	}
	if resumed.ResumedFromLine != 4 {
		t.Errorf("re-Ingest(main).ResumedFromLine = %d, want 4", resumed.ResumedFromLine)
	}

	// The [1m] long-context selector must resolve the same rate as the bare model id, never
	// fail and never a different (mispriced) rate; the raw observed model id is still what gets
	// stored, so historical reads know exactly which window tier was in play.
	events, err := store.Query(QueryFilter{Project: "proj", Tool: "Bash"})
	if err != nil {
		t.Fatalf("Query(tool=Bash): %v", err)
	}
	if len(events) != 1 || events[0].ModelID != "claude-sonnet-5[1m]" {
		t.Fatalf("Query(tool=Bash) = %+v, want one event with raw model id claude-sonnet-5[1m]", events)
	}
	wantInput := moneyFromTokens(10, 3.0)
	wantOutput := moneyFromTokens(10, 15.0)
	if events[0].Amounts.Input != wantInput || events[0].Amounts.Output != wantOutput {
		t.Errorf("[1m] event priced as input=%d output=%d, want the bare claude-sonnet-5 rate input=%d output=%d",
			events[0].Amounts.Input, events[0].Amounts.Output, wantInput, wantOutput)
	}

	report, err := store.Identity(QueryFilter{Project: "proj"}, 0, 0)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	identityTotal := report.Orchestrator + sumMoney(report.Agents) + report.Fixed + report.Residual
	if identityTotal != report.Total {
		t.Errorf("additive identity: total=%d orchestrator+agents+fixed+residual=%d", report.Total, identityTotal)
	}
	if !report.WithinTolerance {
		t.Errorf("Identity not within tolerance: diff=%d", report.Diff)
	}
	if report.Residual == 0 {
		t.Error("Residual = 0, want the unmappable inlined-sidechain turn's cost")
	}
	if len(report.Itemized) != 1 || report.Itemized[0].TranscriptPath != mainPath {
		t.Errorf("Itemized = %+v, want one entry for %s", report.Itemized, mainPath)
	}
	if report.Agents["software-engineer"] == 0 {
		t.Error("Agents[software-engineer] = 0, want the agent transcript's cost")
	}

	split, err := EvenSplit(report, []string{"software-engineer", "test-engineer"})
	if err != nil {
		t.Fatalf("EvenSplit: %v", err)
	}
	if split["software-engineer"]+split["test-engineer"] != report.Residual {
		t.Errorf("EvenSplit total = %d, want %d", split["software-engineer"]+split["test-engineer"], report.Residual)
	}

	for _, dim := range []Dimension{DimSession, DimProject, DimAgent, DimTool, DimError} {
		rows, err := store.Rollup(QueryFilter{Project: "proj"}, dim)
		if err != nil {
			t.Fatalf("Rollup(%s): %v", dim, err)
		}
		var sum Money
		for _, r := range rows {
			sum += r.Cost
		}
		if sum != report.Total {
			t.Errorf("Rollup(%s) sums to %d, want %d", dim, sum, report.Total)
		}
	}

	sanity, err := store.SanityCheck(QueryFilter{Project: "proj"}, 10.0, 0.9)
	if err != nil {
		t.Fatalf("SanityCheck: %v", err)
	}
	if !sanity.WithinTolerance {
		t.Errorf("SanityCheck out of tolerance: precise=%d estimate=%d delta=%f", sanity.PreciseTotal, sanity.EstimatedTotal, sanity.RelativeDelta)
	}

	history, err := store.RateHistory("claude-sonnet-5")
	if err != nil {
		t.Fatalf("RateHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("RateHistory(claude-sonnet-5) = %d entries, want 1", len(history))
	}
	repriced, err := store.Reprice(events, history[0].AsOf)
	if err != nil {
		t.Fatalf("Reprice: %v", err)
	}
	if repriced != events[0].Total {
		t.Errorf("Reprice at the same observed rate = %d, want %d (unchanged)", repriced, events[0].Total)
	}
}

func TestSanityMoneyFromTokensRoundsToNearestMicroDollar(t *testing.T) {
	got := moneyFromTokens(1, 3.0) // 1 token at $3/million = 0.000003 USD = 3 micro-USD.
	if got != 3 {
		t.Errorf("moneyFromTokens(1, 3.0) = %d, want 3", got)
	}
}
