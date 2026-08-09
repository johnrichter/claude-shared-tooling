package graph

import (
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
)

// ticket is a node id with no hierarchy and no ordering grammar, used to prove
// the graph carries no id scheme of its own.
type ticket struct {
	Queue string
	Num   int
}

// ticketIDs renders a ticket as "QUEUE-<n>", with the queue alone as the short
// form so several tickets deliberately collide on it.
func ticketIDs() IDScheme[ticket] {
	return IDScheme[ticket]{
		String: func(t ticket) string { return t.Queue + "-" + itoa(t.Num) },
		Short:  func(t ticket) string { return t.Queue },
	}
}

// itoa renders a small non-negative int without pulling in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// newStringGraph builds a payload-free string-keyed graph from an
// id-to-dependencies table, in the order the ids are listed.
func newStringGraph(t *testing.T, ids []string, deps map[string][]string) *Graph[string, struct{}] {
	t.Helper()
	g := New[string, struct{}](StringIDs())
	for _, id := range ids {
		if err := g.AddNode(id, struct{}{}); err != nil {
			t.Fatalf("AddNode(%q): %v", id, err)
		}
	}
	for id, ds := range deps {
		for _, d := range ds {
			if err := g.AddDep(id, d); err != nil {
				t.Fatalf("AddDep(%q, %q): %v", id, d, err)
			}
		}
	}
	return g
}

// TestTopoSortAndLayers confirms Kahn's algorithm orders dependencies before
// dependents, breaks ties by insertion order, and cuts the same graph into
// frontiers whose members are pairwise independent.
func TestTopoSortAndLayers(t *testing.T) {
	g := newStringGraph(t,
		[]string{"deploy", "build", "lint", "fetch"},
		map[string][]string{"build": {"fetch"}, "lint": {"fetch"}, "deploy": {"build", "lint"}})

	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	want := []string{"fetch", "build", "lint", "deploy"}
	if !slices.Equal(order, want) {
		t.Fatalf("TopoSort = %v, want %v", order, want)
	}

	layers, err := g.Layers()
	if err != nil {
		t.Fatalf("Layers: %v", err)
	}
	wantLayers := [][]string{{"fetch"}, {"build", "lint"}, {"deploy"}}
	if len(layers) != len(wantLayers) {
		t.Fatalf("Layers = %v, want %v", layers, wantLayers)
	}
	for i := range layers {
		if !slices.Equal(layers[i], wantLayers[i]) {
			t.Fatalf("Layers[%d] = %v, want %v", i, layers[i], wantLayers[i])
		}
	}
	if !g.Independent("build", "lint") {
		t.Fatal("build and lint share a layer but are reported dependency-linked")
	}
	if g.Independent("deploy", "fetch") {
		t.Fatal("deploy transitively depends on fetch but is reported independent")
	}
	if got := g.TransitiveDeps("deploy"); !slices.Equal(got, []string{"build", "lint", "fetch"}) {
		t.Fatalf("TransitiveDeps(deploy) = %v", got)
	}
}

