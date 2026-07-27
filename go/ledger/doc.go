// Package ledger is an append-only ranked register: the feedback/findings-backlog pattern
// (defect, risk, finding — any item that needs a criticality-ranked queue and a periodic
// act-now/deferred split). A caller supplies a statement plus impact and urgency scores; Add
// derives the entry's id and criticality (impact x urgency) itself — there is no parameter
// through which a caller could supply either, so "engine-derived, never caller-supplied" is
// enforced by the function signature, not by a runtime check. List applies composable filters
// over the criticality-ranked entries, and Partition splits a slice of entries into act-now and
// deferred by a caller-supplied threshold, total and lossless by construction.
//
// Every successful Add persists the ledger as a canonical JSON document plus its Markdown
// mirror in one atomic pair (docmirror.WritePair over jsondoc's RFC 8785 canonicalization) —
// there is no code path that updates one file without the other.
//
// Resolve, Retract, and Recur close the loop an append-only register otherwise leaves open. An
// entry reaches one of five closed outcomes (Resolution.Known(): closed, fixed-live, carried,
// retracted, stopgap), each requiring a typed Citation — never a prose note — as evidence.
// Retracted is its own outcome, not a flavour of closed: a Retract call also records a
// Retraction (the refuting evidence plus the id of the entry that supersedes it), and a
// retracted entry is excluded from List's default ranking and from Partition's act-now split,
// though never dropped from Rollup's total accounting. Recur increments an entry's recurrence
// counter, idempotently per planning cycle, when it survives to another cycle unconsumed.
package ledger
