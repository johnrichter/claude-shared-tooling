package bandcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// Planted is the artifact ProbeFiring writes to disk before driving a `claude -p` run against
// it: a real file, at a real path, for a real governed hook to evaluate — never a synthetic
// in-memory stand-in the hook never actually sees.
type Planted struct {
	Path    string
	Content []byte
}

// CommandRunner is the subprocess seam ProbeFiring calls through: sysops.Run in production, a
// fake in a test, so exercising plant/probe/observe never requires a live `claude` binary.
type CommandRunner func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error)

// SysopsRunner adapts sysops.Run to CommandRunner — the production seam.
func SysopsRunner(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
	return sysops.Run(ctx, name, args, opts)
}

// FiringObservation is what ProbeFiring established about one rule's real firing. Detected is
// false when the probe run's own transcript could not be resolved or streamed at all — reported
// as undetected, never folded into either Fired or not-fired.
type FiringObservation struct {
	GateID    string
	Fired     bool
	Detected  bool
	Reason    string
	RawStdout []byte
}

// ProbeFiring writes planted to disk, drives one `claude -p prompt` run through run, and
// observes whether gateID actually fired by streaming the run's own transcript (transcriptPath)
// through src — never by re-reading the invariant registry or by re-running the static glob
// check. observe decides Fired/Detected from the parsed turns; it is the caller's job because a
// gate's actual observable signal (a tool denial, a specific stderr shape, an absent write) is
// rule-specific and this package does not hardcode one shape for every gate.
//
// This is a single-shot mechanism check on ONE rule's real firing, not a stand-in for the
// multi-turn behavioral tier that validates agentic dispatch or orchestration — SC-VALIDATION
// draws that line, not this function.
func ProbeFiring(
	ctx context.Context,
	run CommandRunner,
	src transcript.TranscriptSource,
	gateID string,
	planted Planted,
	prompt string,
	transcriptPath string,
	observe func(turns []transcript.Turn) (fired bool, reason string),
) (FiringObservation, error) {
	obs := FiringObservation{GateID: gateID}

	if err := os.MkdirAll(filepath.Dir(planted.Path), 0o755); err != nil {
		return obs, fmt.Errorf("bandcheck: plant %q: %w", planted.Path, err)
	}
	if err := os.WriteFile(planted.Path, planted.Content, 0o644); err != nil {
		return obs, fmt.Errorf("bandcheck: plant %q: %w", planted.Path, err)
	}

	res, err := run(ctx, "claude", []string{"-p", prompt}, sysops.Options{})
	if err != nil {
		return obs, fmt.Errorf("bandcheck: probe run for gate %q: %w", gateID, err)
	}
	obs.RawStdout = res.Stdout

	f, err := os.Open(transcriptPath)
	if err != nil {
		obs.Reason = fmt.Sprintf("probe transcript %q unreadable: %v -- firing undetected", transcriptPath, err)
		return obs, nil
	}
	defer f.Close()

	var turns []transcript.Turn
	if err := src.Turns(f, func(t transcript.Turn) error {
		turns = append(turns, t)
		return nil
	}); err != nil {
		obs.Reason = fmt.Sprintf("probe transcript %q failed to stream: %v -- firing undetected", transcriptPath, err)
		return obs, nil
	}

	fired, reason := observe(turns)
	obs.Detected, obs.Fired, obs.Reason = true, fired, reason
	return obs, nil
}
