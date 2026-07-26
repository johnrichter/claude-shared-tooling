package graph

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestSelfEdgeIsACycle confirms a self-edge is treated as a one-node cycle,
// not silently accepted as a trivial dependency.
func TestSelfEdgeIsACycle(t *testing.T) {
	g := newStringGraph(t, []string{"a"}, nil)
	if err := g.AddDep("a", "a"); err != nil {
		t.Fatalf("AddDep(a, a): %v", err)
	}
	err := g.DetectCycles()
	if err == nil {
		t.Fatal("self-edge not reported as a cycle")
	}
	if !strings.Contains(err.Error(), "a -> a") {
		t.Fatalf("cycle error %q does not name the self-loop", err.Error())
	}
	if _, err := g.TopoSort(); err == nil {
		t.Fatal("TopoSort succeeded over a self-cycle")
	}
}

// TestTwoDisjointCyclesBothReported confirms DetectCycles names every cycle in
// a graph that has more than one, not just the first it reaches, and that a
// downstream node untouched by either cycle still gets no phantom cycle.
func TestTwoDisjointCyclesBothReported(t *testing.T) {
	g := newStringGraph(t,
		[]string{"a", "b", "x", "y", "clean"},
		map[string][]string{"a": {"b"}, "b": {"a"}, "x": {"y"}, "y": {"x"}, "clean": {"a"}})
	err := g.DetectCycles()
	var ce *CycleError[string]
	if !errors.As(err, &ce) {
		t.Fatalf("DetectCycles = %v, want *CycleError", err)
	}
	if len(ce.Cycles) != 2 {
		t.Fatalf("Cycles = %v, want exactly 2 distinct cycles", ce.Cycles)
	}
}

// TestBatchRejectsMissingCollaborators confirms Batch fails fast, naming the
// missing collaborator, rather than silently degrading to unsafe-everywhere.
func TestBatchRejectsMissingCollaborators(t *testing.T) {
	g := newStringGraph(t, []string{"a"}, nil)
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	surfaces := func(string) Surface { return nil }

	if _, err := g.Batch(nil, p); !errors.Is(err, ErrNoSurfaces) {
		t.Fatalf("Batch(nil surfaces) = %v, want ErrNoSurfaces", err)
	}
	if _, err := g.Batch(surfaces, nil); !errors.Is(err, ErrNoProver) {
		t.Fatalf("Batch(nil prover) = %v, want ErrNoProver", err)
	}
}

// TestBatchOverCyclicGraphFails confirms Batch refuses to schedule a cyclic
// graph and reports the cycle rather than looping or panicking.
func TestBatchOverCyclicGraphFails(t *testing.T) {
	g := newStringGraph(t, []string{"a", "b"}, map[string][]string{"a": {"b"}, "b": {"a"}})
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	surfaces := func(string) Surface { return Surface{"path": {}} }
	if _, err := g.Batch(surfaces, p); !errors.As(err, new(*CycleError[string])) {
		t.Fatalf("Batch over a cycle = %v, want *CycleError", err)
	}
}

// TestNewProverRejectsMalformedDomains confirms every domain-registration
// invariant is enforced rather than trusted: no domains at all, an unnamed
// domain, a domain with no Relate function, and a duplicate name.
func TestNewProverRejectsMalformedDomains(t *testing.T) {
	if _, err := NewProver(); !errors.Is(err, ErrNoDomains) {
		t.Fatalf("NewProver() = %v, want ErrNoDomains", err)
	}
	if _, err := NewProver(Domain{Name: "", Relate: func(Claim, Claim) Relation { return RelationDisjoint }}); err == nil {
		t.Fatal("NewProver accepted a domain with no name")
	}
	if _, err := NewProver(Domain{Name: "x"}); err == nil {
		t.Fatal("NewProver accepted a domain with a nil Relate")
	}
	dup := Domain{Name: "path", Relate: func(Claim, Claim) Relation { return RelationDisjoint }}
	if _, err := NewProver(dup, dup); err == nil {
		t.Fatal("NewProver accepted two domains with the same name")
	}
}

// TestUnregisteredDomainBeatsUndeclared confirms Relate's precedence: a domain
// neither surface declares but that IS registered is "undeclared", while a
// domain a surface mentions that is NOT registered is "unregistered" and wins
// even when it is only one side that mentions it.
func TestUnregisteredDomainBeatsUndeclared(t *testing.T) {
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	a := Surface{"path": {}, "ghost": {{Value: "x"}}}
	b := Surface{"path": {}}
	v := p.Relate(a, b)
	if v.Ground != GroundUnregistered || v.Domain != "ghost" {
		t.Fatalf("Relate = %+v, want unregistered-domain naming ghost", v)
	}
}

