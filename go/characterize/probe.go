package characterize

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// probeMaxCaptureBytes bounds a probe's captured stdout/stderr -- generous enough for a
// characterizing agent's full JSON reply, still far short of unbounded.
const probeMaxCaptureBytes = 8 << 20 // 8 MiB

// ProbeRunner is the subprocess seam a metered characterizing run calls through: SysopsProbeRunner
// in production, a fixture-backed fake in a test, so exercising this package never spends real
// money or requires a live `claude` binary.
type ProbeRunner func(ctx context.Context, args []string, opts sysops.Options) (*sysops.Result, error)

// SysopsProbeRunner adapts sysops.Run to ProbeRunner -- the production seam, invoking the real
// `claude` binary on PATH.
func SysopsProbeRunner(ctx context.Context, args []string, opts sysops.Options) (*sysops.Result, error) {
	return sysops.Run(ctx, "claude", args, opts)
}

// probeResult is the subset of `claude -p --output-format json`'s result object this package
// reads. Every other field is left unparsed.
type probeResult struct {
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
}

// buildProbeArgs composes one headless characterizing invocation: a print-mode prompt pinned to
// model, its per-run spend ceiling passed straight to the probe, and structured JSON output so
// this package never has to scrape a human-readable transcript for the cost figure.
func buildProbeArgs(prompt, model string, maxBudgetUSD float64) []string {
	return []string{
		"-p", prompt,
		"--output-format", "json",
		"--model", model,
		"--max-budget-usd", fmt.Sprintf("%.4f", maxBudgetUSD),
	}
}

// runProbe drives one metered characterizing invocation through run and parses its result JSON.
// It returns the parsed result and the run's raw stdout (kept for diagnostics even on a parse
// failure) or an error naming why the probe could not be trusted.
func runProbe(ctx context.Context, run ProbeRunner, prompt, model string, maxBudgetUSD float64, opts sysops.Options) (probeResult, []byte, error) {
	if opts.MaxStdoutBytes <= 0 {
		opts.MaxStdoutBytes = probeMaxCaptureBytes
	}
	res, err := run(ctx, buildProbeArgs(prompt, model, maxBudgetUSD), opts)
	if err != nil {
		return probeResult{}, nil, fmt.Errorf("characterize: probe invocation: %w", err)
	}
	if res.ExitCode != 0 {
		return probeResult{}, res.Stdout, fmt.Errorf("characterize: probe exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var pr probeResult
	if err := json.Unmarshal(res.Stdout, &pr); err != nil {
		return probeResult{}, res.Stdout, fmt.Errorf("characterize: probe result was not valid JSON: %w", err)
	}
	if pr.IsError {
		return pr, res.Stdout, fmt.Errorf("characterize: probe reported is_error for its own run")
	}
	return pr, res.Stdout, nil
}

// stripCodeFence removes a leading/trailing ```-fenced block a model sometimes wraps its JSON
// reply in despite being told to emit bare JSON, so parsing the reply never depends on the model
// having followed that instruction to the letter.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "```"), "```")
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		if tag := strings.TrimSpace(s[:nl]); tag != "" && !strings.ContainsAny(tag, "{[\"") {
			s = s[nl+1:] // drop a language tag (e.g. "json") on the fence's opening line
		}
	}
	return strings.TrimSpace(s)
}
