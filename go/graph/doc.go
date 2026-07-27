// Package graph builds and reasons about directed dependency graphs — trees
// and DAGs alike — and proves when two units of work may safely run in
// parallel.
//
// # Two graphs, one admission rule
//
// Deciding that two units of work can run at the same time needs two
// independent facts about them. The dependency graph answers the first: does
// either one need the other's output? A resource-surface graph answers the
// second: do they touch any of the same resources? Batch composes both — a
// pair may share a batch only when the dependency graph shows them independent
// AND a Prover shows their declared surfaces disjoint. CanCoBatch is that same
// rule for a single pair, exported so any batching decision can be re-derived
// or audited after the fact.
//
// # Nothing about ids is built in
//
// A Graph is keyed by any comparable type, so there is no id grammar to
// outgrow: strings, structs, UUIDs, and paths all work unchanged. IDScheme
// carries the only id knowledge the package needs — how to render an id as
// text, and what shorter form (if any) a reference may use in its place.
// Neither function is required.
//
// # Resource surfaces are pluggable
//
// A Surface is a set of Claims grouped by resource domain, and a Domain is
// whatever a caller says one is: files, directories and globs (PathDomain),
// hierarchical identifier namespaces such as packages, locks, queues or topics
// (NamespaceDomain), or any universe a caller can write a disjointness rule
// for. A Prover holds the registered domains, and two surfaces are disjoint
// only when every domain agrees.
//
// # The disjointness bias, and where it stops
//
// Every proof here is biased one way on purpose: a pair is unsafe unless
// disjointness is proven. An undeclared domain, a domain nobody registered, a
// claim a domain cannot reason about, or any verdict other than
// RelationDisjoint all make the pair unsafe, so a node in any of those states
// runs in a batch of one rather than alongside a neighbour. Failing this way
// costs parallelism; failing the other way destroys work that already ran.
//
// The bias is only as good as the proofs behind it, and its blast radius is
// every batch ever scheduled: a pair wrongly called disjoint runs in parallel,
// produces two clean-looking results, and surfaces as damage at merge time
// rather than as a failure in the batch that caused it. Three things bound
// that risk, and one thing does not:
//
//   - Every verdict fails closed. RelationDisjoint is the only safe answer and
//     it must be stated explicitly; the zero value and every unrecognised
//     value are unsafe, so a Domain that forgets a case loses parallelism
//     instead of correctness.
//   - Every proof is a sufficient condition, never a decision procedure. A
//     rule that cannot settle a pair says so, and the imprecision shows up as
//     a smaller batch.
//   - CheckDomainSoundness turns the bias into something testable: given a
//     finite universe and a ground-truth extent function, it reports every
//     pair a Domain proved disjoint whose extents actually intersect. A domain
//     without such a check is trusted, not verified.
//
// What does not bound it: a surface is a declaration, so a unit that writes
// outside what it declared is invisible to every proof here, as are resource
// identities only the running system knows — symlinks and hard links aliasing
// two path claims, or two units emitting the same new symbol into one package.
// That residual is genuinely undecidable before the work runs, and it belongs
// to a post-merge backstop (re-asserting what each unit actually touched, and
// building the merged result), not to this package.
//
// # Determinism and concurrency
//
// Every ordered result — topological order, layers, cycle paths, batches —
// breaks ties by node insertion order, so identical inputs always produce
// identical output. Nothing is cached between calls: each call recomputes from
// the graph as it stands, so a result can never be stale with respect to a
// mutation. A Graph is safe for concurrent readers but not for concurrent
// mutation; a Domain must be pure, which makes a Prover safe to share.
package graph
