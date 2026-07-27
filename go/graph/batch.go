package graph

import "errors"

// ErrNoProver reports a batching call made without a Prover, which would leave
// the resource-surface half of the admission rule unanswered.
var ErrNoProver = errors.New("graph: batching needs a prover")

// ErrNoSurfaces reports a batching call made without a surface lookup. A
// lookup that returns nil for a node is fine and means "declares nothing"; no
// lookup at all is not.
var ErrNoSurfaces = errors.New("graph: batching needs a surface lookup")

// ErrNoProgress reports a batching round that could admit nothing while nodes
// remained. An acyclic graph cannot produce one — some unscheduled node always
// has all its dependencies behind it — so this is what a graph mutated
// underneath a Batch call surfaces as, instead of a loop that never ends.
var ErrNoProgress = errors.New("graph: batching made no progress")

// BatchOption configures Batch.
type BatchOption func(*batchConfig)

type batchConfig struct {
	maxGroup int
}

// WithMaxGroupSize caps how many nodes may share one batch. Zero or less means
// no cap, which is the default: how much parallelism is affordable is a
// property of whoever runs the work — reviewer load, machine count, budget —
// and never of the graph.
func WithMaxGroupSize(n int) BatchOption {
	return func(c *batchConfig) { c.maxGroup = n }
}

// Deferral records one candidate that could not join the group being formed,
// and what stopped it. These are the whole record of where parallelism was
// lost: a run whose batches are all of size one has a Ground on every deferral
// saying which half of the admission rule refused, and in which resource
// domain.
type Deferral[ID comparable] struct {
	Node ID `json:"node"`
	// Blocker is the member that refused the candidate. It is the zero ID
	// when nothing about a specific member was the problem.
	Blocker ID      `json:"blocker,omitzero"`
	Verdict Verdict `json:"verdict"`
}

// Group is one batch: nodes that may all run at the same time, plus the
// candidates that were ready to run and did not make it in.
type Group[ID comparable] struct {
	Members  []ID           `json:"members"`
	Deferred []Deferral[ID] `json:"deferred,omitempty"`
}

// Schedule is a whole graph partitioned into batches, in the order they must
// run. Every node appears in exactly one group, and no node appears before a
// group containing something it depends on.
type Schedule[ID comparable] struct {
	Groups []Group[ID] `json:"groups"`
}

// CanCoBatch answers the two-graph question for one pair: may a and b run at
// the same time? Both graphs must agree. The dependency graph must show them
// independent — neither in the other's transitive closure — and the prover
// must prove their declared surfaces disjoint. The verdict explains the
// answer; when the dependency graph is what refused — the pair is linked, or
// the graph does not hold one of them — no surface comparison was made, so the
// Relation says nothing about resources and the Ground says which it was.
//
// This is the rule Batch admits by, exported so a batch formed elsewhere can
// be checked against it and so any admission can be re-derived afterwards from
// the same inputs.
func (g *Graph[ID, N]) CanCoBatch(a, b ID, surfaces func(ID) Surface, p *Prover) (bool, Verdict) {
	return g.coBatch(a, b, g.closure, surfaces, p)
}

// coBatch is CanCoBatch over a caller-held closure lookup, so Batch computes
// each node's dependency closure once rather than once per pair it considers.
func (g *Graph[ID, N]) coBatch(a, b ID, closureOf func(ID) map[ID]bool, surfaces func(ID) Surface, p *Prover) (bool, Verdict) {
	switch {
	case p == nil || surfaces == nil:
		return false, Verdict{Relation: RelationUnknown, Ground: GroundUndecidable}
	case !g.Has(a) || !g.Has(b):
		return false, Verdict{Relation: RelationUnknown, Ground: GroundUnknownNode}
	case !g.independentVia(a, b, closureOf):
		return false, Verdict{Relation: RelationUnknown, Ground: GroundDependencyLinked}
	}
	v := p.Relate(surfaces(a), surfaces(b))
	return v.Disjoint(), v
}

// Batch partitions the graph into an ordered schedule of batches by composing
// its two graphs: dependencies decide when a node becomes a candidate, and
// declared resource surfaces decide who it may run beside once it is one.
//
// Each round takes every unscheduled node whose dependencies are all already
// scheduled, walks them in insertion order, and admits a candidate only if
// CanCoBatch clears it against every member admitted so far. A candidate that
// clears nothing still forms the next batch on its own, so a node with an
// undeclared, overlapping or undecidable surface runs alone rather than
// blocking the schedule — the batch-of-one fallback that makes the
// unsafe-unless-provably-disjoint bias affordable. Everything a round turns
// away is recorded on the group, with the ground it was turned away on.
//
// surfaces maps a node to what it declares it will touch; returning nil for a
// node means it declares nothing, which is a batch of one. A cyclic graph has
// no schedule, so the error is a *CycleError naming the cycle, and a round
// that somehow admits nothing stops with ErrNoProgress rather than spinning.
func (g *Graph[ID, N]) Batch(surfaces func(ID) Surface, p *Prover, opts ...BatchOption) (Schedule[ID], error) {
	switch {
	case p == nil:
		return Schedule[ID]{}, ErrNoProver
	case surfaces == nil:
		return Schedule[ID]{}, ErrNoSurfaces
	}
	var cfg batchConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := g.DetectCycles(); err != nil {
		return Schedule[ID]{}, err
	}

	closures := make(map[ID]map[ID]bool, len(g.order))
	closureOf := func(id ID) map[ID]bool {
		c, ok := closures[id]
		if !ok {
			c = g.closure(id)
			closures[id] = c
		}
		return c
	}

	scheduled := make(map[ID]bool, len(g.order))
	var out Schedule[ID]
	for len(scheduled) < len(g.order) {
		group := Group[ID]{}
		for _, id := range g.order {
			if scheduled[id] || !g.ready(id, scheduled) {
				continue
			}
			if cfg.maxGroup > 0 && len(group.Members) >= cfg.maxGroup {
				group.Deferred = append(group.Deferred, Deferral[ID]{
					Node:    id,
					Verdict: Verdict{Relation: RelationUnknown, Ground: GroundGroupFull},
				})
				continue
			}
			if blocker, v, ok := g.admits(id, group.Members, closureOf, surfaces, p); !ok {
				group.Deferred = append(group.Deferred, Deferral[ID]{Node: id, Blocker: blocker, Verdict: v})
				continue
			}
			group.Members = append(group.Members, id)
		}
		if len(group.Members) == 0 {
			return Schedule[ID]{}, ErrNoProgress
		}
		for _, id := range group.Members {
			scheduled[id] = true
		}
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

// ready reports whether every dependency of id has already been scheduled.
func (g *Graph[ID, N]) ready(id ID, scheduled map[ID]bool) bool {
	for _, d := range g.deps[id] {
		if !scheduled[d] {
			return false
		}
	}
	return true
}

// admits runs the admission rule against every member already in the group,
// returning the first member to refuse the candidate along with its verdict.
// An empty group admits unconditionally, which is what guarantees a batch of
// one is always available.
func (g *Graph[ID, N]) admits(id ID, members []ID, closureOf func(ID) map[ID]bool, surfaces func(ID) Surface, p *Prover) (ID, Verdict, bool) {
	for _, m := range members {
		if ok, v := g.coBatch(id, m, closureOf, surfaces, p); !ok {
			return m, v, false
		}
	}
	var none ID
	return none, Verdict{Relation: RelationDisjoint, Ground: GroundProved}, true
}
