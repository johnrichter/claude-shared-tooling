package plugin_behavioral

import (
	"sort"
	"strings"
)

// leakContextRadius is how many characters of surrounding text a LeakFinding keeps on each side
// of a match, so a reviewer can judge a finding without re-opening the prompt it came from.
const leakContextRadius = 20

// LeakFinding is one place a forbidden term -- a tool, skill, subagent-role or binary name the
// case under test names -- was found inside a probe prompt. A prompt that names what it tests
// biases the very trial it is meant to observe: Claude reaching for a tool because the prompt
// said to use it proves nothing about whether the tool's own trigger would have fired unprompted.
type LeakFinding struct {
	Term    string
	Offset  int
	Context string
}

// isIdentifierByte reports whether b belongs to this lint's identifier-boundary class: letters,
// digits, underscore and hyphen all glue together as one token. This is deliberately wider than a
// plain word-boundary rule, which treats "-" as a break -- a hyphenated name like a skill or a
// subagent role must match as one token, never fragment into pieces that could each
// coincidentally collide with an unrelated word.
func isIdentifierByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_', b == '-':
		return true
	default:
		return false
	}
}

// LintPrompt scans prompt for every case-insensitive, identifier-bounded occurrence of each term
// in forbiddenTerms. A match is bounded on both sides by a non-identifier character (or the
// string's start/end): narrower than a raw substring match (so "git" does not flag "digit"),
// wider than a \b-style word boundary (so a hyphenated term matches as one token). This lint is
// deliberately biased toward over-catching -- a leak this lint misses silently invalidates a live
// trial, discovered only by a human reviewer noticing later, if at all; a false positive costs one
// glance at a finding and a prompt rewrite. Findings are sorted by offset, then term, for a
// deterministic order.
func LintPrompt(prompt string, forbiddenTerms []string) []LeakFinding {
	var findings []LeakFinding
	lowerPrompt := strings.ToLower(prompt)
	for _, term := range forbiddenTerms {
		if term == "" {
			continue
		}
		lowerTerm := strings.ToLower(term)
		for start := 0; start <= len(lowerPrompt)-len(lowerTerm); start++ {
			if lowerPrompt[start:start+len(lowerTerm)] != lowerTerm {
				continue
			}
			end := start + len(lowerTerm)
			boundedBefore := start == 0 || !isIdentifierByte(prompt[start-1])
			boundedAfter := end == len(prompt) || !isIdentifierByte(prompt[end])
			if !boundedBefore || !boundedAfter {
				continue
			}
			lo, hi := start-leakContextRadius, end+leakContextRadius
			if lo < 0 {
				lo = 0
			}
			if hi > len(prompt) {
				hi = len(prompt)
			}
			findings = append(findings, LeakFinding{Term: term, Offset: start, Context: prompt[lo:hi]})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Offset != findings[j].Offset {
			return findings[i].Offset < findings[j].Offset
		}
		return findings[i].Term < findings[j].Term
	})
	return findings
}
