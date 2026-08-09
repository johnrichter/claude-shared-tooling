package graph

import (
	"errors"
	"fmt"
	"slices"
)

// Relation is how two resource extents stand to one another. Only
// RelationDisjoint is safe to act on: the zero value and any value a Domain
// invents are treated exactly like RelationUnknown, so a rule that forgets a
// case costs parallelism rather than correctness.
type Relation string

const (
	// RelationUnknown means disjointness could neither be proven nor
	// disproven.
	RelationUnknown Relation = "unknown"
	// RelationOverlap means the two extents share, or may share, a resource.
	RelationOverlap Relation = "overlap"
	// RelationDisjoint means the two extents provably share no resource.
	RelationDisjoint Relation = "disjoint"
)

// Valid reports whether r is one of the three defined relations. The set is
// closed, so a caller validating input from outside the process can check it
// exhaustively without restating the list.
func (r Relation) Valid() bool {
	switch r {
	case RelationUnknown, RelationOverlap, RelationDisjoint:
		return true
	}
	return false
}

// Ground names why a disjointness or admission decision came out the way it
// did. It is a closed vocabulary so callers can count and alert on grounds
// instead of matching on prose.
type Ground string

const (
	// GroundProved: every claim pair in every registered domain proved
	// disjoint.
	GroundProved Ground = "proved-disjoint"
	// GroundClaimOverlap: a domain found a claim pair that shares, or may
	// share, a resource.
	GroundClaimOverlap Ground = "claim-overlap"
	// GroundUndecidable: a domain could not settle a claim pair either way.
	GroundUndecidable Ground = "undecidable"
	// GroundUndeclared: a surface declares nothing for a registered domain,
	// so what it touches there is unknown.
	GroundUndeclared Ground = "undeclared-domain"
	// GroundUnregistered: a surface declares claims in a domain the prover
	// does not hold, so nothing can reason about them.
	GroundUnregistered Ground = "unregistered-domain"
	// GroundDependencyLinked: one node is in the other's transitive
	// dependency closure, so no surface comparison was made.
	GroundDependencyLinked Ground = "dependency-linked"
	// GroundUnknownNode: the graph does not hold one of the nodes, so nothing
	// is known about what it needs or touches.
	GroundUnknownNode Ground = "unknown-node"
	// GroundGroupFull: the batch already holds the configured maximum number
	// of members.
	GroundGroupFull Ground = "group-full"
)

// Valid reports whether gr is one of the defined grounds.
func (gr Ground) Valid() bool {
	switch gr {
	case GroundProved, GroundClaimOverlap, GroundUndecidable, GroundUndeclared,
		GroundUnregistered, GroundDependencyLinked, GroundUnknownNode, GroundGroupFull:
		return true
	}
	return false
}

// Claim is one declared resource extent within a single domain. Kind and Value
// carry no meaning here — the owning Domain interprets both — which is what
// lets one Surface type describe files, directories, globs, package paths,
// lock names, queue topics, or anything else a caller can write a rule for.
// All three fields are plain strings so a surface survives a round trip
// through the JSON a plan is usually stored in.
//
// Root is optional and, like Kind and Value, means whatever the owning Domain
// decides: a domain that understands it can use it to scope a claim to one
// tree among several — a repository, a worktree, a tenant — so that two
// claims naming the same value in different roots are never confused for one
// another. Its zero value is the claim's own domain's ordinary behavior:
// every claim is implicitly in the same, single root.
type Claim struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value"`
	Root  string `json:"root,omitempty"`
}

// String renders a claim as "kind:value", or just the value when the domain
// uses a single kind.
func (c Claim) String() string {
	if c.Kind == "" {
		return c.Value
	}
	return c.Kind + ":" + c.Value
}

