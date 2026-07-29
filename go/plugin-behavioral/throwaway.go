package plugin_behavioral

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ThrowawayMarker is the file every provisioned live-case working directory carries at its root
// the instant it exists -- the one thing this package's cleanup, and AssertThrowaway, trust to
// decide a directory is safe to remove or to run a mutating probe in.
const ThrowawayMarker = ".plugin-behavioral-throwaway"

// Throwaway is one provisioned, marker-carrying working directory a live (KindProbe) case runs
// in.
type Throwaway struct {
	Dir string
}

type throwawayMarker struct {
	RunID     string `json:"run_id"`
	PID       int    `json:"pid"`
	CreatedAt string `json:"created_at"`
}

// IsThrowaway reports whether dir carries the marker at its root.
func IsThrowaway(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ThrowawayMarker))
	return err == nil && !info.IsDir()
}

// AssertThrowaway fails loud unless dir carries the marker -- the defense-in-depth check a live
// case re-runs right before use, on top of Provision's own guarantee, so a path swapped out from
// under a Throwaway can never widen a mutating probe's blast radius. context, when non-empty, is
// folded into the error so a refusal names what it refused, not just where.
func AssertThrowaway(dir, context string) error {
	if IsThrowaway(dir) {
		return nil
	}
	label := ""
	if context != "" {
		label = " for " + context
	}
	return fmt.Errorf("plugin-behavioral: refusing to use non-throwaway path%s: %s (missing marker %s)", label, dir, ThrowawayMarker)
}

// Provision creates one marked, disposable directory for a live case: mkdtemp, then the marker
// written the instant the directory exists, then seed (if non-nil) to lay down whatever
// case-specific content it needs (a git repo, fixture files). seed runs with the marker already
// in place, so anything it does inside the directory is still covered by Cleanup's guard; a
// seed failure removes the directory and returns the error rather than handing back a
// half-seeded Throwaway.
func Provision(runID string, seed func(dir string) error) (*Throwaway, error) {
	dir, err := os.MkdirTemp("", "plugin-behavioral-throwaway-")
	if err != nil {
		return nil, fmt.Errorf("plugin-behavioral: create throwaway dir: %w", err)
	}
	marker := throwawayMarker{RunID: runID, PID: os.Getpid(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.Marshal(marker)
	if err != nil {
		return nil, fmt.Errorf("plugin-behavioral: marshal throwaway marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ThrowawayMarker), raw, 0o644); err != nil {
		return nil, fmt.Errorf("plugin-behavioral: write throwaway marker: %w", err)
	}
	if seed != nil {
		if err := seed(dir); err != nil {
			_ = Cleanup(dir)
			return nil, fmt.Errorf("plugin-behavioral: seed throwaway dir: %w", err)
		}
	}
	return &Throwaway{Dir: dir}, nil
}

// Cleanup removes dir, but only if it still carries the marker -- the same guard a caller that
// somehow swapped the path out from under a Throwaway can never bypass to widen this call's own
// removal scope. A dir missing its marker is left untouched and reported, never removed.
func Cleanup(dir string) error {
	if !IsThrowaway(dir) {
		return fmt.Errorf("plugin-behavioral: refusing to remove %s: missing marker %s", dir, ThrowawayMarker)
	}
	return os.RemoveAll(dir)
}
