package characterize

// SchemaVersion is the capability-manifest schema version this package emits, pinned to
// schemas/plugin-validation/capability-manifest.schema.json's `schema` const.
const SchemaVersion = "plugin-validation.capability-manifest@1.0.0"

// SurfaceType is the kind of surface a plugin contributes, per Claude Code's plugin component
// model -- the manifest schema's closed `surfaces[].type` enum.
type SurfaceType string

const (
	SurfaceCommand     SurfaceType = "command"
	SurfaceAgent       SurfaceType = "agent"
	SurfaceSkill       SurfaceType = "skill"
	SurfaceHook        SurfaceType = "hook"
	SurfaceMCPServer   SurfaceType = "mcp-server"
	SurfaceStatusline  SurfaceType = "statusline"
	SurfaceOutputStyle SurfaceType = "output-style"
	SurfaceOther       SurfaceType = "other"
)

// Severity is a weak spot's closed severity enum.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// Citation is the evidence for one claim: a real file inside the characterized plugin. Every
// surface and every weak spot carries one.
type Citation struct {
	// Path is repo-qualified (first segment names the checkout it resolves in), matching the
	// manifest's plugin.path prefix.
	Path string `json:"path"`
	// Lines is [start, end], 1-indexed and inclusive; nil when the claim is about the whole
	// file.
	Lines []int `json:"lines,omitempty"`
	// Excerpt is a short verbatim quote from the cited lines, when quoting the exact text
	// materially supports the claim.
	Excerpt string `json:"excerpt,omitempty"`
}

// WeakSpot is an analytically-derived concern about a surface: where its trigger is ambiguous,
// narrower or wider than intended, unreachable, or otherwise likely to misfire.
type WeakSpot struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Basis       string   `json:"basis"`
	Citation    Citation `json:"citation"`
	Severity    Severity `json:"severity,omitempty"`
}

// Surface is one capability the plugin contributes: what it is, what invokes it, and the file
// that proves both.
type Surface struct {
	ID        string      `json:"id"`
	Type      SurfaceType `json:"type"`
	Name      string      `json:"name,omitempty"`
	Trigger   string      `json:"trigger"`
	Citation  Citation    `json:"citation"`
	WeakSpots []WeakSpot  `json:"weak_spots,omitempty"`
	CaseIDs   []string    `json:"case_ids,omitempty"`
	Notes     string      `json:"notes,omitempty"`
}

// Gap is an aspect the characterizing read could not establish -- about a candidate surface, a
// trigger, or a suspected weak spot. First-class: named here rather than folded into a surface
// as a guess.
type Gap struct {
	ID                string    `json:"id"`
	Area              string    `json:"area"`
	Reason            string    `json:"reason"`
	AttemptedCitation *Citation `json:"attempted_citation,omitempty"`
}

// Coverage carries the two case-id sets whose difference a downstream consumer computes as
// coverage: manifest-side ids (every surface's case_ids) and executed-side ids (written back by
// a later phase). Both are required and always non-nil, even when empty.
type Coverage struct {
	ManifestCaseIDs []string `json:"manifest_case_ids"`
	ExecutedCaseIDs []string `json:"executed_case_ids"`
}

// PluginIdentity names the plugin a manifest characterizes.
type PluginIdentity struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	Marketplace     string `json:"marketplace,omitempty"`
	Commit          string `json:"commit,omitempty"`
	DeclaredVersion string `json:"declared_version,omitempty"`
}

// Generator names what produced a manifest, when it was agent-generated rather than
// hand-authored.
type Generator struct {
	Tool    string `json:"tool"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Manifest is one capability-manifest document: schemas/plugin-validation's Phase-1 output.
type Manifest struct {
	Schema            string         `json:"schema"`
	Plugin            PluginIdentity `json:"plugin"`
	GeneratedAt       string         `json:"generated_at"`
	Generator         *Generator     `json:"generator,omitempty"`
	Surfaces          []Surface      `json:"surfaces"`
	CouldNotDetermine []Gap          `json:"could_not_determine"`
	Coverage          Coverage       `json:"coverage"`
}
