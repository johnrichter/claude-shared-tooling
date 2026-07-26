// Package transcript reads AI coding-agent session logs behind a format-agnostic interface.
// A session log's on-disk shape is owned entirely by the coding tool that wrote it; this
// package treats that shape as a swappable adapter (TranscriptSource) rather than a fact a
// consumer hard-codes, so a session-log format change is a new implementation, never a rewrite
// of every caller.
//
// ClaudeCodeJSONL is the current implementation: it stream-parses Claude Code's JSONL session
// transcripts, resolves a session's transcript path deterministically (no directory scan, no
// mtime comparison), and discovers the subagent transcripts a session spawned. A malformed or
// truncated line is reported through the same turn stream with Turn.Malformed set — never a
// crash and never a silently dropped line — so a consumer decides how to react.
package transcript
