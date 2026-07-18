package main

// Black-box CLI coverage for the `record` command's --input-tokens/
// --cache-write-tokens/--cache-read-tokens/--usage-turns flags, which populate ExecTask.Usage
// end-to-end through the actual binary (not just the bh package's pure functions).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCLI compiles the build-helpers binary once for this test file's cases and returns its path.
func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "build-helpers-test-bin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// minimalPlanJSON is the smallest valid plan.json init-exec accepts, with exactly one task.
const minimalPlanJSON = `{
  "goal": "g",
  "success_criteria": ["ok"],
  "milestones": [
    {"id": "M1", "name": "m1", "phases": [
      {"id": "M1.P1", "name": "p1", "tasks": [
        {"id": "M1.P1.T1", "name": "t1", "summary": "s", "deliverable": "d", "model": "claude-sonnet-5", "effort": "medium", "test_strategy": "unit", "acceptance": ["a"]}
      ]}
    ]}
  ]
}`

func runCLI(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
	}
	return out
}

func TestCLI_RecordUsageFlags_PopulateAllFourTokenClasses(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(minimalPlanJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	execOut := runCLI(t, bin, "init-exec", planPath, "--slug", "demo", "--at", "2026-07-01T00:00:00Z")
	execPath := filepath.Join(dir, "execution.json")
	if err := os.WriteFile(execPath, execOut, 0o644); err != nil {
		t.Fatal(err)
	}

	recorded := runCLI(t, bin, "record", execPath, "M1.P1.T1",
		"--status", "done",
		"--cost", "0.42",
		"--tokens-out", "300", // output — same basis as legacy TokensOut
		"--input-tokens", "500",
		"--cache-write-tokens", "200",
		"--cache-read-tokens", "9000", // cache_read intentionally dominates output
		"--usage-turns", "4",
		"--at", "2026-07-01T00:05:00Z",
	)

	var ex struct {
		RunConfig struct {
			SpentUSD  float64 `json:"spent_usd"`
			TokensOut int64   `json:"tokens_out"`
			Usage     *struct {
				InputTokens         int64 `json:"input_tokens"`
				CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
				CacheReadTokens     int64 `json:"cache_read_input_tokens"`
				OutputTokens        int64 `json:"output_tokens"`
				TotalTokens         int64 `json:"total_tokens"`
				Turns               int64 `json:"turns"`
			} `json:"usage"`
		} `json:"run_config"`
		Tasks []struct {
			ID        string `json:"id"`
			TokensOut int64  `json:"tokens_out"`
			Usage     *struct {
				InputTokens     int64 `json:"input_tokens"`
				CacheReadTokens int64 `json:"cache_read_input_tokens"`
				OutputTokens    int64 `json:"output_tokens"`
				TotalTokens     int64 `json:"total_tokens"`
			} `json:"usage"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(recorded, &ex); err != nil {
		t.Fatalf("cannot parse record output: %v\n%s", err, recorded)
	}

	if len(ex.Tasks) != 1 {
		t.Fatalf("expected exactly 1 task row, got %d", len(ex.Tasks))
	}
	task := ex.Tasks[0]
	if task.TokensOut != 300 {
		t.Fatalf("backward-compat tokens_out = %d, want 300", task.TokensOut)
	}
	if task.Usage == nil {
		t.Fatal("per-task usage missing after CLI record with token flags")
	}
	if task.Usage.InputTokens != 500 || task.Usage.CacheReadTokens != 9000 || task.Usage.OutputTokens != 300 {
		t.Fatalf("per-task usage mismatch: %+v", task.Usage)
	}
	if task.Usage.TotalTokens != 500+200+9000+300 {
		t.Fatalf("per-task total_tokens = %d, want %d", task.Usage.TotalTokens, 500+200+9000+300)
	}
	if task.Usage.CacheReadTokens <= task.Usage.OutputTokens {
		t.Fatal("fixture invariant broken: cache_read must dominate output")
	}

	// Per-run totals: legacy fields intact AND the new four-class Usage present, computed from
	// the (single) per-task row — never a hand-summed side channel.
	if ex.RunConfig.SpentUSD != 0.42 || ex.RunConfig.TokensOut != 300 {
		t.Fatalf("legacy run aggregates changed: spent=%v tokensOut=%v", ex.RunConfig.SpentUSD, ex.RunConfig.TokensOut)
	}
	if ex.RunConfig.Usage == nil {
		t.Fatal("run_config.usage missing after CLI record with token flags")
	}
	if ex.RunConfig.Usage.InputTokens != task.Usage.InputTokens ||
		ex.RunConfig.Usage.CacheReadTokens != task.Usage.CacheReadTokens ||
		ex.RunConfig.Usage.OutputTokens != task.Usage.OutputTokens ||
		ex.RunConfig.Usage.TotalTokens != task.Usage.TotalTokens {
		t.Fatalf("run usage (single task) must equal that task's usage exactly: run=%+v task=%+v", ex.RunConfig.Usage, task.Usage)
	}
}

func TestCLI_RecordWithoutUsageFlags_LeavesUsageAbsent(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(minimalPlanJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	execOut := runCLI(t, bin, "init-exec", planPath, "--slug", "demo", "--at", "2026-07-01T00:00:00Z")
	execPath := filepath.Join(dir, "execution.json")
	if err := os.WriteFile(execPath, execOut, 0o644); err != nil {
		t.Fatal(err)
	}

	recorded := runCLI(t, bin, "record", execPath, "M1.P1.T1",
		"--status", "done", "--cost", "0.10", "--at", "2026-07-01T00:05:00Z",
	)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(recorded, &probe); err != nil {
		t.Fatal(err)
	}
	var runConfig map[string]json.RawMessage
	if err := json.Unmarshal(probe["run_config"], &runConfig); err != nil {
		t.Fatal(err)
	}
	if _, present := runConfig["usage"]; present {
		t.Fatalf("run_config.usage should be absent (omitempty) when no task ever recorded usage, got: %s", recorded)
	}
}
