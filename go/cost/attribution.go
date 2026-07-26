package cost

import "github.com/johnrichter/claude-shared-tooling/go/transcript"

// Role classifies whose spend a cost event counts toward: the additive-identity partition
// Identity checks is built directly from this classification.
type Role string

const (
	// RoleOrchestrator is a turn authored by the session's own orchestrating agent.
	RoleOrchestrator Role = "orchestrator"
	// RoleAgent is a turn authored by a specific, named spawned agent (TranscriptMeta.Agent).
	RoleAgent Role = "agent"
	// RoleUnmappable is a turn that cannot be attributed to the orchestrator or a specific
	// named agent. It is never folded into RoleOrchestrator or dropped — Identity pools it
	// into an itemized residual instead.
	RoleUnmappable Role = "unmappable"
)

// TranscriptMeta identifies which transcript file Ingest is reading: the caller's own fact from
// discovering the file (via transcript.TranscriptSource.ResolvePath / DiscoverSubagentTranscripts),
// never a guess this package makes from file content.
type TranscriptMeta struct {
	// Project labels which project/workspace this transcript belongs to. Required.
	Project string
	// IsMain is true for the session's own orchestrator transcript, false for a discovered
	// subagent transcript.
	IsMain bool
	// Agent names the subagent that authored this transcript, when known. Ignored when IsMain
	// is true. Leaving it empty for a non-main transcript marks the whole transcript
	// unmappable — every billable turn in it pools into the residual rather than guessing an
	// identity for it.
	Agent string
}

// resolveRole assigns a turn's Role from which transcript file is being ingested (meta), cross-
// checked against the turn's own Authorship marker. meta is a file-level fact the caller already
// knows, so the common case (a plain orchestrator turn, or a turn in a file already known to
// belong to a named agent) never depends on Authorship at all. The one case Authorship overrides
// is a subagent-authored turn found inlined in a transcript identified as the main one: a nested
// sidechain with no separate file or name, which cannot be attributed to a specific agent.
func resolveRole(meta TranscriptMeta, a transcript.Authorship) (role Role, agent string) {
	if meta.IsMain {
		if a == transcript.AuthorSubagent {
			return RoleUnmappable, ""
		}
		return RoleOrchestrator, ""
	}
	if meta.Agent == "" {
		return RoleUnmappable, ""
	}
	return RoleAgent, meta.Agent
}
