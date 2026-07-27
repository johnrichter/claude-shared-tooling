package graph

import (
	"fmt"
	"slices"
	"strings"
)

// Ambiguity is one reference that several nodes answer to, listed with the
// candidates in insertion order. It is the guard against a short form quietly
// resolving to whichever node happened to be indexed first.
type Ambiguity[ID comparable] struct {
	Ref        string
	Candidates []ID
}

// RefResolution is the outcome of resolving textual references against a
// graph's id index: what resolved, what named nothing, and what named too
// much. Unknown and Ambiguous are in the order the references were given, so
// a report built from them is stable.
type RefResolution[ID comparable] struct {
	Resolved  map[string]ID
	Unknown   []string
	Ambiguous []Ambiguity[ID]
}

// OK reports whether every reference resolved to exactly one node.
func (r RefResolution[ID]) OK() bool { return len(r.Unknown) == 0 && len(r.Ambiguous) == 0 }

// Err returns nil when every reference resolved, and otherwise an error naming
// what did not — dangling references first, then ambiguous ones with their
// candidates.
func (r RefResolution[ID]) Err(scheme IDScheme[ID]) error {
	if r.OK() {
		return nil
	}
	var parts []string
	if len(r.Unknown) > 0 {
		parts = append(parts, "unresolved "+strings.Join(quoteAll(r.Unknown), ", "))
	}
	for _, a := range r.Ambiguous {
		names := make([]string, len(a.Candidates))
		for i, id := range a.Candidates {
			names[i] = scheme.text(id)
		}
		parts = append(parts, fmt.Sprintf("%q matches %s", a.Ref, strings.Join(names, ", ")))
	}
	return fmt.Errorf("graph: %s", strings.Join(parts, "; "))
}

// ResolveRefs resolves textual references — links between documents, a plan's
// dependency lists, anything written by hand — against the graph's nodes.
//
// A reference is matched first against canonical ids, and only then against
// the short forms the id scheme defines, so writing an id out in full always
// means that node even where some other node's short form collides with it. A
// short form shared by several nodes resolves to none of them and is reported
// as an Ambiguity: guessing between them is exactly the silent
// misattribution the short form is worth having only if it cannot cause. Two
// nodes rendering to the same canonical text are ambiguous for the same
// reason, which is how a broken id scheme surfaces here rather than as a lost
// node.
//
// The index is built fresh on every call, so a result can never be stale with
// respect to a mutation.
func (g *Graph[ID, N]) ResolveRefs(refs ...string) RefResolution[ID] {
	canonical := map[string][]ID{}
	short := map[string][]ID{}
	for _, id := range g.order {
		canonical[g.scheme.text(id)] = append(canonical[g.scheme.text(id)], id)
		if s := g.scheme.short(id); s != "" {
			short[s] = append(short[s], id)
		}
	}

	out := RefResolution[ID]{Resolved: map[string]ID{}}
	for _, ref := range refs {
		if _, done := out.Resolved[ref]; done {
			continue
		}
		candidates, hit := canonical[ref]
		if !hit {
			candidates, hit = short[ref]
		}
		switch {
		case !hit:
			if !slices.Contains(out.Unknown, ref) {
				out.Unknown = append(out.Unknown, ref)
			}
		case len(candidates) == 1:
			out.Resolved[ref] = candidates[0]
		default:
			if !slices.ContainsFunc(out.Ambiguous, func(a Ambiguity[ID]) bool { return a.Ref == ref }) {
				out.Ambiguous = append(out.Ambiguous, Ambiguity[ID]{Ref: ref, Candidates: slices.Clone(candidates)})
			}
		}
	}
	return out
}

// quoteAll renders each string quoted, for readable error text.
func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
