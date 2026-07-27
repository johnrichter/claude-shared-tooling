// Package retrieve provides level-of-detail projections over a hierarchical
// document: outline (every group and item, id+name only) -> group (one
// group's own identity plus its items at outline granularity) -> item (one
// item's full record) -> field (one named field of one group or item).
//
// A caller declares the document's shape once via a Taxonomy — how to
// enumerate groups, how to enumerate a group's items, and how to read each
// one's id/name — and Retrieve drives every level off those functions plus
// reflection over struct tags for field access. There is no hand-maintained
// switch over field or entity names to drift from the caller's schema.
//
// Every call recomputes its projection from the document passed in; nothing
// is cached or accumulated across calls, so a result is always consistent
// with whatever the caller holds at that moment. Item and field projections
// are deep-copied before they're returned, so mutating a result can never
// reach back into the source document.
package retrieve