// TestBuildAndMutate confirms the same type carries a tree and a DAG, that
// referential integrity survives mutation, and that the guards on duplicate
// and unknown nodes hold.
func TestBuildAndMutate(t *testing.T) {
	g := newStringGraph(t,
		[]string{"root", "left", "right", "leaf"},
		map[string][]string{"left": {"root"}, "right": {"root"}, "leaf": {"left"}})

	if !g.IsTree() {
		t.Fatal("a single rooted tree was not recognised as one")
	}
	if !slices.Equal(g.Roots(), []string{"root"}) || !slices.Equal(g.Leaves(), []string{"right", "leaf"}) {
		t.Fatalf("Roots = %v, Leaves = %v", g.Roots(), g.Leaves())
	}
	// A second parent turns the tree into a DAG without changing anything else.
	if err := g.AddDep("leaf", "right"); err != nil {
		t.Fatalf("AddDep: %v", err)
	}
	if g.IsTree() {
		t.Fatal("a node with two parents is still reported as a tree")
	}
	if _, err := g.TopoSort(); err != nil {
		t.Fatalf("TopoSort over the DAG: %v", err)
	}
	// Repeating an edge must not double-count it, or Kahn's in-degrees drift.
	if err := g.AddDep("leaf", "right"); err != nil {
		t.Fatalf("repeat AddDep: %v", err)
	}
	if got := g.Deps("leaf"); !slices.Equal(got, []string{"left", "right"}) {
		t.Fatalf("Deps(leaf) = %v after a repeated edge", got)
	}

	if err := g.AddNode("root", struct{}{}); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("AddNode on an existing id = %v, want ErrDuplicateNode", err)
	}
	if err := g.AddDep("leaf", "ghost"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("AddDep to a missing node = %v, want ErrUnknownNode", err)
	}
	if err := g.RemoveNode("left"); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}
	if got := g.Deps("leaf"); !slices.Equal(got, []string{"right"}) {
		t.Fatalf("Deps(leaf) = %v after removing left; edges were left dangling", got)
	}
	if got := g.Dependents("root"); !slices.Equal(got, []string{"right"}) {
		t.Fatalf("Dependents(root) = %v after removing left", got)
	}
	if _, err := g.TopoSort(); err != nil {
		t.Fatalf("TopoSort after removal: %v", err)
	}
}

// TestDetectCyclesNamesThePath confirms a cycle is reported as the walk that
// closes it, in the error text and as typed ids, and that the ordering calls
// surface the same error.
func TestDetectCyclesNamesThePath(t *testing.T) {
	g := newStringGraph(t,
		[]string{"a", "b", "c", "spectator"},
		map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}, "spectator": {"a"}})

	err := g.DetectCycles()
	if err == nil {
		t.Fatal("DetectCycles found no cycle in a cyclic graph")
	}
	if got := err.Error(); !strings.Contains(got, "a -> b -> c -> a") {
		t.Fatalf("cycle error %q does not name the offending path", got)
	}
	var ce *CycleError[string]
	if !errors.As(err, &ce) {
		t.Fatalf("cycle error is %T, want *CycleError[string]", err)
	}
	if len(ce.Cycles) != 1 || !slices.Equal(ce.Cycles[0], []string{"a", "b", "c"}) {
		t.Fatalf("Cycles = %v, want one cycle a,b,c", ce.Cycles)
	}
	if _, err := g.TopoSort(); !errors.As(err, &ce) {
		t.Fatalf("TopoSort error is %v, want a *CycleError", err)
	}
	if _, err := g.Layers(); !errors.As(err, &ce) {
		t.Fatalf("Layers error is %v, want a *CycleError", err)
	}
	if err := g.RemoveDep("c", "a"); err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}
	if err := g.DetectCycles(); err != nil {
		t.Fatalf("cycle still reported after the closing edge was removed: %v", err)
	}
}

