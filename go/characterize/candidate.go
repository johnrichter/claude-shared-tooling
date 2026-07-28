package characterize

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// candidateManifest is the JSON shape a characterizing probe is asked to reply with: a raw set
// of claims about the plugin it read, before this package mints ids, resolves citations against
// the plugin's real files, or computes coverage. Nothing here is trusted until sanitize checks
// it -- this is the hypothesis set, not the manifest.
type candidateManifest struct {
	Surfaces          []candidateSurface `json:"surfaces"`
	CouldNotDetermine []candidateGap     `json:"could_not_determine"`
}

type candidateSurface struct {
	Type      SurfaceType         `json:"type"`
	Name      string              `json:"name,omitempty"`
	Trigger   string              `json:"trigger"`
	Citation  Citation            `json:"citation"`
	WeakSpots []candidateWeakSpot `json:"weak_spots,omitempty"`
	Notes     string              `json:"notes,omitempty"`
}

type candidateWeakSpot struct {
	Description string   `json:"description"`
	Basis       string   `json:"basis"`
	Citation    Citation `json:"citation"`
	Severity    Severity `json:"severity,omitempty"`
}

type candidateGap struct {
	Area              string    `json:"area"`
	Reason            string    `json:"reason"`
	AttemptedCitation *Citation `json:"attempted_citation,omitempty"`
}

// parseCandidate decodes a probe's reply text as a candidateManifest. Extra fields the model
// added are ignored rather than rejected; a genuinely malformed reply (not JSON, or a JSON value
// that isn't the expected object) is the only thing this returns an error for.
func parseCandidate(replyText string) (candidateManifest, error) {
	var c candidateManifest
	if err := json.Unmarshal([]byte(stripCodeFence(replyText)), &c); err != nil {
		return candidateManifest{}, fmt.Errorf("reply did not parse as the expected JSON object: %w", err)
	}
	return c, nil
}

// Schema-derived minimums this package enforces before accepting a candidate claim, mirroring
// schemas/plugin-validation/capability-manifest.schema.json's own $defs minLength values
// (surface.trigger, weakSpot.description/basis, gap.area/reason). Kept as constants, not read
// from the schema at validation time, because a candidate must be accepted or redirected to
// could_not_determine BEFORE the final document is built and validated; if the schema's own
// minimums ever change, these must move with them.
const (
	minTriggerLen       = 10
	minWeakSpotProseLen = 10
	minGapAreaLen       = 5
	minGapReasonLen     = 10
)

// validSurfaceType reports whether t is one of the manifest schema's closed surface-type enum
// values -- a candidate naming anything else names something this package does not recognize as
// a Claude Code plugin surface kind, and is treated as unresolvable rather than passed through.
func validSurfaceType(t SurfaceType) bool {
	switch t {
	case SurfaceCommand, SurfaceAgent, SurfaceSkill, SurfaceHook, SurfaceMCPServer, SurfaceStatusline, SurfaceOutputStyle, SurfaceOther:
		return true
	default:
		return false
	}
}

// citationPathPattern mirrors the manifest schema's $defs/path.pattern -- a repo-qualified path
// (first segment names the checkout it resolves in, at least one more path segment after it),
// with no embedded whitespace. Used only to decide whether a citation this package could NOT
// resolve to a real file is at least well-formed enough to keep as a could-not-determine gap's
// attempted_citation; a citation failing this shape is dropped from the gap rather than emitted
// as an attempted_citation that would itself fail the manifest's own schema.
var citationPathPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[^\s]+)+$`)

// schemaShapedCitation reports whether c is structurally valid against the citation schema
// (path pattern, lines shape) -- never whether it resolves to a real file; that check is
// resolveCitation's job. A nil c is considered shaped (there is nothing to reject).
func schemaShapedCitation(c *Citation) bool {
	if c == nil {
		return true
	}
	if !citationPathPattern.MatchString(c.Path) {
		return false
	}
	if c.Lines == nil {
		return true
	}
	return len(c.Lines) == 2 && c.Lines[0] >= 1 && c.Lines[1] >= c.Lines[0]
}

// attemptedCitation returns a pointer to c for use as a gap's attempted_citation, or nil when c
// is the zero value (no citation was attempted) or is not schema-shaped -- a malformed attempt
// is reported by the gap's Reason text alone, never as a citation that would fail validation.
func attemptedCitation(c Citation) *Citation {
	if c.Path == "" || !schemaShapedCitation(&c) {
		return nil
	}
	return &c
}
