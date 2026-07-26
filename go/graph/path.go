package graph

import (
	"path"
	"strings"
)

// Claim kinds understood by PathDomain.
const (
	// PathFile is one exact path.
	PathFile = "file"
	// PathDir is a directory: itself and everything beneath it.
	PathDir = "dir"
	// PathGlob is a pattern; see PathDomain for the dialect it is read in.
	PathGlob = "glob"
)

// globMeta are the characters that stop a path segment from being a literal.
// The set is deliberately wide: treating a character as meta can only make the
// domain less willing to prove disjointness.
const globMeta = `*?[]{}\`

// PathOption configures a PathDomain.
type PathOption func(*pathConfig)

type pathConfig struct {
	fold    func(string) string                      // nil means compare paths exactly as written
	custom  bool                                     // the fold came from a caller, so its behaviour is unknown here
	matcher func(pattern, name string) (bool, error) // nil means use the built-in segment-wise dialect
}

// WithPathFold replaces the default case folding with fold, which is applied
// to both sides of every comparison before they are matched. Pass nil for
// exact, byte-for-byte comparison on a filesystem known to be case-sensitive.
//
// A fold must only ever merge paths that could denote the same file, never
// separate paths that could: merging costs precision, separating costs
// correctness. Nothing here can check that a caller's fold behaves, or that it
// leaves a pattern's metacharacters and length alone, so under a
// caller-supplied fold every glob claim falls back to the literal-prefix rule.
func WithPathFold(fold func(string) string) PathOption {
	return func(c *pathConfig) {
		c.fold = fold
		c.custom = fold != nil
	}
}

// WithPathMatcher supplies the glob implementation a pattern is read with when
// it is compared against a single path, in place of the built-in segment-wise
// dialect. Pass a globstar-capable matcher — one that understands "**" and
// whatever else the surfaces in play are written in — and a pattern such as
// "svc/**/*.go" is decided exactly against a file rather than by its literal
// prefix, which is the difference between one batch and several.
//
// The matcher answers only "does this pattern match this one path". Nothing
// answers "do these two patterns share a match", so pattern against pattern,
// and pattern against directory, keep their own rules either way. A matcher
// that reports fewer matches than the runtime glob it stands in for would make
// the domain unsound, so it must be the same implementation the work itself
// resolves surfaces with.
func WithPathMatcher(match func(pattern, name string) (bool, error)) PathOption {
	return func(c *pathConfig) { c.matcher = match }
}

// PathDomain returns a resource domain over slash-separated paths, registered
// under name. It is the file-shaped instance of the resource-surface
// abstraction, not a privileged one: it decides claims of kind PathFile,
// PathDir and PathGlob, and treats any other kind as undecidable.
//
// # Comparison rules
//
// Values are trimmed and cleaned before comparison. A value containing a ".."
// segment is undecidable, because a claim that walks out of its own subtree
// cannot be placed; so is a pair mixing an absolute value with a relative one,
// since nothing here knows the root the relative one hangs off. A PathDir
// value of "." or "/" is the whole tree and overlaps every path beside it.
//
// Literal claims compare directly: two files by equality, a file against a
// directory and two directories by containment. Glob claims are proven
// disjoint three ways, in order of precision:
//
//   - Against a single file, a matcher supplied by WithPathMatcher settles it
//     outright, in whatever glob dialect that matcher implements.
//   - A simple pattern — no "**", no brace alternation, no backslash escape —
//     matches a fixed number of segments, each independently, so two claims
//     whose segment counts differ, or that disagree on any one segment, cannot
//     describe a common path. Segments are matched with path.Match, which is
//     complete for a single segment because none of its wildcards crosses a
//     separator.
//   - Otherwise only the pattern's literal prefix is used: every path a
//     pattern matches begins with the leading segments that contain no
//     metacharacter, so two claims whose literal prefixes diverge cannot meet.
//     This is what reads an unsupplied "**" or brace pattern conservatively
//     instead of guessing at its extent.
//
// Anything none of the three settles is an overlap. No library is consulted
// for the harder half of the job — proving that two *patterns* share no match
// is not a question a matcher can answer — so that half is a sufficient
// condition rather than a decision procedure, and errs toward overlap.
//
// # Case folding
//
// By default both sides are lowercased, because a case-insensitive filesystem
// makes "README.md" and "readme.md" one file and a case-sensitive proof would
// call them two. Folding only ever merges paths, so it is the safe default on
// a filesystem whose behaviour is unknown; WithPathFold turns it off where the
// filesystem is known to be case-sensitive and the precision is wanted. While
// a fold is in effect, a glob is compared by its literal prefix alone unless
// both values are ASCII, since a fold can otherwise change a pattern's meaning
// or its length.
//
// # What it cannot see
//
// Two claims that name the same file through a symlink, a hard link, a bind
// mount, or a filesystem's own Unicode normalisation are different text and
// will be proven disjoint. No comparison of declared paths can see any of
// those; catching them belongs to a check that runs against the real tree
// after the work has merged.
func PathDomain(name string, opts ...PathOption) Domain {
	cfg := pathConfig{fold: strings.ToLower}
	for _, opt := range opts {
		opt(&cfg)
	}
	return Domain{
		Name:   name,
		Relate: func(a, b Claim) Relation { return cfg.relate(a, b) },
	}
}

// pathClaim is a claim resolved into the form the comparison rules work on.
type pathClaim struct {
	kind  string
	value string
	segs  []string
	abs   bool
}

// relate decides how two path claims stand to one another.
func (c pathConfig) relate(a, b Claim) Relation {
	pa, okA := c.resolve(a)
	pb, okB := c.resolve(b)
	if !okA || !okB || pa.abs != pb.abs {
		return RelationUnknown
	}
	switch {
	case pa.kind == PathGlob && pb.kind == PathGlob:
		return c.globVsGlob(pa, pb)
	case pa.kind == PathGlob:
		return c.globVsLiteral(pa, pb)
	case pb.kind == PathGlob:
		return c.globVsLiteral(pb, pa)
	case pa.kind == PathDir && pb.kind == PathDir:
		return overlapIf(covers(pa, pb) || covers(pb, pa))
	case pa.kind == PathDir:
		return overlapIf(covers(pa, pb))
	case pb.kind == PathDir:
		return overlapIf(covers(pb, pa))
	default:
		return overlapIf(pa.value == pb.value)
	}
}

// resolve normalises, folds and splits a claim, reporting false for anything
// the comparison rules cannot place: an unknown kind, an empty value, or a
// value that walks out of its own subtree.
func (c pathConfig) resolve(cl Claim) (pathClaim, bool) {
	switch cl.Kind {
	case PathFile, PathDir, PathGlob:
	default:
		return pathClaim{}, false
	}
	v := strings.TrimSpace(cl.Value)
	if v == "" {
		return pathClaim{}, false
	}
	abs := strings.HasPrefix(v, "/")
	v = path.Clean(v)
	if v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") {
		return pathClaim{}, false
	}
	if c.fold != nil {
		v = c.fold(v)
	}
	kind := cl.Kind
	// A pattern with nothing to match is exactly one path, so read it as one.
	if kind == PathGlob && !strings.ContainsAny(v, globMeta) {
		kind = PathFile
	}
	return pathClaim{kind: kind, value: v, segs: strings.Split(strings.TrimPrefix(v, "/"), "/"), abs: abs}, true
}

// isRoot reports whether a claim names the top of the tree, which as a
// directory contains every other path.
func isRoot(p pathClaim) bool { return p.value == "." || p.value == "/" }

// covers reports whether directory d contains p — p being the directory
// itself, or anything beneath it. The tree root contains everything.
func covers(d, p pathClaim) bool {
	return isRoot(d) || p.value == d.value || strings.HasPrefix(p.value, d.value+"/")
}

// globVsLiteral relates a glob claim to a file or directory claim.
func (c pathConfig) globVsLiteral(g, lit pathClaim) Relation {
	if lit.kind == PathDir && isRoot(lit) {
		return RelationOverlap
	}
	// A supplied matcher settles a pattern against a single path outright,
	// whatever the pattern's shape; a directory is a set of paths, which is
	// not a question a matcher can be asked.
	if lit.kind == PathFile && c.matcher != nil && c.foldSafe(g) {
		switch ok, err := c.matcher(g.value, lit.value); {
		case err != nil:
			return RelationUnknown
		case ok:
			return RelationOverlap
		default:
			return RelationDisjoint
		}
	}
	if !c.precise(g) {
		return prefixRelation(g, lit)
	}
	// Every path a simple pattern matches has exactly as many segments as the
	// pattern does, so a directory the pattern cannot reach down to is out of
	// reach entirely, and a file of a different depth cannot be matched.
	switch {
	case lit.kind == PathDir && len(g.segs) < len(lit.segs):
		return RelationDisjoint
	case lit.kind != PathDir && len(g.segs) != len(lit.segs):
		return RelationDisjoint
	}
	for i := range min(len(g.segs), len(lit.segs)) {
		switch ok, err := path.Match(g.segs[i], lit.segs[i]); {
		case err != nil:
			return RelationUnknown
		case !ok:
			return RelationDisjoint
		}
	}
	return RelationOverlap
}

// globVsGlob relates two glob claims.
func (c pathConfig) globVsGlob(a, b pathClaim) Relation {
	if !c.precise(a) || !c.precise(b) {
		return prefixRelation(a, b)
	}
	if len(a.segs) != len(b.segs) {
		return RelationDisjoint
	}
	for i := range a.segs {
		if segmentsDisjoint(a.segs[i], b.segs[i]) {
			return RelationDisjoint
		}
	}
	return RelationOverlap
}

// segmentsDisjoint reports whether two single-segment patterns provably match
// no common text. Two literals settle it by inequality and a literal against a
// pattern by matching; two patterns are left unsettled, since proving two
// patterns share no match is not something matching can answer.
func segmentsDisjoint(a, b string) bool {
	metaA := strings.ContainsAny(a, globMeta)
	metaB := strings.ContainsAny(b, globMeta)
	switch {
	case !metaA && !metaB:
		return a != b
	case !metaA:
		ok, err := path.Match(b, a)
		return err == nil && !ok
	case !metaB:
		ok, err := path.Match(a, b)
		return err == nil && !ok
	default:
		return false
	}
}

// prefixRelation is the rule that holds for any pattern, however exotic: every
// path a claim describes begins with its leading metacharacter-free segments,
// so two claims whose literal prefixes disagree describe no common path.
func prefixRelation(a, b pathClaim) Relation {
	pa, pb := literalPrefix(a.segs), literalPrefix(b.segs)
	for i := range min(len(pa), len(pb)) {
		if pa[i] != pb[i] {
			return RelationDisjoint
		}
	}
	return RelationOverlap
}

// literalPrefix returns the leading segments that contain no metacharacter —
// the part of a pattern every match it has must reproduce exactly.
func literalPrefix(segs []string) []string {
	for i, s := range segs {
		if strings.ContainsAny(s, globMeta) {
			return segs[:i]
		}
	}
	return segs
}

// foldSafe reports whether a glob claim can be matched at all after folding.
// Under the default fold a character class can invert its meaning and a
// non-ASCII value can change length, either of which would let a pattern match
// less after folding than before — the one direction that turns imprecision
// into unsoundness. A caller's fold is unknown, so nothing is assumed of it.
func (c pathConfig) foldSafe(g pathClaim) bool {
	switch {
	case c.custom:
		return false
	case c.fold != nil && (strings.ContainsAny(g.value, "[]") || !isASCII(g.value)):
		return false
	}
	return true
}

// precise reports whether a glob claim can be matched segment by segment. A
// "**" crosses segment boundaries and brace alternation can change how many
// segments a match has, so neither can be aligned against another claim's
// segments; a backslash escape hides a literal behind a metacharacter.
func (c pathConfig) precise(g pathClaim) bool {
	if strings.Contains(g.value, "**") || strings.ContainsAny(g.value, `{}\`) {
		return false
	}
	return c.foldSafe(g)
}

// isASCII reports whether s is entirely ASCII.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// overlapIf turns a "these touch" test into a relation.
func overlapIf(touching bool) Relation {
	if touching {
		return RelationOverlap
	}
	return RelationDisjoint
}