// TestPluggableIDsAndRefGuard confirms a graph keyed by a non-textual id works
// unchanged, that canonical ids resolve, and that a short form several nodes
// answer to resolves to none of them.
func TestPluggableIDsAndRefGuard(t *testing.T) {
	g := New[ticket, string](ticketIDs())
	tickets := []ticket{{"OPS", 4}, {"OPS", 9}, {"DOCS", 1}}
	for _, tk := range tickets {
		if err := g.AddNode(tk, "work for "+tk.Queue); err != nil {
			t.Fatalf("AddNode(%v): %v", tk, err)
		}
	}
	if err := g.AddDep(tickets[1], tickets[0]); err != nil {
		t.Fatalf("AddDep: %v", err)
	}
	if _, err := g.TopoSort(); err != nil {
		t.Fatalf("TopoSort over struct ids: %v", err)
	}

	res := g.ResolveRefs("OPS-4", "DOCS", "OPS", "NOPE-1")
	if got, ok := res.Resolved["OPS-4"]; !ok || got != (ticket{"OPS", 4}) {
		t.Fatalf("canonical ref did not resolve: %v", res.Resolved)
	}
	if got, ok := res.Resolved["DOCS"]; !ok || got != (ticket{"DOCS", 1}) {
		t.Fatalf("unique short ref did not resolve: %v", res.Resolved)
	}
	if _, ok := res.Resolved["OPS"]; ok {
		t.Fatal("short ref shared by two nodes resolved instead of being guarded")
	}
	if len(res.Ambiguous) != 1 || res.Ambiguous[0].Ref != "OPS" || len(res.Ambiguous[0].Candidates) != 2 {
		t.Fatalf("Ambiguous = %v, want OPS with two candidates", res.Ambiguous)
	}
	if !slices.Equal(res.Unknown, []string{"NOPE-1"}) {
		t.Fatalf("Unknown = %v, want [NOPE-1]", res.Unknown)
	}
	if res.OK() {
		t.Fatal("OK() is true despite an unresolved and an ambiguous reference")
	}
	if msg := res.Err(g.Scheme()).Error(); !strings.Contains(msg, "OPS-4") || !strings.Contains(msg, "NOPE-1") {
		t.Fatalf("Err() = %q, want the candidates and the dangling ref named", msg)
	}
}

// TestBatchBiasesUnsafe confirms the two-graph rule: provably disjoint
// surfaces share a batch, while an overlap, an undeclared domain, a domain
// nobody registered, and an undecidable claim each fall back to a batch of
// one.
func TestBatchBiasesUnsafe(t *testing.T) {
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	file := func(v string) []Claim { return []Claim{{Kind: PathFile, Value: v}} }
	surfaces := map[string]Surface{
		"alpha":        {"path": file("svc/alpha.go")},
		"beta":         {"path": file("svc/beta.go")},
		"wide":         {"path": {{Kind: PathDir, Value: "svc"}}},
		"silent":       nil,
		"empty":        {"path": {}},
		"foreign":      {"database": {{Kind: NameExact, Value: "orders"}}},
		"unparseable":  {"path": {{Kind: "regex", Value: "^svc/"}}},
		"other-empty":  {"path": {}},
		"other-parcel": {"path": file("docs/readme.md")},
	}
	ids := []string{"alpha", "beta", "wide", "silent", "empty", "foreign", "unparseable", "other-empty", "other-parcel"}
	g := newStringGraph(t, ids, nil)

	sched, err := g.Batch(func(id string) Surface { return surfaces[id] }, p)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(sched.Groups) == 0 {
		t.Fatal("Batch produced no groups")
	}
	first := sched.Groups[0].Members
	for _, want := range []string{"alpha", "beta", "other-parcel"} {
		if !slices.Contains(first, want) {
			t.Fatalf("first batch %v omits provably disjoint node %q", first, want)
		}
	}
	for _, unsafe := range []string{"wide", "silent", "foreign", "unparseable"} {
		if slices.Contains(first, unsafe) {
			t.Fatalf("first batch %v admitted %q, which is not provably disjoint", first, unsafe)
		}
	}
	// Two surfaces that both declare the path domain empty touch nothing, so
	// they are provably disjoint from everything, including each other.
	if !slices.Contains(first, "empty") || !slices.Contains(first, "other-empty") {
		t.Fatalf("first batch %v turned away an explicitly empty surface", first)
	}
	// Every node lands in exactly one group, and every unsafe one lands alone.
	seen := map[string]int{}
	for _, grp := range sched.Groups {
		for _, m := range grp.Members {
			seen[m]++
		}
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("node %q scheduled %d times, want once", id, seen[id])
		}
	}
	for _, grp := range sched.Groups[1:] {
		if len(grp.Members) != 1 {
			t.Fatalf("fallback group %v is not a batch of one", grp.Members)
		}
	}

	wantGrounds := map[string]Ground{
		"wide":        GroundClaimOverlap,
		"silent":      GroundUndeclared,
		"foreign":     GroundUnregistered,
		"unparseable": GroundUndecidable,
	}
	for node, want := range wantGrounds {
		var got Ground
		for _, d := range sched.Groups[0].Deferred {
			if d.Node == node {
				got = d.Verdict.Ground
			}
		}
		if got != want {
			t.Fatalf("%q deferred on ground %q, want %q", node, got, want)
		}
	}
}

