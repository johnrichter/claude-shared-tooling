// Package cost turns Claude Code session transcripts into queryable, auditable spend records.
// It reads canonical Claude Code JSONL transcripts through transcript.TranscriptSource — never
// an OpenTelemetry export or a Trajectory file — prices every billable turn against the shared
// model roster (go/roster), and persists the result in a cgo-free SQLite index (modernc.org/sqlite)
// so a caller can roll up and query spend by session, project, agent, tool, and error state.
//
// Every dollar this package reports traces back to a stored per-turn cost event: a turn that
// cannot be priced fails the ingest loudly, and a turn that cannot be attributed to a specific
// agent is flagged unmappable and pooled rather than folded into the orchestrator's total or
// dropped. Identity verifies the resulting partition — total equals the orchestrator's share
// plus every named agent's share plus any caller-supplied fixed cost plus the itemized
// unmappable residual — never absorbing the residual into another bucket to make the numbers
// balance.
//
// Ingest is resumable: a per-transcript watermark tracks the last line already priced, so
// re-running Ingest against a transcript still being appended to picks up where it left off
// without re-pricing or double-counting anything already stored. Every stored cost event keeps
// the exact rate table it was priced against (an immutable snapshot); RateHistory separately
// accumulates every distinct rate this package has ever observed for a model, dated by
// observation, so a later question about what a turn would have cost under an earlier rate can
// still be answered without touching the original snapshot.
package cost