// Surface is everything one node declares it will touch, keyed by resource
// domain. Key presence is load-bearing and is the difference between the two
// ways a surface can be empty: a domain mapped to an empty claim list is an
// explicit "this node touches nothing in that universe", while a domain the
// map never mentions is undeclared, and an undeclared domain is never provably
// disjoint from anything. A nil Surface therefore declares nothing at all and
// can only ever batch alone.
type Surface map[string][]Claim

// Domain decides disjointness inside one universe of resources — one half of
// the pluggable resource-surface abstraction, the other half being the Claim
// grammar the domain gives meaning to.
//
// Relate must be pure and must return RelationDisjoint only when the two
// claims provably share no resource. Every other outcome, including a claim
// whose Kind or Value the domain does not recognise, must be RelationOverlap
// or RelationUnknown. A domain is free to be imprecise — it costs parallelism
// — but it must never be unsound, and CheckDomainSoundness exists to hold it
// to that.
type Domain struct {
	Name   string
	Relate func(a, b Claim) Relation
}

// Verdict is a surface comparison's outcome together with the ground it rests
// on and, where one decided the matter, the two claims that did. It is a
// record, not a cache: Prover.Relate is pure, so the verdict for any pair can
// be re-derived at any time from the same inputs.
type Verdict struct {
	Relation Relation `json:"relation"`
	Ground   Ground   `json:"ground"`
	Domain   string   `json:"domain,omitempty"`
	A        Claim    `json:"a,omitzero"`
	B        Claim    `json:"b,omitzero"`
}

// Disjoint reports whether the verdict is safe to batch on, which is true only
// for RelationDisjoint.
func (v Verdict) Disjoint() bool { return v.Relation == RelationDisjoint }

// String renders a verdict as "relation (ground)", naming the domain and the
// deciding claim pair when there is one.
func (v Verdict) String() string {
	s := fmt.Sprintf("%s (%s)", v.Relation, v.Ground)
	if v.Domain != "" {
		s += " in domain " + v.Domain
	}
	if v.A.Value != "" || v.B.Value != "" {
		s += fmt.Sprintf(": %s vs %s", v.A, v.B)
	}
	return s
}

// ErrNoDomains reports a Prover constructed without any resource domain, which
// could only ever declare every pair unsafe.
var ErrNoDomains = errors.New("graph: prover needs at least one resource domain")

// Prover holds the registered resource domains and decides whether two
// surfaces are disjoint. The registered set is fixed at construction and is
// the definition of "everything that matters" for the batches it will judge:
// a domain nobody registered is a universe nobody is checking, so a surface
// that claims resources in one is unsafe rather than ignored.
//
// A Prover is immutable and its domains must be pure, so one Prover is safe to
// share across goroutines.
type Prover struct {
	domains []Domain
	byName  map[string]Domain
}

// NewProver registers the domains a batch will be judged against. Every domain
// needs a non-empty, unique name and a non-nil Relate; the order given is the
// order domains are consulted, and therefore the order a blocking domain is
// reported in.
func NewProver(domains ...Domain) (*Prover, error) {
	if len(domains) == 0 {
		return nil, ErrNoDomains
	}
	p := &Prover{domains: slices.Clone(domains), byName: make(map[string]Domain, len(domains))}
	for _, d := range domains {
		switch {
		case d.Name == "":
			return nil, errors.New("graph: resource domain has no name")
		case d.Relate == nil:
			return nil, fmt.Errorf("graph: resource domain %q has no Relate function", d.Name)
		}
		if _, dup := p.byName[d.Name]; dup {
			return nil, fmt.Errorf("graph: resource domain %q registered twice", d.Name)
		}
		p.byName[d.Name] = d
	}
	return p, nil
}

// Domains returns the registered domain names in the order they are consulted.
func (p *Prover) Domains() []string {
	out := make([]string, len(p.domains))
	for i, d := range p.domains {
		out[i] = d.Name
	}
	return out
}