// TestBatchRespectsDependenciesAndCap confirms a dependent never shares a
// batch with what it depends on, and that the group-size cap is a parameter
// rather than a built-in ceiling.
func TestBatchRespectsDependenciesAndCap(t *testing.T) {
	p, err := NewProver(PathDomain("path"))
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	ids := []string{"one", "two", "three", "last"}
	g := newStringGraph(t, ids, map[string][]string{"last": {"one"}})
	surfaces := func(id string) Surface {
		return Surface{"path": {{Kind: PathFile, Value: id + ".go"}}}
	}

	sched, err := g.Batch(surfaces, p)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !slices.Equal(sched.Groups[0].Members, []string{"one", "two", "three"}) {
		t.Fatalf("first batch = %v, want the three independent nodes", sched.Groups[0].Members)
	}
	if !slices.Equal(sched.Groups[1].Members, []string{"last"}) {
		t.Fatalf("second batch = %v, want the dependent node alone", sched.Groups[1].Members)
	}
	if ok, v := g.CanCoBatch("last", "one", surfaces, p); ok || v.Ground != GroundDependencyLinked {
		t.Fatalf("CanCoBatch(last, one) = %v, %v; want refused as dependency-linked", ok, v)
	}

	capped, err := g.Batch(surfaces, p, WithMaxGroupSize(2))
	if err != nil {
		t.Fatalf("Batch capped: %v", err)
	}
	if len(capped.Groups[0].Members) != 2 {
		t.Fatalf("capped first batch = %v, want two members", capped.Groups[0].Members)
	}
	if len(capped.Groups[0].Deferred) == 0 || capped.Groups[0].Deferred[0].Verdict.Ground != GroundGroupFull {
		t.Fatalf("capped batch did not record a group-full deferral: %v", capped.Groups[0].Deferred)
	}
}

// TestSurfacesGeneralizeBeyondFiles confirms a node's surface can span several
// resource universes at once, and that a collision in a non-file universe
// blocks a batch just as a file collision does.
func TestSurfacesGeneralizeBeyondFiles(t *testing.T) {
	p, err := NewProver(
		PathDomain("path"),
		NamespaceDomain("package"),
		NamespaceDomain("lock", WithNamespaceSeparator("")),
	)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	surfaces := map[string]Surface{
		// Different files in the same package: text-disjoint, but both can
		// declare the same new symbol, so the package domain refuses them.
		"addFieldA": {
			"path":    {{Kind: PathFile, Value: "svc/store/a.go"}},
			"package": {{Kind: NameExact, Value: "svc/store"}},
			"lock":    {},
		},
		"addFieldB": {
			"path":    {{Kind: PathFile, Value: "svc/store/b.go"}},
			"package": {{Kind: NameExact, Value: "svc/store"}},
			"lock":    {},
		},
		// A different package, but it takes the release lock.
		"release": {
			"path":    {{Kind: PathFile, Value: "cmd/release.go"}},
			"package": {{Kind: NameExact, Value: "cmd"}},
			"lock":    {{Kind: NameExact, Value: "release"}},
		},
		"tag": {
			"path":    {{Kind: PathFile, Value: "cmd/tag.go"}},
			"package": {{Kind: NamePrefix, Value: "cmd/tag"}},
			"lock":    {{Kind: NameExact, Value: "release"}},
		},
	}
	g := newStringGraph(t, []string{"addFieldA", "addFieldB", "release", "tag"}, nil)
	lookup := func(id string) Surface { return surfaces[id] }

	if ok, v := g.CanCoBatch("addFieldA", "addFieldB", lookup, p); ok || v.Domain != "package" {
		t.Fatalf("same-package pair = %v, %v; want refused by the package domain", ok, v)
	}
	if ok, v := g.CanCoBatch("release", "tag", lookup, p); ok || v.Domain != "lock" {
		t.Fatalf("shared-lock pair = %v, %v; want refused by the lock domain", ok, v)
	}
	if ok, _ := g.CanCoBatch("addFieldA", "release", lookup, p); !ok {
		t.Fatal("pair disjoint in every domain was refused")
	}
}

