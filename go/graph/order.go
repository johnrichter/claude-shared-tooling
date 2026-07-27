package graph

import (
	"slices"
	"strings"
)

// CycleError reports that a graph cannot be ordered because it contains at
// least one dependency cycle, and names the offending path rather than just
// the nodes that failed to drain. Each entry of Cycles is a concrete walk
// along "depends on" edges — a -> b -> c means a depends on b depends on c —
// closing back onto its own first element, which Error repeats at the end so
// the text reads as a loop.
//
// Cycles holds every cycle the detector reached, one per back edge, each
// rotated to a canonical starting point so the same loop reached from two
// entry points reports once. Every entry is a real cycle; the set is not
// guaranteed to enumerate every elementary cycle in a densely tangled graph,
// because naming one true cycle per tangle is what a caller needs to act, and
// enumerating them all can be exponential.
type CycleError[ID comparable] struct {
	Cycles [][]ID

	render func(ID) string
}

// Error renders every reported cycle as a closed path, cycles separated by
// "; ".
func (e *CycleError[ID]) Error() string {
	var b strings.Builder
	b.WriteString("graph: dependency cycle: ")
	for i, cyc := range e.Cycles {
		if i > 0 {
			b.WriteString("; ")
		}
		for _, id := range cyc {
			b.WriteString(e.text(id))
			b.WriteString(" -> ")
		}
		if len(cyc) > 0 {
			b.WriteString(e.text(cyc[0]))
		}
	}
	return b.String()
}

// text renders one id, tolerating a CycleError built without a renderer.
func (e *CycleError[ID]) text(id ID) string {
	if e.render == nil {
		return IDScheme[ID]{}.text(id)
	}
	return e.render(id)
}

// DetectCycles reports every dependency cycle it reaches, returning nil when
// the graph is acyclic and a *CycleError naming the offending paths when it is
// not. Detection is a depth-first walk that visits dependencies in the order
// their edges were added, so the reported path is the same on every run.
func (g *Graph[ID, N]) DetectCycles() error {
	return g.cyclesAmong(nil)
}

