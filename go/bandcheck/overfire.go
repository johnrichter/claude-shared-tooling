package bandcheck

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
)

// OverfireReport is CheckOverfire's declared-vs-actual precision verdict for one rule: which
// real files its `paths:` glob(s) actually match, and — when the rule has a **Scope.**
// paragraph — which of those matches its own declared target never named.
//
// ScopeFound is the field that keeps this check honest: when it is false, ScopeAbsentWarning is
// always non-empty and Excess/Precision are the zero value, never a fabricated "fully precise"
// verdict. A caller MUST check ScopeFound before trusting Precision — treating the zero value as
// 1.0-worth-of-confidence is exactly the false clean bill this package exists to prevent.
type OverfireReport struct {
	RulePath           string
	ActualMatches      []string
	ScopeFound         bool
	ScopeAbsentWarning string
	Excess             []string
	Precision          float64
}

// Clean reports whether the check found nothing to flag: it requires ScopeFound, so a rule with
// no Scope paragraph is never reported clean by this method — call sites must check ScopeFound
// directly rather than relying on Clean to fold that case in.
func (r OverfireReport) Clean() bool {
	return r.ScopeFound && len(r.Excess) == 0
}

// expandGlobs walks root and returns every regular file matching any of globs, as sorted
// root-relative slash paths. A pattern ending in a bare "**" is widened to "**/*" first: several
// doublestar/filepath.Glob implementations, this repo's own dir-glob conventions included,
// return directories on a trailing "**" — a rule governs files, not the directories holding
// them, so a directory match is never itself a reportable actual match.
func expandGlobs(root string, globs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		pattern := g
		if pattern == "**" || filepath.Base(pattern) == "**" {
			pattern = filepath.ToSlash(filepath.Join(pattern, "*"))
		}
		err := doublestar.GlobWalk(os.DirFS(root), pattern, func(path string, d os.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// CheckOverfire checks one workspace rule's declared `paths:` trigger for glob-overfire: does it
// match, in a real tree rooted at root, only what the rule's own **Scope.** paragraph names as
// its intended target — or does it also sweep in files the rule's author never meant to govern?
//
// A rule with no PathGlobs (unconditional) or no ScopeFound is reported, never silently treated
// as clean: ScopeFound is false (with ScopeAbsentWarning set) whenever rule.ScopeFound is false,
// regardless of how the actual matches turn out, so a caller can never mistake "nothing to
// compare against" for "compared and found precise" — the defect this check exists to close
// relative to a Scope-optional prior implementation that defaulted an absent Scope paragraph to
// the glob's own matches and reported a trivial, meaningless 1.0.
func CheckOverfire(rule Rule, root string) (OverfireReport, error) {
	report := OverfireReport{RulePath: rule.Path}

	matches, err := expandGlobs(root, rule.PathGlobs)
	if err != nil {
		return OverfireReport{}, err
	}
	report.ActualMatches = matches

	if !rule.ScopeFound {
		report.ScopeAbsentWarning = "rule " + rule.Path + " has no **Scope.** paragraph -- overfire precision cannot be checked against a declared target and is reported undetected, never as a clean bill"
		return report, nil
	}
	report.ScopeFound = true

	// A found Scope paragraph that names no usable glob/filename token (as opposed to no
	// paragraph at all) has nothing independent to check the glob against -- the glob's own
	// matches stand as their own declared target, and precision is trivially 1.0. This is NOT
	// the false-clean-bill case this check exists to prevent: that case is ScopeFound == false,
	// handled above, and it is never routed through this fallback.
	scopeTargets := rule.ScopeTokens
	if len(scopeTargets) == 0 {
		scopeTargets = rule.PathGlobs
	}
	declaredMatches, err := expandGlobs(root, scopeTargets)
	if err != nil {
		return OverfireReport{}, err
	}
	declared := map[string]bool{}
	for _, m := range declaredMatches {
		declared[m] = true
	}

	var excess []string
	for _, m := range matches {
		if !declared[m] {
			excess = append(excess, m)
		}
	}
	report.Excess = excess

	switch {
	case len(matches) == 0:
		report.Precision = 1.0
	default:
		report.Precision = float64(len(matches)-len(excess)) / float64(len(matches))
	}
	return report, nil
}
