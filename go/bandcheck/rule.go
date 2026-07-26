package bandcheck

import (
	"regexp"

	"gopkg.in/yaml.v3"
)

// Rule is one parsed `.claude/rules/*.md` workspace rule: its declared `paths:` trigger glob
// (the loader's only machine-read field) and its body's own declared target, per the
// rule-authoring convention's closing **Scope.** paragraph. ScopeFound distinguishes "the rule
// has no Scope paragraph at all" from "the rule has one but it names no usable glob/filename
// token" — CheckOverfire treats the two differently (see its doc comment).
type Rule struct {
	Path        string
	PathGlobs   []string
	ScopeFound  bool
	ScopeTokens []string
}

// frontmatterRE captures the leading `---`/`---` YAML block a workspace rule opens with.
var frontmatterRE = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n`)

// scopeParagraphRE captures the **Scope.** paragraph's own text, up to the next blank line or
// end of file — a later, separate paragraph (e.g. a dated scope-tightening note) is never folded
// into it, only the live Scope statement is.
var scopeParagraphRE = regexp.MustCompile(`(?s)\*\*Scope\.\*\*(.*?)(?:\r?\n\s*\r?\n|\z)`)

// backtickRE matches one backtick-quoted span in a Scope paragraph.
var backtickRE = regexp.MustCompile("`([^`\n]+)`")

// globLikeRE matches a backtick token that is already glob/path shaped (contains a path
// separator or a wildcard) — passed through to doublestar.Match verbatim.
var globLikeRE = regexp.MustCompile(`^[A-Za-z0-9_.\-*]*[/*][A-Za-z0-9_.\-*/]*$`)

// bareFilenameRE matches a bare filename token with no path separator — "SKILL.md",
// ".gitignore", "workflow.js" — the Scope prose's shorthand for "this file, wherever it lives",
// widened to "**/<name>". A leading dot is optional so a dotfile with no extension-bearing name
// (".gitignore") matches as readily as a named-and-extensioned file.
var bareFilenameRE = regexp.MustCompile(`^\.?[A-Za-z0-9_\-]+(\.[A-Za-z0-9_\-]+)*$`)

// ParseRule reads one `.claude/rules/*.md` file's `paths:` frontmatter and its **Scope.**
// paragraph. A rule with no `paths:` key (an unconditional rule) resolves an empty PathGlobs —
// distinct from a parse failure, since an unconditional rule has nothing for CheckOverfire to
// expand and is correctly reported as such by its caller, not by this function.
func ParseRule(path string, text []byte) (Rule, error) {
	r := Rule{Path: path}

	if m := frontmatterRE.FindSubmatch(text); m != nil {
		var fm struct {
			Paths []string `yaml:"paths"`
		}
		if err := yaml.Unmarshal(m[1], &fm); err != nil {
			return Rule{}, err
		}
		r.PathGlobs = fm.Paths
	}

	scope := scopeParagraphRE.FindSubmatch(text)
	if scope == nil {
		return r, nil
	}
	r.ScopeFound = true
	seen := map[string]bool{}
	for _, tok := range backtickRE.FindAllSubmatch(scope[1], -1) {
		token := string(tok[1])
		var candidate string
		switch {
		case globLikeRE.MatchString(token):
			candidate = token
		case bareFilenameRE.MatchString(token):
			candidate = "**/" + token
		default:
			continue
		}
		if !seen[candidate] {
			seen[candidate] = true
			r.ScopeTokens = append(r.ScopeTokens, candidate)
		}
	}
	return r, nil
}
