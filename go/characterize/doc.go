// Package characterize runs plugin-validation's Phase 1: an agent-driven read of a plugin's own
// files that produces a capability manifest, valid against schemas/plugin-validation's contract
// (see the embedded copy in schema/capability-manifest.schema.json).
//
// The characterizing read is metered -- it spends real model budget -- so every entry point
// resolves its model tier through ResolveModel (never a literal baked into this package's call
// logic) and enforces a caller-declared per-run cost ceiling against the run's real spend, never
// trusting that the ceiling flag handed to the probe was itself honored.
//
// A surface this package cannot verify against a real file inside the plugin is never emitted as
// a surface: its citation is checked against the plugin's own files on disk, and anything that
// does not resolve is folded into the manifest's could_not_determine gaps instead. This is the
// package's one non-negotiable invariant -- everything else (prompt wording, id minting, model
// choice) is free to change without weakening it.
//
// Transcript access, where this package needs it at all, goes through
// transcript.TranscriptSource -- never a hardcoded assumption about the on-disk log format.
package characterize
