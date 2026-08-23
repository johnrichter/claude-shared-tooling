package githooks

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// DefaultMaxBytes is the default oversize threshold ScanRawBinary applies
// when a caller does not need a different one.
const DefaultMaxBytes int64 = 5 * 1024 * 1024

// LFSRouteChecker reports whether rel (a path relative to the scanned root)
// is routed through Git LFS - e.g. `git check-attr filter -- <path>`
// reporting the `lfs` filter. ScanRawBinary takes this as a parameter rather
// than shelling out to git itself, so the scan stays a pure function of its
// inputs (hermetic, deterministic, unit-testable without a git worktree).
type LFSRouteChecker func(rel string) (bool, error)

// executableMode is the permission bits git's checkout gives a tracked
// 100755 file (vs. 100644 for a non-executable one). Checking the checked-out
// file's own executable bit is a proxy for the tracked git mode that needs no
// git plumbing call, matching how core.fileMode-honoring checkouts work.
const executableMode = 0o111

// ScanRawBinary reports every candidate that is a raw (non-LFS) binary by one
// of two rules, closing two different gaps left by extension-based LFS
// routing:
//
//   - "raw_binary": the candidate is over maxBytes AND binary by content (a
//     NUL byte or invalid UTF-8 in its leading bytes).
//   - "raw_binary_executable": the candidate is checked out at an executable
//     mode (git's tracked 100755) AND binary by content, at any size - a
//     committed, ready-to-exec build output is a guardrail violation
//     regardless of how small it is.
//
// Both rules share the same binary-content test and the same exemptions:
// candidates are paths relative to root - typically a caller's
// staged-changes or full-tracked-tree listing from git; a candidate that no
// longer exists on disk (e.g. a staged deletion) is skipped, not reported.
// skipRules is the injected fsx.ClassifyPath ruleset for directories a
// candidate list might still contain but should never be flagged from (VCS
// internals, build artifacts). isLFSRouted keeps a legitimately LFS-routed
// candidate exempt from both rules. A candidate that qualifies for both
// rules yields two findings, one per rule id.
func ScanRawBinary(root string, candidates []string, skipRules []fsx.Rule, maxBytes int64, isLFSRouted LFSRouteChecker) ([]Finding, error) {
	var findings []Finding
	for _, rel := range candidates {
		relSlash := filepath.ToSlash(rel)
		if fsx.ClassifyPath(relSlash, skipRules).Class == SkipClass {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(relSlash))
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue // deleted / not on disk — nothing to inspect
		}
		oversize := info.Size() > maxBytes
		executable := info.Mode().Perm()&executableMode != 0
		if !oversize && !executable {
			continue
		}
		binary, err := fileHasBinaryPrefix(abs)
		if err != nil {
			return nil, fmt.Errorf("githooks: scan raw binary %s: %w", relSlash, err)
		}
		if !binary {
			continue
		}
		if isLFSRouted != nil {
			routed, err := isLFSRouted(relSlash)
			if err != nil {
				return nil, fmt.Errorf("githooks: LFS route check %s: %w", relSlash, err)
			}
			if routed {
				continue
			}
		}
		if oversize {
			findings = append(findings, Finding{
				Path:   relSlash,
				Rule:   "raw_binary",
				Detail: fmt.Sprintf("raw binary (%d bytes, over %d-byte threshold, not LFS-routed)", info.Size(), maxBytes),
			})
		}
		if executable {
			findings = append(findings, Finding{
				Path:   relSlash,
				Rule:   "raw_binary_executable",
				Detail: fmt.Sprintf("committed executable (mode %s) is binary in its first %d bytes, not LFS-routed", info.Mode().Perm(), sniffBytes),
			})
		}
	}
	return findings, nil
}

func fileHasBinaryPrefix(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, sniffBytes)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return isBinaryPrefix(buf[:n]), nil
}
