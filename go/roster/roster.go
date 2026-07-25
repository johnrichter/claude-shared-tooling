// Package roster is the read side of the model roster contract
// (schemas/model-roster/model-roster.schema.json): one row per pinned full Claude model ID,
// embedded into the binary at build time so no consumer ships or hand-maintains a copy of any
// roster field. It exposes exactly the projections a runtime reader needs — Lookup for the
// full row, Compare for capability ordering, EffortAvailable, Selectable, Price, and
// Lifecycle for the single-field projections — and keeps two failure modes structurally
// distinct rather than folding them into one error string:
//
//   - StaleError / SentinelError: the roster is fine, but this query has no answer in it
//     (unknown ID, undeclared cross-family pair, or a dispatch sentinel that legitimately has
//     no row). This is a decision input a caller may act on.
//   - PackagingDefectError: the embedded roster itself failed to load (missing, empty,
//     corrupt, or too new a schema version). This is a build defect, never a decision input —
//     a caller must not treat it as "below floor" or as a silent pass.
//
// model-roster.json in this directory is a mechanical, byte-identical copy of
// schemas/model-roster/model-roster.json (go:embed cannot reach outside its package
// directory); TestEmbeddedCopyMatchesCanonicalSource in this package's test suite guards
// against drift between the two whenever the canonical source is reachable from the module
// checkout.
package roster

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

// builtSchemaVersion is the model-roster schema MAJOR this package was built against
// (model-roster.schema.json's "version" is 1.0.0). A document declaring a higher
// _schema_version is forward-refused as a packaging defect rather than read with guessed
// semantics for fields this build has never seen.
const builtSchemaVersion = 1

//go:embed model-roster.json
var embeddedRosterJSON []byte

var (
	rosterDoc     *document
	rosterLoadErr error
)

func init() {
	rosterDoc, rosterLoadErr = parseRoster(embeddedRosterJSON)
}

// loadedDocument returns the parsed embedded roster, or a PackagingDefectError if the embed
// failed to load. Every exported function routes through this before touching roster data, so
// a load failure never falls through to roster-stale or to a silent pass.
func loadedDocument() (*document, error) {
	if rosterLoadErr != nil {
		return nil, &PackagingDefectError{Err: rosterLoadErr}
	}
	return rosterDoc, nil
}

// parseRoster decodes and validates one roster document. It is the sole place "missing,
// empty, or corrupt" is decided, kept separate from the embed so it can be exercised directly
// against hand-built byte slices without needing a broken build.
func parseRoster(data []byte) (*document, error) {
	if len(data) == 0 {
		return nil, errors.New("embedded roster is empty")
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("embedded roster is not valid JSON: %w", err)
	}
	if doc.SchemaVersion > builtSchemaVersion {
		return nil, fmt.Errorf("embedded roster declares _schema_version %d, newer than the %d this library was built against", doc.SchemaVersion, builtSchemaVersion)
	}
	if len(doc.Models) == 0 {
		return nil, errors.New("embedded roster has no models")
	}
	return &doc, nil
}
