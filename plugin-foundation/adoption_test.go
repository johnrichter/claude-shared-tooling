package plugin_foundation

import (
	"os"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/adoption"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// TestIntegrationRoutingRulesClearThePhaseAGateOnFrozenFixtures is the build-acceptance bar for
// the foundation itself: a registry built from a plugin's own routing-rules.json (never a
// hand-written Go closure) classifies the frozen fixture transcript at or above the 80% gate for
// every operation it declares.
func TestIntegrationRoutingRulesClearThePhaseAGateOnFrozenFixtures(t *testing.T) {
	rules, err := LoadRoutingRulesFile("testdata/routing-rules.json")
	if err != nil {
		t.Fatalf("LoadRoutingRulesFile: %v", err)
	}
	registry := BuildRegistry(rules)

	source := transcript.ClaudeCodeJSONL{}
	invocations, err := adoption.LoadSessionInvocations(source, "testdata/transcripts", "proj", "session-a")
	if err != nil {
		t.Fatalf("LoadSessionInvocations: %v", err)
	}

	classifications := adoption.Classify(registry, invocations)
	rate, err := adoption.Rate(classifications, adoption.PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}

	status, ok := rate["status"]
	if !ok {
		t.Fatal(`rate["status"] missing`)
	}
	if status.CLICount != 4 || status.RawCount != 1 {
		t.Errorf("status counts = %d cli, %d raw, want 4, 1", status.CLICount, status.RawCount)
	}
	if !status.MetGate() {
		t.Errorf("status adoption rate %.2f%% should clear the %d%% gate", status.Rate*100, adoption.PhaseAStartGatePercent)
	}

	config, ok := rate["config"]
	if !ok {
		t.Fatal(`rate["config"] missing`)
	}
	if config.CLICount != 4 || config.RawCount != 1 {
		t.Errorf("config counts = %d cli, %d raw, want 4, 1", config.CLICount, config.RawCount)
	}
	if !config.MetGate() {
		t.Errorf("config adoption rate %.2f%% should clear the %d%% gate", config.Rate*100, adoption.PhaseAStartGatePercent)
	}

	for name, a := range rate {
		if a.Rate < 0.80 {
			t.Errorf("operation %q adopted its CLI at %.2f%%, below the 80%% Phase-A floor", name, a.Rate*100)
		}
	}

	report, err := adoption.BuildReport(classifications, nil, adoption.PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	result, err := report.Result([]string{"plugin-foundation", "adoption"})
	if err != nil {
		t.Fatalf("Report.Result: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("Result.Status = %q, want success (every operation cleared its gate)", result.Status)
	}
}

// TestIntegrationHardFloorNeverTradesOffAgainstRate proves the second half of the acceptance bar:
// a hook-eval log with a genuine tool-existence-denial floor violation fails the report
// regardless of how the transcript-side adoption rate otherwise measures, and a clean hook-eval
// log carries none.
func TestIntegrationHardFloorNeverTradesOffAgainstRate(t *testing.T) {
	clean := readHookEvalFixture(t, "testdata/hookeval/clean.jsonl")
	if v := adoption.CheckFloor(clean); len(v) != 0 {
		t.Errorf("CheckFloor(clean) = %v, want no violations", v)
	}

	broken := readHookEvalFixture(t, "testdata/hookeval/floor_violation.jsonl")
	violations := adoption.CheckFloor(broken)
	if len(violations) != 1 {
		t.Fatalf("CheckFloor(floor_violation) = %d violations, want 1", len(violations))
	}

	rules, err := LoadRoutingRulesFile("testdata/routing-rules.json")
	if err != nil {
		t.Fatalf("LoadRoutingRulesFile: %v", err)
	}
	registry := BuildRegistry(rules)
	source := transcript.ClaudeCodeJSONL{}
	invocations, err := adoption.LoadSessionInvocations(source, "testdata/transcripts", "proj", "session-a")
	if err != nil {
		t.Fatalf("LoadSessionInvocations: %v", err)
	}
	classifications := adoption.Classify(registry, invocations)

	report, err := adoption.BuildReport(classifications, broken, adoption.PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	result, err := report.Result([]string{"plugin-foundation", "adoption"})
	if err != nil {
		t.Fatalf("Report.Result: %v", err)
	}
	if result.Status != "precondition_unmet" {
		t.Errorf("Result.Status = %q, want precondition_unmet (floor violation governs even though adoption clears its gate)", result.Status)
	}
}

func readHookEvalFixture(t *testing.T, path string) []adoption.HookEvalRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	records, err := adoption.ReadHookEvalRecords(f)
	if err != nil {
		t.Fatalf("ReadHookEvalRecords(%s): %v", path, err)
	}
	return records
}
