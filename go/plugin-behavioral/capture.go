package plugin_behavioral

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/adoption"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// probeMaxCaptureBytes bounds a probe's captured stdout/stderr -- generous enough for a full
// reply, still far short of unbounded.
const probeMaxCaptureBytes = 8 << 20 // 8 MiB

// ProbeRunner is the subprocess seam a KindProbe case invokes through: SysopsProbeRunner in
// production, a fixture-backed fake in a test, so exercising this package never spends real
// money or requires a live `claude` binary.
type ProbeRunner func(ctx context.Context, args []string, opts sysops.Options) (*sysops.Result, error)

// SysopsProbeRunner adapts sysops.Run to ProbeRunner -- the production seam, invoking the real
// `claude` binary on PATH.
func SysopsProbeRunner(ctx context.Context, args []string, opts sysops.Options) (*sysops.Result, error) {
	return sysops.Run(ctx, "claude", args, opts)
}

// probeResultJSON is the subset of `claude -p --output-format json`'s result object this package
// reads.
type probeResultJSON struct {
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
}

// ProbeObservation is everything a KindProbe case's ProbeClassifier needs to grade one captured
// trial: the raw reply text, the real spend, and (best-effort) the tool invocations its own
// session transcript recorded -- across the main transcript and any subagent fan-out.
type ProbeObservation struct {
	RawResult   string
	SpentUSD    float64
	SessionID   string
	Invocations []adoption.Invocation
}

var nonSlugChar = regexp.MustCompile(`[^A-Za-z0-9-]`)

// ProjectSlug maps a probe's working directory to Claude Code's on-disk project-directory slug:
// every character that is not a letter, digit or hyphen becomes a hyphen. dir is resolved to its
// real path first (symlinks followed) -- Claude Code records a session's transcript under the
// resolved cwd, so a symlinked parent (e.g. a throwaway under a symlinked temp root) would
// otherwise slugify to a directory Claude Code never wrote to. A dir this call cannot resolve
// falls back to its own cleaned, unresolved form -- transcript location is always best-effort.
func ProjectSlug(dir string) string {
	resolved := dir
	if abs, err := filepath.Abs(dir); err == nil {
		resolved = abs
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	return nonSlugChar.ReplaceAllString(filepath.Clean(resolved), "-")
}

// capture drives one live, single-shot probe invocation and assembles its ProbeObservation.
// Reserved for KindProbe cases -- a KindAgentic case is never graded through this function; see
// DispatchObserved.
func capture(ctx context.Context, run ProbeRunner, prompt, model, dir string, maxBudgetUSD float64, timeout time.Duration, source transcript.TranscriptSource, transcriptsRoot string) (ProbeObservation, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--model", model,
	}
	if maxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.4f", maxBudgetUSD))
	}

	res, err := run(ctx, args, sysops.Options{Dir: dir, Timeout: timeout, MaxStdoutBytes: probeMaxCaptureBytes})
	if err != nil {
		return ProbeObservation{}, fmt.Errorf("plugin-behavioral: probe invocation: %w", err)
	}
	if res.ExitCode != 0 {
		return ProbeObservation{}, fmt.Errorf("plugin-behavioral: probe exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}

	var pr probeResultJSON
	if err := json.Unmarshal(res.Stdout, &pr); err != nil {
		return ProbeObservation{}, fmt.Errorf("plugin-behavioral: probe result was not valid JSON: %w", err)
	}
	if pr.IsError {
		return ProbeObservation{}, fmt.Errorf("plugin-behavioral: probe reported is_error for its own run")
	}

	obs := ProbeObservation{RawResult: pr.Result, SpentUSD: pr.TotalCostUSD, SessionID: pr.SessionID}
	if source != nil && transcriptsRoot != "" && pr.SessionID != "" {
		// Best-effort: a transcript this package cannot locate or read never fails the probe
		// capture itself -- the classifier still has RawResult/SpentUSD to grade against.
		if invocations, err := adoption.LoadSessionInvocations(source, transcriptsRoot, ProjectSlug(dir), pr.SessionID); err == nil {
			obs.Invocations = invocations
		}
	}
	return obs, nil
}
