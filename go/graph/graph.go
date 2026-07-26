package graph

import (
	"errors"
	"fmt"
	"slices"
)

// ErrUnknownNode reports an operation naming a node the graph does not hold.
var ErrUnknownNode = errors.New("graph: unknown node")

// ErrDuplicateNode reports an AddNode call for an id the graph already holds.
var ErrDuplicateNode = errors.New("graph: duplicate node")

// IDScheme is the caller's description of how a node id is written. It is the
// only place this package knows anything about id text — the graph itself keys
// on ID's own comparability — so no id grammar is built in and any comparable
// type serves as an id.
//
// String renders the canonical form used in messages and as the primary key
// ResolveRefs indexes. Short renders the abbreviated form a reference may use
// in place of the canonical one, returning "" for an id that has none; a
// filesystem-shaped scheme would return the basename here, a hierarchical one
// its last component. ResolveRefs honours a short form only when exactly one
// node answers to it.
//
// Both fields are optional: String falls back to fmt.Sprint and a nil Short
// means no id has a short form, so the zero IDScheme is usable and carries no
// grammar at all.
type IDScheme[ID comparable] struct {
	String func(id ID) string
	Short  func(id ID) string
}

// StringIDs is the identity scheme for graphs keyed by plain strings: the id
// is its own canonical text and has no short form.
func StringIDs() IDScheme[string] {
	return IDScheme[string]{String: func(id string) string { return id }}
}

// text renders id per the scheme, falling back to fmt.Sprint.
func (s IDScheme[ID]) text(id ID) string {
	if s.String != nil {
		return s.String(id)
	}
	return fmt.Sprint(id)
}

// short renders id's abbreviated form, or "" when the scheme defines none.
func (s IDScheme[ID]) short(id ID) string {
	if s.Short == nil {
		return ""
	}
	return s.Short(id)
}

// Graph is a directed graph of nodes carrying payloads of type N, with edges
// meaning "depends on". A tree is just a DAG in which every node has at most
// one dependency, so tree-shaped and DAG-shaped data share this one type; see
// IsTree.
//
// Acyclicity is a checked property, not an enforced invariant: AddDep accepts
// any edge between existing nodes, including a self-edge, because the common
// case is loading a graph someone else authored and reporting where its cycle
// is. Referential integrity is the opposite — AddDep refuses an edge to a node
// that does not exist and RemoveNode drops every edge touching the node it
// removes, so a dangling edge cannot be constructed. Dangling *text*
// references, which come from outside the graph, are ResolveRefs's business.
//
// The zero Graph is not usable; construct one with New.
type Graph[ID comparable, N any] struct {
	scheme IDScheme[ID]
	order  []ID // insertion order: the tie-break behind every ordered result
	nodes  map[ID]N
	deps   map[ID][]ID
	rdeps  map[ID][]ID
}

// New returns an empty graph whose ids are rendered by scheme. Pass the zero
// IDScheme to accept the fmt.Sprint default and no short forms.
func New[ID comparable, N any](scheme IDScheme[ID]) *Graph[ID, N] {
	return &Graph[ID, N]{
		scheme: scheme,
		nodes:  map[ID]N{},
		deps:   map[ID][]ID{},
		rdeps:  map[ID][]ID{},
	}
}

// Scheme returns the id scheme the graph was built with.
func (g *Graph[ID, N]) Scheme() IDScheme[ID] { return g.scheme }

// Len returns the number of nodes.
func (g *Graph[ID, N]) Len() int { return len(g.order) }

// Has reports whether the graph holds id.
func (g *Graph[ID, N]) Has(id ID) bool {
	_, ok := g.nodes[id]
	return ok
}