// cyclesAmong walks the graph depth-first and reports the cycles it finds. A
// non-nil scope restricts both the walk and its entry points to the nodes it
// contains, which is how TopoSort turns "these nodes never drained" into the
// actual path that trapped them.
func (g *Graph[ID, N]) cyclesAmong(scope map[ID]bool) error {
	const (
		open = 1 // on the current path
		done = 2 // fully explored
	)
	state := make(map[ID]int, len(g.order))
	var path []ID
	var found [][]ID
	seen := map[string]bool{}

	inScope := func(id ID) bool { return scope == nil || scope[id] }

	var visit func(ID)
	visit = func(id ID) {
		state[id] = open
		path = append(path, id)
		for _, d := range g.deps[id] {
			if !inScope(d) {
				continue
			}
			switch state[d] {
			case open:
				cyc, key := canonicalCycle(g.scheme, path[slices.Index(path, d):])
				if !seen[key] {
					seen[key] = true
					found = append(found, cyc)
				}
			case done:
				// Already explored, and nothing on the current path leads
				// back through it, so there is no cycle to find here.
			default:
				visit(d)
			}
		}
		path = path[:len(path)-1]
		state[id] = done
	}

	for _, id := range g.order {
		if inScope(id) && state[id] == 0 {
			visit(id)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &CycleError[ID]{Cycles: found, render: g.scheme.text}
}

// canonicalCycle rotates a cycle to begin at its lexicographically smallest
// rendering and returns it alongside that rendering as a de-duplication key —
// two walks of the same loop differ only by where they start. The input is the
// live depth-first path, so it is copied before rotating.
func canonicalCycle[ID comparable](scheme IDScheme[ID], cyc []ID) ([]ID, string) {
	texts := make([]string, len(cyc))
	for i, id := range cyc {
		texts[i] = scheme.text(id)
	}
	pivot := 0
	for i, t := range texts {
		if t < texts[pivot] {
			pivot = i
		}
	}
	rotated := append(slices.Clone(cyc[pivot:]), cyc[:pivot]...)
	rotatedText := append(slices.Clone(texts[pivot:]), texts[:pivot]...)
	return rotated, strings.Join(rotatedText, "\x00")
}

// Layers returns the graph's topological frontiers: layer 0 is every node that
// depends on nothing, and each later layer is every node whose dependencies
// all sit in earlier layers. Nodes within a layer are in insertion order, and
// no two of them can be dependency-related, which is what makes a layer the
// natural unit of parallel work. A cyclic graph has no layering, so the error
// is a *CycleError naming the cycle.
func (g *Graph[ID, N]) Layers() ([][]ID, error) {
	layers, undrained := g.frontiers()
	if len(undrained) > 0 {
		return nil, g.cyclesAmong(undrained)
	}
	return layers, nil
}

// TopoSort returns a dependency-respecting order: every node appears after
// everything it depends on. It is Kahn's algorithm in frontier form —
// repeatedly take the nodes of in-degree zero, emit them, and remove their
// outgoing edges — with ties broken by insertion order, so the order is stable
// across runs. A cyclic graph has no topological order, so the error is a
// *CycleError naming the cycle rather than merely listing what failed to
// drain.
func (g *Graph[ID, N]) TopoSort() ([]ID, error) {
	layers, undrained := g.frontiers()
	if len(undrained) > 0 {
		return nil, g.cyclesAmong(undrained)
	}
	out := make([]ID, 0, len(g.order))
	for _, layer := range layers {
		out = append(out, layer...)
	}
	return out, nil
}

// frontiers runs Kahn's algorithm and returns the drained layers plus the set
// of nodes that never reached in-degree zero. A non-empty second result means
// the graph is cyclic; the trapped nodes are exactly the cycles and everything
// downstream of them.
func (g *Graph[ID, N]) frontiers() ([][]ID, map[ID]bool) {
	indeg := make(map[ID]int, len(g.order))
	for _, id := range g.order {
		indeg[id] = len(g.deps[id])
	}
	var layers [][]ID
	drained := make(map[ID]bool, len(g.order))
	for len(drained) < len(g.order) {
		var layer []ID
		for _, id := range g.order {
			if !drained[id] && indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			break
		}
		for _, id := range layer {
			drained[id] = true
			for _, dep := range g.rdeps[id] {
				indeg[dep]--
			}
		}
		layers = append(layers, layer)
	}
	if len(drained) == len(g.order) {
		return layers, nil
	}
	undrained := map[ID]bool{}
	for _, id := range g.order {
		if !drained[id] {
			undrained[id] = true
		}
	}
	return layers, undrained
}

// TransitiveDeps returns everything id depends on, directly or through any
// chain of dependencies, in insertion order. An unknown id yields nil. On a
// graph containing a cycle through id, id appears in its own closure, which is
// the honest answer rather than a special case.
func (g *Graph[ID, N]) TransitiveDeps(id ID) []ID {
	closure := g.closure(id)
	var out []ID
	for _, n := range g.order {
		if closure[n] {
			out = append(out, n)
		}
	}
	return out
}

// closure returns the set of everything id depends on transitively.
func (g *Graph[ID, N]) closure(id ID) map[ID]bool {
	out := map[ID]bool{}
	if !g.Has(id) {
		return out
	}
	stack := slices.Clone(g.deps[id])
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[cur] {
			continue
		}
		out[cur] = true
		stack = append(stack, g.deps[cur]...)
	}
	return out
}

// Independent reports whether a and b are free of any dependency relationship
// — neither appears in the other's transitive dependency closure — which is
// the dependency graph's half of the two-graph parallelism rule. A node is
// never independent of itself, and an unknown id is never independent of
// anything, because nothing is known about what it needs.
func (g *Graph[ID, N]) Independent(a, b ID) bool {
	return g.independentVia(a, b, g.closure)
}

// independentVia is Independent over a caller-held closure lookup, so a call
// site comparing many pairs can compute each node's closure once instead of
// once per pair.
func (g *Graph[ID, N]) independentVia(a, b ID, closureOf func(ID) map[ID]bool) bool {
	if a == b || !g.Has(a) || !g.Has(b) {
		return false
	}
	return !closureOf(a)[b] && !closureOf(b)[a]
}
