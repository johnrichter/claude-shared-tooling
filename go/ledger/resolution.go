package ledger

import (
	"fmt"
	"strings"
)

// Resolution is one of the closed outcomes a register entry can reach. It is string-backed so
// a foreign value survives round-tripping (and reporting) rather than silently coercing to a
// known member; Known is the only gate through which a resolution is accepted.
//
// Retracted is a distinct outcome from Closed, never a flavour of it: Closed means the
// underlying issue was real and got handled, Retracted means the entry itself never held —
// collapsing the two would lose whichever of those two facts the register actually recorded.
// Stopgap is likewise not closure: it resolves the entry while leaving the underlying seam
// open, tracked forward by the successor work named in its citation.
type Resolution string

// The closed resolution set. Adding or removing a member changes what a caller can assert
// about an entry's fate, so treat it with the same care as any other closed enum here.
const (
	ResolutionClosed    Resolution = "closed"
	ResolutionFixedLive Resolution = "fixed-live"
	ResolutionCarried   Resolution = "carried"
	ResolutionRetracted Resolution = "retracted"
	ResolutionStopgap   Resolution = "stopgap"
)

var knownResolutions = map[Resolution]bool{
	ResolutionClosed:    true,
	ResolutionFixedLive: true,
	ResolutionCarried:   true,
	ResolutionRetracted: true,
	ResolutionStopgap:   true,
}

// Known reports whether r is one of the five canonical resolutions. The zero value "" (an
// entry not yet resolved) is deliberately not Known — unresolved is its own state, not a
// sixth member of the enum.
func (r Resolution) Known() bool {
	return knownResolutions[r]
}

func (r Resolution) String() string { return string(r) }

// CitationKind is the closed set of shapes a citation can take. A prose note is never one of
// them.
type CitationKind string

const (
	CitationPathLine   CitationKind = "path:line"
	CitationReleaseTag CitationKind = "release-tag"
	CitationTaskID     CitationKind = "task-id"
)

var knownCitationKinds = map[CitationKind]bool{
	CitationPathLine:   true,
	CitationReleaseTag: true,
	CitationTaskID:     true,
}

// Known reports whether k is one of the three canonical citation kinds.
func (k CitationKind) Known() bool {
	return knownCitationKinds[k]
}

// Citation is the REQUIRED evidence behind a resolution: a located line, a released tag, or a
// task id — never free prose. The zero value (empty Kind) is not a citation; Validate refuses
// it the same as an unrecognized Kind or a prose Value.
type Citation struct {
	Kind  CitationKind `json:"kind"`
	Value string       `json:"value"`
}

// Validate reports whether c is well-formed for its declared Kind. A path:line, release tag,
// or task id is always a single token, so any Value carrying internal whitespace is refused as
// prose regardless of Kind; path:line additionally requires the ":line" suffix that
// distinguishes a located line from a bare path.
func (c Citation) Validate() error {
	if !c.Kind.Known() {
		return &ValidationError{Field: "citation.kind", Message: fmt.Sprintf("unknown citation kind %q", string(c.Kind))}
	}
	value := strings.TrimSpace(c.Value)
	if value == "" {
		return &ValidationError{Field: "citation.value", Message: "must not be empty"}
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return &ValidationError{Field: "citation.value", Message: "must be a single token (path:line, release tag, or task id), not a prose note"}
	}
	if c.Kind == CitationPathLine {
		path, line, ok := strings.Cut(value, ":")
		if !ok || path == "" || line == "" || !isDigits(line) {
			return &ValidationError{Field: "citation.value", Message: `path:line citation must have the shape "path:line", e.g. "go/ledger/ledger.go:42"`}
		}
	}
	return nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Retraction carries the provenance behind a Retracted resolution: the citation that refutes
// the entry, and the id of the entry that supersedes it. Both are required — there is no bare
// "this was wrong" without saying what showed it and what replaced it.
type Retraction struct {
	RefutingEvidence  Citation `json:"refuting_evidence"`
	SupersededEntryID string   `json:"superseded_entry_id"`
}

// Validate reports whether ret is complete: a well-formed refuting citation plus a non-empty
// superseded entry id.
func (ret Retraction) Validate() error {
	if err := ret.RefutingEvidence.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(ret.SupersededEntryID) == "" {
		return &ValidationError{Field: "retraction.superseded_entry_id", Message: "must not be empty"}
	}
	return nil
}