// Relate proves, or fails to prove, that two surfaces touch no common
// resource. It returns on the first thing that makes the pair unsafe, so the
// verdict names the first obstacle rather than all of them:
//
//   - a domain either surface claims but the prover does not hold, since
//     nothing here can reason about it;
//   - a registered domain either surface leaves undeclared, since silence is
//     not the same as touching nothing;
//   - a claim pair a domain calls overlapping, or cannot settle.
//
// Only when every cross pair in every registered domain proves disjoint is the
// result RelationDisjoint. Domains are consulted in registration order and
// claims in declaration order, so the same inputs always yield the same
// verdict.
func (p *Prover) Relate(a, b Surface) Verdict {
	if unknown := unregisteredDomains(p.byName, a, b); len(unknown) > 0 {
		return Verdict{Relation: RelationUnknown, Ground: GroundUnregistered, Domain: unknown[0]}
	}
	for _, d := range p.domains {
		claimsA, okA := a[d.Name]
		claimsB, okB := b[d.Name]
		if !okA || !okB {
			return Verdict{Relation: RelationUnknown, Ground: GroundUndeclared, Domain: d.Name}
		}
		for _, x := range claimsA {
			for _, y := range claimsB {
				switch d.Relate(x, y) {
				case RelationDisjoint:
					continue
				case RelationOverlap:
					return Verdict{Relation: RelationOverlap, Ground: GroundClaimOverlap, Domain: d.Name, A: x, B: y}
				default:
					return Verdict{Relation: RelationUnknown, Ground: GroundUndecidable, Domain: d.Name, A: x, B: y}
				}
			}
		}
	}
	return Verdict{Relation: RelationDisjoint, Ground: GroundProved}
}

// unregisteredDomains returns, in sorted order, every domain either surface
// mentions that the prover does not hold.
func unregisteredDomains(known map[string]Domain, surfaces ...Surface) []string {
	var out []string
	for _, s := range surfaces {
		for name := range s {
			if _, ok := known[name]; !ok && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Unsoundness is one counterexample to a domain's disjointness rule: a claim
// pair the domain proved disjoint whose extents nevertheless share resources.
type Unsoundness struct {
	A      Claim    `json:"a"`
	B      Claim    `json:"b"`
	Shared []string `json:"shared"`
}

// String renders a counterexample as the claim pair and the resources they
// both reach.
func (u Unsoundness) String() string {
	return fmt.Sprintf("%s and %s both reach %v", u.A, u.B, u.Shared)
}

// CheckDomainSoundness looks for counterexamples to a domain's disjointness
// rule over a universe the caller can enumerate. It asks the domain about
// every ordered pair of claims — both orientations, so an asymmetric rule is
// caught too — and wherever the domain answered RelationDisjoint it compares
// the resources extent actually returns for each side; anything in both is a
// counterexample. Pairs the domain called overlapping or unknown are not
// checked, because imprecision is allowed and unsoundness is not.
//
// This is the falsification hook for the whole unsafe-unless-provably-disjoint
// bias. A wrong disjointness rule is otherwise silent — it produces two
// clean-looking parallel results and only shows up as damage at merge time —
// so a domain should ship with a fixture universe it is checked against.
// Results follow the order of claims, which makes that a stable test.
func CheckDomainSoundness(d Domain, claims []Claim, extent func(Claim) []string) []Unsoundness {
	if d.Relate == nil || extent == nil {
		return nil
	}
	var out []Unsoundness
	for _, a := range claims {
		for _, b := range claims {
			if d.Relate(a, b) != RelationDisjoint {
				continue
			}
			if shared := intersect(extent(a), extent(b)); len(shared) > 0 {
				out = append(out, Unsoundness{A: a, B: b, Shared: shared})
			}
		}
	}
	return out
}

// intersect returns the sorted, de-duplicated resources present in both sets.
func intersect(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, x := range b {
		inB[x] = true
	}
	var out []string
	for _, x := range a {
		if inB[x] && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}
