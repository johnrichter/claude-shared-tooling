package bh

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDeliverableKindResolveAndValidate(t *testing.T) {
	if KindCode.Resolve() != KindCode || DeliverableKind("").Resolve() != KindCode {
		t.Fatal("empty/code kind must resolve to code")
	}
	if KindDocs.Resolve() != KindDocs {
		t.Fatal("docs kind must resolve to docs")
	}
	// A bad kind is a validation error; a valid one and the omitted default both pass.
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Kind = "report" // invalid
	if res := ValidatePlanBytes(mustBytes(t, p)); res.OK {
		t.Fatal("validate should reject deliverable_kind 'report'")
	}
	p.Milestones[0].Phases[0].Tasks[0].Kind = KindDocs
	if res := ValidatePlanBytes(mustBytes(t, p)); !res.OK {
		t.Fatalf("validate should accept deliverable_kind 'docs': %v", res.Errors)
	}
}

func TestInitExecCarriesKindAndZeroTokens(t *testing.T) {
	p := validPlan()
	p.Milestones[0].Phases[0].Tasks[0].Kind = KindDocs // T1 docs; T2 omitted -> code
	ex, err := InitExec(p, InitExecOptions{Slug: "demo", At: "2026-06-27T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Tasks[0].Kind != KindDocs {
		t.Errorf("T1 kind = %q, want docs", ex.Tasks[0].Kind)
	}
	if ex.Tasks[1].Kind != KindCode {
		t.Errorf("T2 kind = %q, want code (default)", ex.Tasks[1].Kind)
	}
	if ex.RunConfig.TokensOut != 0 {
		t.Errorf("fresh run tokens_out = %d, want 0", ex.RunConfig.TokensOut)
	}
}

func TestRecordAccruesTokens(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: "2026-06-27T00:00:00Z"})
	tok1, tok2 := int64(1200), int64(800)
	if err := RecordTask(&ex, "M1.P1.T1", RecordFields{TokensOut: &tok1}, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := RecordTask(&ex, "M1.P1.T2", RecordFields{TokensOut: &tok2}, "t2"); err != nil {
		t.Fatal(err)
	}
	if ex.RunConfig.TokensOut != 2000 {
		t.Errorf("cumulative tokens_out = %d, want 2000 (recomputed, not hand-summed)", ex.RunConfig.TokensOut)
	}
	if ex.Tasks[0].TokensOut != 1200 {
		t.Errorf("T1 tokens_out = %d, want 1200", ex.Tasks[0].TokensOut)
	}
}

func TestSetUsageRecordsTrueTotals(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: "2026-06-27T00:00:00Z"})
	SetUsage(&ex, Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10, CacheCreationTokens: 5, TotalTokens: 165, Turns: 3}, "now")
	if ex.RunConfig.TrueUsage == nil || ex.RunConfig.TrueUsage.TotalTokens != 165 {
		t.Fatal("SetUsage must record the true-usage snapshot")
	}
	if ex.RunConfig.TrueUsage.At != "now" {
		t.Errorf("snapshot At = %q, want now", ex.RunConfig.TrueUsage.At)
	}
	if len(ex.Log) == 0 || !strings.Contains(ex.Log[len(ex.Log)-1], "true-usage") {
		t.Error("SetUsage must append a true-usage log line")
	}
}

