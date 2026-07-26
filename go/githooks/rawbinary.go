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

// ScanRawBinary reports every candidate that is a raw (non-LFS) binary over
// maxBytes: the gap left by extension-based LFS routing, closed by content
// rather than by name. candidates are paths relative to root - typically a
// caller's staged-changes or full-tracked-tree listing from git; a candidate
// that no longer exists on disk (e.g. a staged deletion) is skipped, not
// reported. skipRules is the injected fsx.ClassifyPath ruleset for
// directories a candidate list might still contain but should never be
// flagged from (VCS internals, build artifacts). A candidate FAILS only when
// it is over maxBytes, binary by content (a NUL byte or invalid UTF-8 in its
// leading bytes), and isLFSRouted reports it is not LFS-routed.
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
		if info.Size() <= maxBytes {
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
		findings = append(findings, Finding{
			Path:   relSlash,
			Rule:   "raw_binary",
			Detail: fmt.Sprintf("raw binary (%d bytes, over %d-byte threshold, not LFS-routed)", info.Size(), maxBytes),
		})
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