// TestResolveRefsCanonicalCollisionIsAmbiguous confirms that when a broken id
// scheme renders two distinct nodes to the same canonical text, resolving
// that text is reported as ambiguous rather than silently picking one — the
// same guard as a short-form collision, applied to the primary index.
func TestResolveRefsCanonicalCollisionIsAmbiguous(t *testing.T) {
	scheme := IDScheme[int]{String: func(int) string { return "same" }}
	g := New[int, struct{}](scheme)
	if err := g.AddNode(1, struct{}{}); err != nil {
		t.Fatalf("AddNode(1): %v", err)
	}
	if err := g.AddNode(2, struct{}{}); err != nil {
		t.Fatalf("AddNode(2): %v", err)
	}
	res := g.ResolveRefs("same")
	if len(res.Ambiguous) != 1 || len(res.Ambiguous[0].Candidates) != 2 {
		t.Fatalf("ResolveRefs over a canonical collision = %+v, want one ambiguity with two candidates", res)
	}
	if _, resolved := res.Resolved["same"]; resolved {
		t.Fatal("colliding canonical id resolved instead of being reported ambiguous")
	}
}

// TestIsTreeEdgeCases confirms an empty graph and a forest of two disjoint
// roots are both correctly refused as "not a tree" — the boundary cases the
// doc comment calls out.
func TestIsTreeEdgeCases(t *testing.T) {
	empty := New[string, struct{}](StringIDs())
	if empty.IsTree() {
		t.Fatal("empty graph reported as a tree")
	}
	forest := newStringGraph(t, []string{"r1", "r2", "leaf"}, map[string][]string{"leaf": {"r1"}})
	if forest.IsTree() {
		t.Fatal("a forest of two roots reported as a single tree")
	}
}

// TestConcurrentReadersAreSafe exercises the doc.go claim that a Graph is
// safe for concurrent readers: many goroutines hammer TopoSort, Layers,
// DetectCycles and TransitiveDeps on one shared, unmutated graph. Run with
// -race; a data race here is a doc-comment promise broken.
func TestConcurrentReadersAreSafe(t *testing.T) {
	g := newStringGraph(t,
		[]string{"a", "b", "c", "d"},
		map[string][]string{"b": {"a"}, "c": {"a"}, "d": {"b", "c"}})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.TopoSort(); err != nil {
				t.Errorf("concurrent TopoSort: %v", err)
			}
			if _, err := g.Layers(); err != nil {
				t.Errorf("concurrent Layers: %v", err)
			}
			if err := g.DetectCycles(); err != nil {
				t.Errorf("concurrent DetectCycles: %v", err)
			}
			_ = g.TransitiveDeps("d")
			_ = g.Independent("b", "c")
		}()
	}
	wg.Wait()
}

// TestBatchGroundsAreExhaustiveOnFallback confirms every batch-of-one fallback
// in a larger, mixed schedule carries a Ground explaining the refusal — the
// property the doc comment calls "the whole record of where parallelism was
// lost" — so an all-singleton run is diagnosable rather than mute.
func TestBatchGroundsAreExhaustiveOnFallback(t *testing.T) {
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	// Every node overlaps every other: nothing can ever co-batch.
	surfaces := func(string) Surface { return Surface{"path": {{Kind: PathDir, Value: "."}}} }
	g := newStringGraph(t, []string{"a", "b", "c"}, nil)
	sched, err := g.Batch(surfaces, p)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(sched.Groups) != 3 {
		t.Fatalf("Groups = %v, want 3 singleton batches when nothing is disjoint", sched.Groups)
	}
	for _, grp := range sched.Groups {
		if len(grp.Members) != 1 {
			t.Fatalf("group %v is not a singleton", grp.Members)
		}
	}
	if grounds := sched.Groups[0].Deferred; len(grounds) == 0 || grounds[0].Verdict.Ground != GroundClaimOverlap {
		t.Fatalf("first group deferred = %v, want a claim-overlap ground", grounds)
	}
}

// TestNamespaceFoldMergesSpellings confirms WithNamespaceFold is honoured —
// two spellings a caller's fold equates are treated as one resource, which
// the default exact comparison would wrongly call disjoint.
func TestNamespaceFoldMergesSpellings(t *testing.T) {
	exact := NamespaceDomain("pkg")
	if got := exact.Relate(Claim{NameExact, "Orders"}, Claim{NameExact, "orders"}); got != RelationDisjoint {
		t.Fatalf("exact NamespaceDomain merged spellings: got %s", got)
	}
	folded := NamespaceDomain("pkg", WithNamespaceFold(strings.ToLower))
	if got := folded.Relate(Claim{NameExact, "Orders"}, Claim{NameExact, "orders"}); got != RelationOverlap {
		t.Fatalf("folded NamespaceDomain = %s, want overlap on equated spellings", got)
	}
}

// TestRelationAndGroundValidAreExhaustive confirms the closed vocabularies
// reject anything a caller might invent, which is what lets a caller
// validating external input (a stored schedule, a replayed verdict) trust the
// set without restating it.
func TestRelationAndGroundValidAreExhaustive(t *testing.T) {
	if Relation("bogus").Valid() {
		t.Fatal("an invented Relation reported valid")
	}
	if Ground("bogus").Valid() {
		t.Fatal("an invented Ground reported valid")
	}
	for _, r := range []Relation{RelationUnknown, RelationOverlap, RelationDisjoint} {
		if !r.Valid() {
			t.Fatalf("defined Relation %q reported invalid", r)
		}
	}
}
