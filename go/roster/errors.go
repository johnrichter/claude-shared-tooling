package roster

import "fmt"

// StaleError is the roster-stale verdict: the roster loaded fine but has no data to answer
// this query — an ID with no row, or a cross-family pair with no declared cross_family_rank on
// both sides. It is a distinct outcome from "known and below floor" or "known and selectable":
// collapsing it into either mistakes "the roster doesn't know" for "the roster knows and says
// no."
type StaleError struct {
	// Query names what was asked: the model ID for a lookup, or "id-a vs id-b" for a
	// cross-family comparison with no declared rank.
	Query string
	// Reason is the specific condition (unknown ID, no declared rank, no sourced price).
	Reason string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf(
		"roster-stale: %s (query: %s) — refresh model-roster.json (see schemas/model-roster/README.md, \"Refreshing it\") to resolve this",
		e.Reason, e.Query,
	)
}

// SentinelError reports that an id is a declared dispatch sentinel (model-roster.json's
// effort_exempt_sentinels, e.g. "inherit") rather than a model. Sentinels never get a roster
// row by design, so this is neither roster-stale (there is nothing to refresh) nor a resolved
// Model.
type SentinelError struct {
	ID string
}

func (e *SentinelError) Error() string {
	return fmt.Sprintf("roster: %q is a dispatch sentinel, not a model", e.ID)
}

// PackagingDefectError wraps a failure to load the embedded roster itself: missing, empty, or
// corrupt data, or a document declaring a schema version newer than this library understands.
// This is a build defect, never a runtime verdict — a caller MUST NOT treat it as roster-stale
// or as a silent pass in either direction.
type PackagingDefectError struct {
	Err error
}

func (e *PackagingDefectError) Error() string {
	return fmt.Sprintf("roster: packaging defect: %v", e.Err)
}

func (e *PackagingDefectError) Unwrap() error {
	return e.Err
}
