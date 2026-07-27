// Package state is the shared crash-safe run-state store: atomic JSON read/write with
// safe degradation on a corrupt or missing file, a `_schema_version` + migration chain
// so an old file upgrades in place, telemetry counters, a cross-consumer source registry
// for dedup, and a task writer that refuses ever to persist a done task with no commit.
package state