func TestParseTranscriptUsage(t *testing.T) {
	// Two assistant turns (usage nested under message, the common shape) + one subagent turn
	// (top-level usage) + a no-usage line that must be skipped. Sums across all three turns.
	jsonl := strings.Join([]string{
		`{"type":"assistant","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}}`,
		`{"type":"user","message":{"content":"no usage here"}}`,
		`{"type":"assistant","parent_tool_use_id":"abc","usage":{"input_tokens":5,"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"output_tokens":3}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":7}}}`,
		`not even json — tolerated`,
	}, "\n")
	u, err := ParseTranscriptUsage(strings.NewReader(jsonl))
	if err != nil {
		t.Fatal(err)
	}
	if u.Turns != 3 {
		t.Errorf("turns = %d, want 3 (skips the no-usage and malformed lines)", u.Turns)
	}
	if u.InputTokens != 115 || u.OutputTokens != 50 || u.CacheCreationTokens != 21 || u.CacheReadTokens != 32 {
		t.Errorf("sums wrong: in=%d out=%d cc=%d cr=%d", u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens)
	}
	if u.TotalTokens != 115+50+21+32 {
		t.Errorf("total = %d, want %d", u.TotalTokens, 115+50+21+32)
	}
}

// ---- schema-version migration (M2.P2.T1) ----

// legacyExecJSON is a pre-M2 execution.json shape: no schema_version key at all, one done task
// with a commit SHA + cost + verdicts, one not-started task with a dep, a populated run_config
// and log. Every field here must survive MigrateExec unchanged.
const legacyExecJSON = `{
  "schema": "execution-state/v1",
  "project": "demo",
  "name": "demo — Execution",
  "topic": "tooling",
  "goal": "demo goal",
  "provenance": {"design_updated": "2026-06-01T00:00:00Z", "plan_updated": "2026-06-01T00:00:00Z", "derived_at": "2026-06-01T00:00:00Z"},
  "started": "2026-06-01T00:00:00Z",
  "updated": "2026-06-01T00:10:00Z",
  "run_config": {
    "pause_mode": "phase", "budget": "$5.00", "budget_ceiling_usd": 5,
    "spent_usd": 0.27, "tokens_out": 1200, "rates": "list-price"
  },
  "tasks": [
    {"id": "M1.P1.T1", "summary": "first", "model": "claude-sonnet-4-6", "effort": "medium",
     "status": "done", "test": "PASS", "review": "ACCEPT", "commit": "a1b2c3d",
     "cost_usd": 0.27, "tokens_out": 1200, "updated": "2026-06-01T00:10:00Z"},
    {"id": "M1.P1.T2", "summary": "second", "model": "claude-haiku-4-5", "effort": "low",
     "status": "not-started", "cost_usd": 0, "tokens_out": 0, "updated": "2026-06-01T00:00:00Z"}
  ],
  "log": ["2026-06-01T00:10:00Z M1.P1.T1 → done test PASS review ACCEPT a1b2c3d $0.27 1200 out-tok"]
}`

func mustUnmarshalExec(t *testing.T, raw string) ExecState {
	t.Helper()
	var ex ExecState
	if err := json.Unmarshal([]byte(raw), &ex); err != nil {
		t.Fatalf("unmarshal exec: %v", err)
	}
	return ex
}

func TestMigrateExecLoadsLegacyFileLossless(t *testing.T) {
	ex := mustUnmarshalExec(t, legacyExecJSON)
	if ex.SchemaVersion != 0 {
		t.Fatalf("legacy fixture must have no schema_version, got %d", ex.SchemaVersion)
	}
	if err := MigrateExec(&ex); err != nil {
		t.Fatalf("MigrateExec on legacy file: %v", err)
	}
	if ex.SchemaVersion != CurrentExecSchemaVersion {
		t.Fatalf("post-migrate version = %d, want %d", ex.SchemaVersion, CurrentExecSchemaVersion)
	}
	// every existing field preserved.
	if ex.Project != "demo" || ex.Goal != "demo goal" || ex.RunConfig.SpentUSD != 0.27 || ex.RunConfig.TokensOut != 1200 {
		t.Fatalf("run_config/top-level fields lost on migrate: %+v", ex)
	}
	if len(ex.Tasks) != 2 {
		t.Fatalf("tasks lost on migrate: %+v", ex.Tasks)
	}
	t1 := ex.Tasks[0]
	if t1.Status != StatusDone || t1.Commit != "a1b2c3d" || t1.CostUSD != 0.27 || t1.Test != "PASS" || t1.Review != "ACCEPT" {
		t.Fatalf("done task fields (SC4 lossless) not preserved: %+v", t1)
	}
	if len(ex.Log) != 1 {
		t.Fatalf("log lost on migrate: %v", ex.Log)
	}
	// new (M2) fields default safely absent — nil ledger is the accounting package's tolerated case.
	if ex.RunConfig.Accounting != nil {
		t.Fatalf("legacy file must not fabricate an accounting snapshot, got %+v", ex.RunConfig.Accounting)
	}
}

