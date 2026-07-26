package agentcontract

// Relation is the declared relationship between one brief and one sibling — the value of
// exactly one discriminator-matrix cell.
type Relation string

const (
	// RelationDiscriminator means the two agents are confusable in principle and Reason states
	// the specific, checkable thing that tells them apart.
	RelationDiscriminator Relation = "discriminator"
	// RelationNotConfusable means no discriminator is needed and Reason states why.
	RelationNotConfusable Relation = "not-confusable"
)

// Discriminator is one cell of the N x N matrix: how this brief's author distinguishes their
// agent from one named sibling.
type Discriminator struct {
	Relation Relation `yaml:"relation"`
	Reason   string   `yaml:"reason"`
	// Fuzzy marks a genuinely overlapping boundary. When true, TieBreak is required.
	Fuzzy bool `yaml:"fuzzy"`
	// TieBreak names the rule that decides which agent handles a request landing in the
	// overlap. Required when Fuzzy is true; ignored otherwise.
	TieBreak string `yaml:"tie_break"`
}

// Decision is one named, single-source-of-truth rule a brief's agent must follow. Body prose
// refers to a decision by name; restating its Statement a second time is the defect this
// contract calls "derived twice."
type Decision struct {
	Name      string `yaml:"name"`
	Statement string `yaml:"statement"`
}

// FailurePath is one named way the agent's task can fail, paired with the action that
// terminates it: stop, fall back to a named alternative, or report and continue.
type FailurePath struct {
	Name   string `yaml:"name"`
	Action string `yaml:"action"`
}

// Contract is the agentcontract-specific extension block on an agent brief's frontmatter,
// alongside the Claude Code subagent keys (name, description, tools, model, effort) this
// package otherwise ignores.
type Contract struct {
	// OutputSchema is a path to the JSON Schema the agent's return validates against, resolved
	// relative to the brief's own directory or the lint's declared schema roots. It must be a
	// path, never a paraphrase of one.
	OutputSchema string `yaml:"output_schema"`
	// EditProposing marks an agent whose output proposes edits to a document, which brings the
	// FB11 requirement into scope.
	EditProposing bool `yaml:"edit_proposing"`
	// LargeArtifact marks an agent that can produce an artifact too large for one dispatch
	// payload, which brings the FB3 requirement into scope.
	LargeArtifact bool `yaml:"large_artifact"`
	// Decisions is every rule this brief derives once. Keyed by Name; Statement is the single
	// canonical wording, referenced elsewhere by name rather than restated.
	Decisions []Decision `yaml:"decisions"`
	// FailurePaths is every named way the agent's task can fail, each carrying its terminating
	// action.
	FailurePaths []FailurePath `yaml:"failure_paths"`
	// Discriminators is this brief's declared cells of the discriminator matrix, keyed by
	// sibling agent name. The set of keys is checked against the roster's actual membership —
	// it is never trusted as the sibling set itself.
	Discriminators map[string]Discriminator `yaml:"discriminators"`
}

// Frontmatter is the full parsed YAML frontmatter block of an agent brief.
type Frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Contract    Contract `yaml:"contract"`
}

// Brief is one parsed agent brief: its frontmatter plus the Markdown body below it.
type Brief struct {
	// Path is the brief's file path, used to resolve OutputSchema and to identify the brief in
	// findings.
	Path string
	// Dir is Path's parent directory — the roster this brief belongs to.
	Dir         string
	Frontmatter Frontmatter
	Body        string
}

// Roster is every brief that lives directly in one "agents" directory — the closed, mechanically
// derived set every member's sibling set is checked against.
type Roster struct {
	Dir    string
	Briefs []*Brief
}

// Siblings returns every roster member other than b, sorted by name. This — never a set b's
// own frontmatter names — is the sibling set completeness is checked against.
func (r *Roster) Siblings(b *Brief) []*Brief {
	out := make([]*Brief, 0, len(r.Briefs)-1)
	for _, other := range r.Briefs {
		if other != b {
			out = append(out, other)
		}
	}
	return out
}
