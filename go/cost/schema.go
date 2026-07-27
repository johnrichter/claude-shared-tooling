package cost

// schemaDDL creates every table this package owns if it does not already exist. Open runs this
// once per Store, so opening an existing database is a no-op past the first run.
//
//   - watermarks tracks, per transcript path, the last line number Ingest has already priced —
//     the resumability mechanism: a re-run skips everything at or below it.
//   - rate_history accumulates one row per (model, rate tuple) this package has ever observed
//     at ingest time, dated by observation. It is insert-only: a row already recorded is never
//     updated, so a historical repricing question can still be answered against a rate that has
//     since changed.
//   - cost_events is the immutable per-turn ledger: one row per (transcript_path, line_no),
//     never updated after insert. Every bucket cost and the exact rate snapshot it was priced
//     against are stored on the row itself, so a later rate_history change can never silently
//     alter an already-reported number.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS watermarks (
	transcript_path TEXT PRIMARY KEY,
	last_line       INTEGER NOT NULL,
	updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS rate_history (
	model_id       TEXT NOT NULL,
	as_of          TEXT NOT NULL,
	basis          TEXT NOT NULL,
	input          REAL NOT NULL,
	output         REAL NOT NULL,
	cache_read     REAL NOT NULL,
	cache_write_5m REAL NOT NULL,
	cache_write_1h REAL NOT NULL,
	recorded_at    TEXT NOT NULL,
	PRIMARY KEY (model_id, as_of)
);

CREATE TABLE IF NOT EXISTS cost_events (
	id                    INTEGER PRIMARY KEY AUTOINCREMENT,
	transcript_path       TEXT NOT NULL,
	line_no               INTEGER NOT NULL,
	session_id            TEXT NOT NULL,
	project               TEXT NOT NULL,
	role                  TEXT NOT NULL,
	agent                 TEXT NOT NULL DEFAULT '',
	tool                  TEXT NOT NULL DEFAULT '',
	model_id              TEXT NOT NULL DEFAULT '',
	input_tokens          INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens    INTEGER NOT NULL DEFAULT 0,
	cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
	output_tokens         INTEGER NOT NULL DEFAULT 0,
	input_cost            INTEGER NOT NULL DEFAULT 0,
	cache_write_cost      INTEGER NOT NULL DEFAULT 0,
	cache_read_cost       INTEGER NOT NULL DEFAULT 0,
	output_cost           INTEGER NOT NULL DEFAULT 0,
	total_cost            INTEGER NOT NULL DEFAULT 0,
	price_basis           TEXT NOT NULL DEFAULT '',
	price_as_of           TEXT NOT NULL DEFAULT '',
	errored               INTEGER NOT NULL DEFAULT 0,
	error_reason          TEXT NOT NULL DEFAULT '',
	ingested_at           TEXT NOT NULL,
	UNIQUE (transcript_path, line_no)
);

CREATE INDEX IF NOT EXISTS idx_cost_events_session ON cost_events (session_id);
CREATE INDEX IF NOT EXISTS idx_cost_events_project ON cost_events (project);
CREATE INDEX IF NOT EXISTS idx_cost_events_role_agent ON cost_events (role, agent);
CREATE INDEX IF NOT EXISTS idx_cost_events_tool ON cost_events (tool);
CREATE INDEX IF NOT EXISTS idx_cost_events_errored ON cost_events (errored);
`
