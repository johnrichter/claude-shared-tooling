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
	if got := exact.Relate(Claim{Kind: NameExact, Value: "Orders"}, Claim{Kind: NameExact, Value: "orders"}); got != RelationDisjoint {
		t.Fatalf("exact NamespaceDomain merged spellings: got %s", got)
	}
	folded := NamespaceDomain("pkg", WithNamespaceFold(strings.ToLower))
	if got := folded.Relate(Claim{Kind: NameExact, Value: "Orders"}, Claim{Kind: NameExact, Value: "orders"}); got != RelationOverlap {
		t.Fatalf("folded NamespaceDomain = %s, want overlap on equated spellings", got)
	}
}

// TestPathRootTakesPrecedenceOverValue confirms the workspace/the-work vs
// the-work pair: two claims for the identical value are disjoint the instant
// their roots differ, and overlap once rooted the same way — the root
// decides before any path-text rule runs, matching the doc comment's claim
// that a root difference is checked "regardless of path text".
func TestPathRootTakesPrecedenceOverValue(t *testing.T) {
	d := PathDomain("path")
	workspaceWork := Claim{Kind: PathDir, Value: "the-work", Root: "workspace"}
	otherWork := Claim{Kind: PathDir, Value: "the-work", Root: "the-work"}
	sameRootWork := Claim{Kind: PathDir, Value: "the-work", Root: "workspace"}

	if got := d.Relate(workspaceWork, otherWork); got != RelationDisjoint {
		t.Fatalf("Relate(%s, %s) = %s, want %s: different roots must win over an identical value", workspaceWork, otherWork, got, RelationDisjoint)
	}
	if got := d.Relate(workspaceWork, sameRootWork); got != RelationOverlap {
		t.Fatalf("Relate(%s, %s) = %s, want %s: identical root and value must overlap", workspaceWork, sameRootWork, got, RelationOverlap)
	}
	// A root difference must also beat a claim shape that would otherwise be
	// undecidable (mismatched abs/relative), proving root is checked first.
	absWork := Claim{Kind: PathFile, Value: "/the-work", Root: "workspace"}
	relWork := Claim{Kind: PathFile, Value: "the-work", Root: "other"}
	if got := d.Relate(absWork, relWork); got != RelationDisjoint {
		t.Fatalf("Relate(%s, %s) = %s, want %s: differing roots must be decided before the abs/relative mismatch", absWork, relWork, got, RelationDisjoint)
	}
}

// TestPathRootDefaultsAreAdditive confirms the zero-value Root reproduces
// exactly today's rootless behaviour (no domain default, no per-claim root):
// two equal-valued claims overlap and two different-valued claims are judged
// purely on the pre-existing path rules, so the new field cannot regress a
// caller who never sets it.
func TestPathRootDefaultsAreAdditive(t *testing.T) {
	d := PathDomain("path")
	a := Claim{Kind: PathFile, Value: "svc/a.go"}
	b := Claim{Kind: PathFile, Value: "svc/a.go"}
	if got := d.Relate(a, b); got != RelationOverlap {
		t.Fatalf("Relate(%s, %s) with zero-value Root = %s, want %s", a, b, got, RelationOverlap)
	}
	c := Claim{Kind: PathFile, Value: "svc/b.go"}
	if got := d.Relate(a, c); got != RelationDisjoint {
		t.Fatalf("Relate(%s, %s) with zero-value Root = %s, want %s", a, c, got, RelationDisjoint)
	}
}

