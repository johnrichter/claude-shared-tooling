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
package ledger
