package fsx

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// Rule maps a doublestar glob pattern (matched against a slash-separated,
// relative path) to a caller-defined class label, e.g. "protected", "generated",
// "cruft". The label's meaning is entirely up to the caller — fsx has no
// built-in notion of what "protected" means.
type Rule struct {
	Pattern string
	Class   string
}

// ClassifyResult is what ClassifyPath decided, plus enough of its reasoning for
// the caller to audit it.
type ClassifyResult struct {
	// Class is the resolved class, or "" if no rule matched.
	Class string
	// MatchedPattern is the pattern that produced Class, or "" if none matched.
	MatchedPattern string
	// Malformed lists rule patterns that failed to compile as globs. These rules
	// are never silently dropped: each one is treated as a conservative match
	// (see ClassifyPath doc) and reported here so a caller can fix or audit the
	// ruleset instead of unknowingly running with a broken rule.
	Malformed []string
}

// ClassifyPath decides path's class from an injected, ordered ruleset — fsx
// never hardcodes classification rules; every caller supplies its own.
//
// Rules are evaluated in order and the last match wins (like a .gitignore or CSS
// cascade), so a later rule can deliberately override an earlier, broader one.
// An empty ruleset always yields Class == "". Overlapping patterns are resolved
// entirely by list order — there is no implicit specificity ranking.
//
// A rule whose Pattern fails to compile as a doublestar glob is never dropped
// from consideration: since fsx cannot verify what it does or doesn't match, it
// is demoted to a conservative fallback that always matches (fail closed) rather
// than being treated as a non-match (fail open, which could silently miss a
// path a caller intended to protect). It is also recorded in Malformed so the
// caller can see the ruleset needs fixing.
func ClassifyPath(path string, rules []Rule) ClassifyResult {
	slashPath := filepath.ToSlash(path)
	result := ClassifyResult{}
	for _, rule := range rules {
		matched, err := doublestar.Match(rule.Pattern, slashPath)
		if err != nil {
			result.Malformed = append(result.Malformed, rule.Pattern)
			matched = true // demote: fail closed, not dropped
		}
		if matched {
			result.Class = rule.Class
			result.MatchedPattern = rule.Pattern
		}
	}
	return result
}