// TestWithPathRootDefaultsUnrootedClaims confirms WithPathRoot only fills in
// for a claim that leaves its own Root empty: an explicit domain default
// makes two otherwise-unrooted claims agree with an explicitly rooted one,
// but never overrides a claim that set its own, different root.
func TestWithPathRootDefaultsUnrootedClaims(t *testing.T) {
	d := PathDomain("path", WithPathRoot("workspace"))
	unrooted := Claim{Kind: PathFile, Value: "the-work/a.go"}
	explicitSameRoot := Claim{Kind: PathFile, Value: "the-work/a.go", Root: "workspace"}
	if got := d.Relate(unrooted, explicitSameRoot); got != RelationOverlap {
		t.Fatalf("Relate(%s, %s) = %s, want %s: domain default root must equate an unrooted claim with one naming it explicitly", unrooted, explicitSameRoot, got, RelationOverlap)
	}
	explicitOtherRoot := Claim{Kind: PathFile, Value: "the-work/a.go", Root: "the-work"}
	if got := d.Relate(unrooted, explicitOtherRoot); got != RelationDisjoint {
		t.Fatalf("Relate(%s, %s) = %s, want %s: a claim's own root must not be overridden by the domain default", unrooted, explicitOtherRoot, got, RelationDisjoint)
	}
}

// TestPathRootRespectsCaseFold confirms a root is subject to the same case
// fold as a value, so a caller relying on the default fold does not get a
// root comparison that is silently exact while the value comparison beside
// it is not.
func TestPathRootRespectsCaseFold(t *testing.T) {
	folded := PathDomain("path")
	a := Claim{Kind: PathFile, Value: "a.go", Root: "Workspace"}
	b := Claim{Kind: PathFile, Value: "a.go", Root: "workspace"}
	if got := folded.Relate(a, b); got != RelationOverlap {
		t.Fatalf("Relate(%s, %s) under default fold = %s, want %s: roots differing only by case must fold together", a, b, got, RelationOverlap)
	}
	exact := PathDomain("path", WithPathFold(nil))
	if got := exact.Relate(a, b); got != RelationDisjoint {
		t.Fatalf("Relate(%s, %s) under exact comparison = %s, want %s: roots must not be folded when folding is off", a, b, got, RelationDisjoint)
	}
}

// TestPathDomainRootSoundness runs the falsification hook over claims that mix
// two roots with the same values and directory nesting, using an oracle that
// scopes every resource to its claim's root (so "workspace:the-work/a.go" and
// "the-work:the-work/a.go" are different resources). This is what would catch
// a comparator that let a shared value leak across roots.
func TestPathDomainRootSoundness(t *testing.T) {
	values := []string{"the-work", "the-work/a.go", "the-work/sub", "the-work/sub/b.go", "other"}
	roots := []string{"", "workspace", "the-work"}
	var claims []Claim
	for _, r := range roots {
		for _, v := range values {
			claims = append(claims,
				Claim{Kind: PathFile, Value: v, Root: r},
				Claim{Kind: PathDir, Value: v, Root: r},
				Claim{Kind: PathGlob, Value: v + "/*", Root: r})
		}
	}
	extent := func(c Claim) []string {
		var out []string
		for _, v := range values {
			hit := false
			switch c.Kind {
			case PathFile:
				hit = v == c.Value
			case PathDir:
				hit = v == c.Value || strings.HasPrefix(v, c.Value+"/")
			case PathGlob:
				prefix := strings.TrimSuffix(c.Value, "*")
				hit = strings.HasPrefix(v, prefix) && !strings.Contains(strings.TrimPrefix(v, prefix), "/")
			}
			if hit {
				out = append(out, c.Root+":"+v)
			}
		}
		return out
	}
	d := PathDomain("path")
	bad := CheckDomainSoundness(d, claims, extent)
	for i, u := range bad {
		if i == 10 {
			t.Fatalf("... %d counterexamples in total", len(bad))
		}
		t.Errorf("unsound root disjointness: %s", u)
	}
}

// TestRootedClaimAndVariadicDomainCompile is a compile-time and behavioural
// check that a keyed Claim literal naming Root, and a PathDomain built with
// several variadic options including WithPathRoot, still build and combine —
// the two call shapes the reasoning cue requires to keep compiling.
func TestRootedClaimAndVariadicDomainCompile(t *testing.T) {
	claim := Claim{Kind: PathFile, Value: "the-work/a.go", Root: "workspace"}
	d := PathDomain("path", WithPathFold(strings.ToLower), WithPathRoot("workspace"), WithPathMatcher(globstarMatch))
	if got := d.Relate(claim, claim); got != RelationOverlap {
		t.Fatalf("Relate(claim, claim) = %s, want %s", got, RelationOverlap)
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