// Node returns id's payload, and false when the graph does not hold id.
func (g *Graph[ID, N]) Node(id ID) (N, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// IDs returns every node id in insertion order. The result is a copy.
func (g *Graph[ID, N]) IDs() []ID { return slices.Clone(g.order) }

// Deps returns what id depends on, in the order the edges were added. The
// result is a copy; an unknown id yields nil.
func (g *Graph[ID, N]) Deps(id ID) []ID { return slices.Clone(g.deps[id]) }

// Dependents returns what depends on id, in the order the edges were added.
// The result is a copy; an unknown id yields nil.
func (g *Graph[ID, N]) Dependents(id ID) []ID { return slices.Clone(g.rdeps[id]) }

// AddNode inserts id with the given payload. Insertion order is the tie-break
// for every ordered result, so it is worth making deliberate. Re-adding an
// existing id is an error wrapping ErrDuplicateNode; use SetPayload to replace
// a payload.
func (g *Graph[ID, N]) AddNode(id ID, payload N) error {
	if g.Has(id) {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, g.scheme.text(id))
	}
	g.nodes[id] = payload
	g.order = append(g.order, id)
	return nil
}

// SetPayload replaces id's payload, leaving its edges and insertion position
// untouched. An unknown id is an error wrapping ErrUnknownNode.
func (g *Graph[ID, N]) SetPayload(id ID, payload N) error {
	if !g.Has(id) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(id))
	}
	g.nodes[id] = payload
	return nil
}

// RemoveNode deletes id along with every edge touching it, so no edge is ever
// left dangling. An unknown id is an error wrapping ErrUnknownNode.
func (g *Graph[ID, N]) RemoveNode(id ID) error {
	if !g.Has(id) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(id))
	}
	for _, d := range g.deps[id] {
		g.rdeps[d] = removeID(g.rdeps[d], id)
	}
	for _, d := range g.rdeps[id] {
		g.deps[d] = removeID(g.deps[d], id)
	}
	delete(g.deps, id)
	delete(g.rdeps, id)
	delete(g.nodes, id)
	g.order = removeID(g.order, id)
	return nil
}

// AddDep records that dependent depends on dependency. Both nodes must exist —
// an unknown endpoint is an error wrapping ErrUnknownNode — and repeating an
// edge is a no-op, so an edge is never counted twice. A self-edge is accepted
// and is a one-node cycle, which DetectCycles reports.
func (g *Graph[ID, N]) AddDep(dependent, dependency ID) error {
	if !g.Has(dependent) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(dependent))
	}
	if !g.Has(dependency) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(dependency))
	}
	if slices.Contains(g.deps[dependent], dependency) {
		return nil
	}
	g.deps[dependent] = append(g.deps[dependent], dependency)
	g.rdeps[dependency] = append(g.rdeps[dependency], dependent)
	return nil
}

// RemoveDep deletes the edge recording that dependent depends on dependency.
// Removing an edge that was never there is a no-op; an unknown endpoint is an
// error wrapping ErrUnknownNode.
func (g *Graph[ID, N]) RemoveDep(dependent, dependency ID) error {
	if !g.Has(dependent) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(dependent))
	}
	if !g.Has(dependency) {
		return fmt.Errorf("%w: %s", ErrUnknownNode, g.scheme.text(dependency))
	}
	g.deps[dependent] = removeID(g.deps[dependent], dependency)
	g.rdeps[dependency] = removeID(g.rdeps[dependency], dependent)
	return nil
}

// Roots returns the nodes that depend on nothing, in insertion order — where a
// topological order starts.
func (g *Graph[ID, N]) Roots() []ID {
	var out []ID
	for _, id := range g.order {
		if len(g.deps[id]) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// Leaves returns the nodes nothing depends on, in insertion order — where a
// topological order ends.
func (g *Graph[ID, N]) Leaves() []ID {
	var out []ID
	for _, id := range g.order {
		if len(g.rdeps[id]) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// IsTree reports whether the graph is a single rooted tree: acyclic, every
// node depending on at most one other, and exactly one root. An empty graph is
// not a tree, and a forest of several trees is not one either — use Roots to
// tell those two apart.
func (g *Graph[ID, N]) IsTree() bool {
	if len(g.order) == 0 {
		return false
	}
	roots := 0
	for _, id := range g.order {
		switch len(g.deps[id]) {
		case 0:
			roots++
		case 1:
		default:
			return false
		}
	}
	return roots == 1 && g.DetectCycles() == nil
}

// removeID returns s without the first occurrence of id, preserving order.
func removeID[ID comparable](s []ID, id ID) []ID {
	if i := slices.Index(s, id); i >= 0 {
		return slices.Delete(s, i, i+1)
	}
	return s
}