func TestMigrateExecUpgradeSurvivesSaveRoundTrip(t *testing.T) {
	ex := mustUnmarshalExec(t, legacyExecJSON)
	if err := MigrateExec(&ex); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded ExecState
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.SchemaVersion != CurrentExecSchemaVersion {
		t.Fatalf("save round-trip lost stamped version: got %d, want %d", reloaded.SchemaVersion, CurrentExecSchemaVersion)
	}
	if reloaded.Tasks[0].Commit != "a1b2c3d" || reloaded.Tasks[0].CostUSD != 0.27 {
		t.Fatalf("save round-trip lost done-task data: %+v", reloaded.Tasks[0])
	}
}

// TestMigrateExecPreservesNextBatchDeterminism is the SC4 no-regression guard: a resumed legacy
// file must yield the exact same next/batch scheduling decision as the same state would pre-upgrade
// (migrate only stamps schema_version — it must never touch task status/deps/order).
func TestMigrateExecPreservesNextBatchDeterminism(t *testing.T) {
	p := validPlan() // T1 -> T2 dep chain, matches the legacy fixture's task IDs/shape
	before := mustUnmarshalExec(t, legacyExecJSON)
	after := mustUnmarshalExec(t, legacyExecJSON)
	if err := MigrateExec(&after); err != nil {
		t.Fatal(err)
	}
	nextBefore := NextTask(before, p)
	nextAfter := NextTask(after, p)
	if !reflect.DeepEqual(nextBefore, nextAfter) {
		t.Fatalf("next differs after migrate: before=%+v after=%+v", nextBefore, nextAfter)
	}
	batchBefore := BatchTasks(before, p, 4)
	batchAfter := BatchTasks(after, p, 4)
	if !reflect.DeepEqual(batchBefore, batchAfter) {
		t.Fatalf("batch differs after migrate: before=%+v after=%+v", batchBefore, batchAfter)
	}
}

func TestMigrateExecCurrentVersionRoundTripsIdentical(t *testing.T) {
	p := validPlan()
	ex, _ := InitExec(p, InitExecOptions{Slug: "demo", At: at0})
	_ = RecordTask(&ex, "M1.P1.T1", RecordFields{Status: ptr(StatusDone), Test: ptrS("PASS"), Review: ptrS("ACCEPT"), Commit: ptrS("deadbee"), Cost: ptrF(0.5)}, at0)
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := mustUnmarshalExec(t, string(raw))
	if err := MigrateExec(&reloaded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ex, reloaded) {
		t.Fatalf("current-version save->load->migrate round-trip not identical:\nbefore=%+v\nafter=%+v", ex, reloaded)
	}
}

func TestMigrateExecRejectsFutureVersion(t *testing.T) {
	ex := mustUnmarshalExec(t, legacyExecJSON)
	ex.SchemaVersion = CurrentExecSchemaVersion + 1
	if err := MigrateExec(&ex); err == nil {
		t.Fatal("MigrateExec must reject a schema_version newer than this build supports")
	}
}

func TestCommaInt(t *testing.T) {
	cases := map[int64]string{0: "0", 42: "42", 999: "999", 1000: "1,000", 1234567: "1,234,567", -2500: "-2,500"}
	for in, want := range cases {
		if got := commaInt(in); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", in, got, want)
		}
	}
}