// TestPathProofIsSound runs the falsification hook over a generated universe
// of paths and a generated set of claims: every pair the path domain proves
// disjoint must really cover no file in common, under the default case fold
// and under exact comparison alike. This is the check that fails when the
// unsafe-unless-provably-disjoint bias is wrong, which nothing downstream of a
// batch can do.
func TestPathProofIsSound(t *testing.T) {
	universe := pathUniverse()
	claims := pathClaims()
	for _, tc := range []struct {
		name   string
		domain Domain
		fold   func(string) string
	}{
		{"default fold", PathDomain("path"), strings.ToLower},
		{"exact", PathDomain("path", WithPathFold(nil)), func(s string) string { return s }},
		// Supplying the matcher the oracle itself uses is the contract
		// WithPathMatcher states; what this case checks is that everything the
		// matcher does not answer stays sound beside it.
		{"globstar matcher", PathDomain("path", WithPathMatcher(globstarMatch)), strings.ToLower},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := CheckDomainSoundness(tc.domain, claims, pathExtent(universe, tc.fold))
			for i, u := range bad {
				if i == 10 {
					t.Fatalf("... %d counterexamples in total", len(bad))
				}
				t.Errorf("unsound disjointness: %s", u)
			}
		})
	}
}

// TestNamespaceProofIsSound is the same falsification pass for the namespace
// domain, over names that share prefixes without nesting.
func TestNamespaceProofIsSound(t *testing.T) {
	universe := []string{"a", "b", "ab", "a/b", "a/bc", "a/b/c", "a/b/c/d", "b/a"}
	var claims []Claim
	for _, v := range []string{"a", "b", "ab", "a/b", "a/bc", "a/b/c", "a/"} {
		claims = append(claims, Claim{Kind: NameExact, Value: v}, Claim{Kind: NamePrefix, Value: v})
	}
	extent := func(c Claim) []string {
		v := strings.TrimSuffix(c.Value, "/")
		var out []string
		for _, n := range universe {
			if n == v || (c.Kind == NamePrefix && strings.HasPrefix(n, v+"/")) {
				out = append(out, n)
			}
		}
		return out
	}
	for _, u := range CheckDomainSoundness(NamespaceDomain("ns"), claims, extent) {
		t.Errorf("unsound disjointness: %s", u)
	}
}

