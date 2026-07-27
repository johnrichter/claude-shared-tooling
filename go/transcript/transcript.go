package transcript

import "io"

// TranscriptSource is the one seam a consumer depends on for session-log access. Every
// operation a consumer needs — locating a session's transcript, streaming its turns, and
// finding the transcripts its subagents left behind — is a method here, so swapping the
// underlying log format (e.g. a future Claude Code release, or a different coding tool
// entirely) is a matter of handing the consumer a different implementation, never rewriting
// the consumer.
type TranscriptSource interface {
	// ResolvePath computes the deterministic transcript path for a session, given the
	// format's storage root and a scope key (e.g. a workspace/cwd slug) and session id.
	// It is pure string composition: no directory listing, no mtime comparison, no I/O — a
	// concurrently-written sibling transcript can never be picked by mistake because none is
	// ever looked at. Existence is the caller's concern.
	ResolvePath(root, scope, sessionID string) string

	// Turns streams every turn found in r, in file order, invoking fn once per non-blank
	// line. A line that fails to parse produces a Turn with Malformed set (Flag carries the
	// reason) instead of stopping the scan — a malformed or truncated line is reported, never
	// silently dropped and never a crash. Iteration stops early, and that error is returned,
	// only when fn itself returns a non-nil error; a read error from r is returned once
	// encountered, but a truncated final line with no trailing newline is parsed like any
	// other line before that happens.
	Turns(r io.Reader, fn func(Turn) error) error

	// DiscoverSubagentTranscripts returns every subagent transcript spawned from the main
	// transcript at path, at any nesting depth, sorted for a stable order. A format with no
	// subagent concept returns (nil, nil). A missing/non-existent subagent location is not an
	// error — it is the valid "no fan-out yet" state.
	DiscoverSubagentTranscripts(path string) ([]string, error)
}

// Authorship classifies who authored a transcript turn: the orchestrating session itself, or
// a subagent it spawned. AuthorUnknown is the resolution for any line carrying no authorship
// marker at all — deliberately distinct from AuthorOrchestrator. A source must never fold
// "no marker" into "orchestrator-authored": a future transcript format that inlines subagent
// turns into the same stream a consumer already reads must either carry its own authorship
// marker on those turns or leave them AuthorUnknown, so a consumer that requires
// orchestrator-authored lines cannot be silently handed subagent lines it never asked for by a
// mere format upgrade.
type Authorship string

const (
	// AuthorUnknown is the resolution when a turn carries no authorship marker.
	AuthorUnknown Authorship = ""
	// AuthorOrchestrator marks a turn the orchestrating session authored directly.
	AuthorOrchestrator Authorship = "orchestrator"
	// AuthorSubagent marks a turn authored by a spawned subagent (a sidechain turn).
	AuthorSubagent Authorship = "subagent"
)

// Usage is one turn's token accounting, in the four classes a coding-agent transcript reports.
// CacheCreationEphemeral5m/1h are the TTL-split breakdown of CacheCreationTokens when the
// source transcript carries it; a source that only has the flat total leaves both zero and
// CacheCreationTokens is still the caller's authoritative total for that turn.
type Usage struct {
	InputTokens              int64
	CacheCreationTokens      int64
	CacheCreationEphemeral5m int64
	CacheCreationEphemeral1h int64
	CacheReadTokens          int64
	OutputTokens             int64
}

// Turn is one parsed transcript line. Model/SessionID/Usage are the zero value when the line
// did not carry them (e.g. a non-assistant turn has no Usage). Malformed lines carry only
// LineNo, Malformed and Flag — every other field is the zero value, never a guessed one.
type Turn struct {
	LineNo     int
	Type       string
	Role       string
	Model      string
	SessionID  string
	Usage      *Usage
	Authorship Authorship
	Malformed  bool
	Flag       string
}
