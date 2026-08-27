package githooks

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// SkipClass is the fsx.Rule class a caller uses to mark a path skipped by
// every content scan in this package - VCS internals, build/venv artifacts,
// vendored trees. Any other class (or no match) means "scan it".
const SkipClass = "skip"

// exemptRuleset names which of ScanPrivacy/ScanSecrets' exempt-style
// rulesets a malformed pattern was found in, so validateExemptRules' error
// can point a caller straight at the right config field.
type exemptRuleset string

const (
	markerExemptRuleset exemptRuleset = "MarkerExemptRules"
	secretExemptRuleset exemptRuleset = "SecretExemptRules"
)

// validateExemptRules reports an error naming ruleset and its first pattern
// that is not a valid glob, or nil if every pattern is valid.
//
// SkipRules and the exempt-style rulesets (MarkerExemptRules,
// SecretExemptRules) both go through fsx.ClassifyPath, which treats an
// unparseable pattern as an always-match - a deliberately cautious default
// for SkipRules, where "matched" means "be careful, don't scan this path".
// For an exempt-style ruleset, though, "matched" means "exempt this path
// from a security check", so that same default is fail-open: a malformed
// entry would silently exempt every path in the tree instead of none. This
// function exists to turn that into a loud, immediate error instead, before
// the tree walk starts.
//
// Validity is decided by doublestar's own whole-pattern validator, not by
// fsx.ClassifyResult.Malformed: that field reports only what the matcher
// happened to parse while deciding one specific path, so it misses a broken
// alternative the match never had to reach - "{**,[bad}" is reported
// malformed for no path at all, because "**" always matches first, yet it
// exempts every path it is applied to. ValidatePattern takes no path, so it
// has no such blind spot.
func validateExemptRules(ruleset exemptRuleset, rules []fsx.Rule) error {
	for _, rule := range rules {
		if !doublestar.ValidatePattern(rule.Pattern) {
			return fmt.Errorf("githooks: %s pattern %q is not a valid glob pattern", ruleset, rule.Pattern)
		}
	}
	return nil
}

// walkScannable walks root and calls visit(relPath, absPath) for every
// regular file fsx.ClassifyPath does not resolve to skipRules' SkipClass.
// Matching runs against each file's slash-separated path relative to root, so
// a rule like "**/.git/**" skips the directory wherever it sits under root.
func walkScannable(root string, skipRules []fsx.Rule, visit func(rel, abs string) error) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if fsx.ClassifyPath(rel, skipRules).Class == SkipClass {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		return visit(filepath.ToSlash(rel), path)
	})
	if err != nil {
		return fmt.Errorf("githooks: walk %s: %w", root, err)
	}
	return nil
}