// TestPathProofIsUseful confirms the conservative proof still admits the
// everyday disjoint cases, so the bias costs precision only where it must.
func TestPathProofIsUseful(t *testing.T) {
	d := PathDomain("path")
	for _, tc := range []struct {
		a, b Claim
		want Relation
	}{
		{Claim{Kind: PathFile, Value: "svc/a.go"}, Claim{Kind: PathFile, Value: "svc/b.go"}, RelationDisjoint},
		{Claim{Kind: PathFile, Value: "svc/a.go"}, Claim{Kind: PathFile, Value: "SVC/A.go"}, RelationOverlap},
		{Claim{Kind: PathDir, Value: "svc"}, Claim{Kind: PathDir, Value: "svcx"}, RelationDisjoint},
		{Claim{Kind: PathDir, Value: "svc"}, Claim{Kind: PathFile, Value: "svc/deep/a.go"}, RelationOverlap},
		{Claim{Kind: PathGlob, Value: "svc/*.go"}, Claim{Kind: PathGlob, Value: "docs/*.md"}, RelationDisjoint},
		{Claim{Kind: PathGlob, Value: "svc/*.go"}, Claim{Kind: PathFile, Value: "svc/a.go"}, RelationOverlap},
		{Claim{Kind: PathGlob, Value: "svc/*.go"}, Claim{Kind: PathFile, Value: "svc/deep/a.go"}, RelationDisjoint},
		{Claim{Kind: PathGlob, Value: "svc/**"}, Claim{Kind: PathDir, Value: "docs"}, RelationDisjoint},
		{Claim{Kind: PathGlob, Value: "svc/**"}, Claim{Kind: PathFile, Value: "svc/deep/a.go"}, RelationOverlap},
		{Claim{Kind: PathDir, Value: "."}, Claim{Kind: PathFile, Value: "anything.txt"}, RelationOverlap},
		{Claim{Kind: PathFile, Value: "svc/a.go"}, Claim{Kind: PathFile, Value: "/svc/a.go"}, RelationUnknown},
		{Claim{Kind: PathFile, Value: "../escape.go"}, Claim{Kind: PathFile, Value: "svc/a.go"}, RelationUnknown},
		// A character class cannot be case-folded without changing what it
		// means, so under the default fold it falls back to the prefix rule.
		{Claim{Kind: PathGlob, Value: "[a-z]*.go"}, Claim{Kind: PathFile, Value: "A.go"}, RelationOverlap},
	} {
		if got := d.Relate(tc.a, tc.b); got != tc.want {
			t.Errorf("Relate(%s, %s) = %s, want %s", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestPathMatcherAddsPrecision confirms a supplied globstar matcher settles a
// pattern against a file that the built-in dialect can only read
// conservatively, without changing what either says elsewhere.
func TestPathMatcherAddsPrecision(t *testing.T) {
	pattern := Claim{Kind: PathGlob, Value: "svc/**/*.go"}
	other := Claim{Kind: PathFile, Value: "svc/deep/notes.md"}

	if got := PathDomain("path").Relate(pattern, other); got != RelationOverlap {
		t.Fatalf("built-in dialect = %s, want the conservative %s", got, RelationOverlap)
	}
	precise := PathDomain("path", WithPathMatcher(globstarMatch))
	if got := precise.Relate(pattern, other); got != RelationDisjoint {
		t.Fatalf("with a globstar matcher = %s, want %s", got, RelationDisjoint)
	}
	if got := precise.Relate(pattern, Claim{Kind: PathFile, Value: "svc/deep/a.go"}); got != RelationOverlap {
		t.Fatalf("matched file = %s, want %s", got, RelationOverlap)
	}
	// A directory is a set of paths, so the matcher is not consulted and the
	// conservative rules still decide.
	if got := precise.Relate(pattern, Claim{Kind: PathDir, Value: "docs"}); got != RelationDisjoint {
		t.Fatalf("pattern against an unrelated directory = %s, want %s", got, RelationDisjoint)
	}
}

// TestSoundnessCheckDiscriminates plants a domain that calls every pair
// disjoint and confirms the falsification hook catches it — a hook that
// reports nothing whatever it is given would make the soundness test above
// meaningless.
func TestSoundnessCheckDiscriminates(t *testing.T) {
	claims := []Claim{{Value: "a"}, {Value: "b"}}
	extent := func(c Claim) []string { return []string{"shared", c.Value} }

	reckless := Domain{Name: "reckless", Relate: func(Claim, Claim) Relation { return RelationDisjoint }}
	bad := CheckDomainSoundness(reckless, claims, extent)
	if len(bad) != 4 {
		t.Fatalf("planted unsound domain produced %d counterexamples, want 4", len(bad))
	}
	if !slices.Contains(bad[0].Shared, "shared") {
		t.Fatalf("counterexample %v does not name the shared resource", bad[0])
	}

	careful := Domain{Name: "careful", Relate: func(Claim, Claim) Relation { return RelationUnknown }}
	if got := CheckDomainSoundness(careful, claims, extent); len(got) != 0 {
		t.Fatalf("domain that proves nothing reported %d counterexamples, want 0", len(got))
	}
}

// pathUniverse enumerates every path up to three levels deep over a small
// name set chosen to make the awkward cases collide: names that prefix one
// another, names that differ only in case, and names with and without an
// extension.
func pathUniverse() []string {
	names := []string{"a", "b", "ab", "a.go", "A", "A.go"}
	var out []string
	for _, x := range names {
		out = append(out, x)
		for _, y := range names {
			out = append(out, x+"/"+y)
			for _, z := range names {
				out = append(out, x+"/"+y+"/"+z)
			}
		}
	}
	return out
}

// pathClaims enumerates the claims the soundness pass is run over: every one-
// and two-segment pattern over a segment alphabet covering literals,
// wildcards, a character class and the segment-crossing "**", a handful of
// three-segment patterns to exercise alignment past a wildcard, and literal
// file and directory claims.
func pathClaims() []Claim {
	segs := []string{"a", "b", "A", "*", "?", "*.go", "a*", "**", "[ab]", "a.go"}
	var out []Claim
	for _, s := range segs {
		out = append(out, Claim{Kind: PathGlob, Value: s})
		for _, t := range segs {
			out = append(out, Claim{Kind: PathGlob, Value: s + "/" + t})
		}
	}
	for _, s := range []string{"a/*/a.go", "a/**/a.go", "*/*/*", "a/b/c", "**/a/*", "a/*/*"} {
		out = append(out, Claim{Kind: PathGlob, Value: s})
	}
	for _, l := range []string{"a", "b", "a/b", "A/b", "a/a.go", "a/b/a.go", "a.go", "ab/a"} {
		out = append(out, Claim{Kind: PathFile, Value: l}, Claim{Kind: PathDir, Value: l})
	}
	return out
}

// pathExtent is the independent oracle behind the soundness pass: which paths
// in the universe a claim really covers, decided without consulting the domain
// under test. Results are memoised because the pass asks for every ordered
// pair.
func pathExtent(universe []string, fold func(string) string) func(Claim) []string {
	memo := map[Claim][]string{}
	return func(c Claim) []string {
		if hit, ok := memo[c]; ok {
			return hit
		}
		var out []string
		for _, p := range universe {
			fp, cv := fold(p), fold(c.Value)
			hit := false
			switch c.Kind {
			case PathFile:
				hit = fp == cv
			case PathDir:
				hit = cv == "." || fp == cv || strings.HasPrefix(fp, cv+"/")
			case PathGlob:
				hit = oracleMatch(strings.Split(cv, "/"), strings.Split(fp, "/"))
			}
			if hit {
				out = append(out, p)
			}
		}
		memo[c] = out
		return out
	}
}

// globstarMatch is oracleMatch in the shape WithPathMatcher takes.
func globstarMatch(pattern, name string) (bool, error) {
	return oracleMatch(strings.Split(pattern, "/"), strings.Split(name, "/")), nil
}

// oracleMatch matches a split pattern against a split path: "**" stands for
// zero or more whole segments and every other segment is matched by
// path.Match. It is written straight from the dialect PathDomain documents,
// deliberately without reusing any of the domain's own reasoning.
func oracleMatch(pattern, name []string) bool {
	switch {
	case len(pattern) == 0:
		return len(name) == 0
	case pattern[0] == "**":
		for i := 0; i <= len(name); i++ {
			if oracleMatch(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	case len(name) == 0:
		return false
	}
	ok, err := path.Match(pattern[0], name[0])
	return err == nil && ok && oracleMatch(pattern[1:], name[1:])
}
