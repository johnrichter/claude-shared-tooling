package cost

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver — pure Go, no cgo.
)

// Store is one open cost index backed by a cgo-free SQLite database (modernc.org/sqlite). It is
// safe for concurrent use — every method routes through database/sql's own connection pool and
// each Ingest call runs inside one transaction.
type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the SQLite database at path and ensures its schema exists.
//
// The busy_timeout and immediate-transaction pragmas are passed in the DSN, not run as a one-off
// PRAGMA after opening: database/sql hands each method whichever pooled connection is free, so a
// pragma set on one connection would not bind the others. busy_timeout makes a writer wait for a
// held lock instead of failing SQLITE_BUSY immediately; _txlock=immediate takes the write lock at
// BEGIN so two concurrent Ingest transactions (each a rate-history read then a write) serialize
// cleanly instead of deadlocking on a shared-to-reserved lock upgrade.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cost: open store %s: %w", path, err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cost: migrate store %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// nowUTC is the single timestamp source Ingest uses, so every row a call writes shares one
// instant rather than drifting across a long transcript's worth of inserts.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// IngestSummary reports what one Ingest call did, for a caller to log or assert on.
type IngestSummary struct {
	// ResumedFromLine is the watermark Ingest found for this transcript before this call —
	// 0 for a transcript never ingested before.
	ResumedFromLine int
	// EventsIngested is the count of newly priced, newly stored cost events.
	EventsIngested int
	// UnmappableEvents is the subset of EventsIngested flagged RoleUnmappable.
	UnmappableEvents int
	// ErrorsFlagged is the count of newly stored zero-cost events for a malformed transcript
	// line (transcript.Turn.Malformed).
	ErrorsFlagged int
	// TurnsSkippedNoUsage is the count of turns with neither Usage nor Malformed set (a
	// non-assistant line, or an assistant line with nothing billable) — nothing to ledger.
	TurnsSkippedNoUsage int
}

// Ingest reads path via source, prices every new billable turn against the roster rate card,
// and persists the result. It is resumable: a turn at or below the transcript's stored watermark
// is never re-read from source.Turns' callback effects (it is skipped before pricing or storage),
// so re-running Ingest against a transcript still being appended to never re-prices or double-
// counts anything already stored. All of one call's inserts and its watermark advance commit
// together — a failure partway through leaves the store exactly as it was before the call.
//
// A turn whose model roster.Price cannot resolve fails the whole call loudly (no event for it or
// anything after it in this call is stored, and the watermark does not advance past it) rather
// than pricing it at zero or a guessed rate — an unpriceable model is a roster gap to fix, not a
// cost-accounting decision this package makes silently. meta.Project must be non-empty.
func (s *Store) Ingest(source transcript.TranscriptSource, path string, meta TranscriptMeta) (IngestSummary, error) {
	if meta.Project == "" {
		return IngestSummary{}, errors.New("cost: Ingest: TranscriptMeta.Project is required")
	}

	watermark, err := s.readWatermark(path)
	if err != nil {
		return IngestSummary{}, err
	}
	tools, err := toolNamesByLine(path)
	if err != nil {
		return IngestSummary{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("cost: open transcript %s: %w", path, err)
	}
	defer f.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return IngestSummary{}, fmt.Errorf("cost: begin ingest transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	var summary IngestSummary
	summary.ResumedFromLine = watermark
	maxLine := watermark
	ingestedAt := nowUTC()
	ingestAsOf := ingestedAt[:len("2006-01-02")]
	seenRates := map[string]bool{}

	walkErr := source.Turns(f, func(t transcript.Turn) error {
		if t.LineNo <= watermark {
			return nil
		}
		maxLine = t.LineNo

		switch {
		case t.Malformed:
			role, agent := resolveRole(meta, t.Authorship)
			if err := insertEvent(tx, path, t.LineNo, t.SessionID, meta.Project, role, agent, "", "", PriceSnapshot{}, "", transcript.Usage{}, BucketAmounts{}, true, t.Flag, ingestedAt); err != nil {
				return err
			}
			summary.ErrorsFlagged++
			return nil

		case t.Usage == nil:
			summary.TurnsSkippedNoUsage++
			return nil

		default:
			snap, err := resolvePrice(t.Model)
			if err != nil {
				return err
			}
			if !seenRates[snap.ModelID] {
				if err := recordRateHistory(tx, snap, ingestAsOf, ingestedAt); err != nil {
					return err
				}
				seenRates[snap.ModelID] = true
			}
			role, agent := resolveRole(meta, t.Authorship)
			amounts := priceUsage(*t.Usage, snap)
			if err := insertEvent(tx, path, t.LineNo, t.SessionID, meta.Project, role, agent, snap.ModelID, tools[t.LineNo], snap, ingestAsOf, *t.Usage, amounts, false, "", ingestedAt); err != nil {
				return err
			}
			summary.EventsIngested++
			if role == RoleUnmappable {
				summary.UnmappableEvents++
			}
			return nil
		}
	})
	if walkErr != nil {
		return IngestSummary{}, fmt.Errorf("cost: ingest %s: %w", path, walkErr)
	}

	if maxLine > watermark {
		if _, err := tx.Exec(
			`INSERT INTO watermarks (transcript_path, last_line, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (transcript_path) DO UPDATE SET last_line = excluded.last_line, updated_at = excluded.updated_at`,
			path, maxLine, ingestedAt,
		); err != nil {
			return IngestSummary{}, fmt.Errorf("cost: advance watermark for %s: %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return IngestSummary{}, fmt.Errorf("cost: commit ingest of %s: %w", path, err)
	}
	return summary, nil
}

func (s *Store) readWatermark(path string) (int, error) {
	var last int
	err := s.db.QueryRow(`SELECT last_line FROM watermarks WHERE transcript_path = ?`, path).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cost: read watermark for %s: %w", path, err)
	}
	return last, nil
}

// recordRateHistory inserts a dated rate_history row for snap if this exact rate has never been
// recorded for this model before. Insert-only: a row already present is left untouched even if
// this observation is later in time, so the row's as_of date stays the date the rate was first
// observed at, and every historical rate a model has ever carried remains queryable.
func recordRateHistory(tx *sql.Tx, snap PriceSnapshot, asOf, observedAt string) error {
	var exists int
	err := tx.QueryRow(
		`SELECT 1 FROM rate_history
		 WHERE model_id = ? AND basis = ? AND input = ? AND output = ? AND cache_read = ?
		   AND cache_write_5m = ? AND cache_write_1h = ?`,
		snap.ModelID, snap.Basis, snap.Input, snap.Output, snap.CacheRead, snap.CacheWrite5m, snap.CacheWrite1h,
	).Scan(&exists)
	if err == nil {
		return nil // this exact rate is already recorded, under whatever date it was first seen.
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cost: check rate history for %s: %w", snap.ModelID, err)
	}
	_, err = tx.Exec(
		`INSERT INTO rate_history (model_id, as_of, basis, input, output, cache_read, cache_write_5m, cache_write_1h, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (model_id, as_of) DO NOTHING`,
		snap.ModelID, asOf, snap.Basis, snap.Input, snap.Output, snap.CacheRead, snap.CacheWrite5m, snap.CacheWrite1h, observedAt,
	)
	if err != nil {
		return fmt.Errorf("cost: record rate history for %s: %w", snap.ModelID, err)
	}
	return nil
}

func insertEvent(
	tx *sql.Tx, path string, lineNo int, sessionID, project string, role Role, agent, modelID, tool string,
	snap PriceSnapshot, priceAsOf string, usage transcript.Usage, amounts BucketAmounts, errored bool, errorReason, ingestedAt string,
) error {
	_, err := tx.Exec(
		`INSERT INTO cost_events (
			transcript_path, line_no, session_id, project, role, agent, tool, model_id,
			input_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens, cache_read_tokens, output_tokens,
			input_cost, cache_write_cost, cache_read_cost, output_cost, total_cost,
			price_basis, price_as_of, errored, error_reason, ingested_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		path, lineNo, sessionID, project, string(role), agent, tool, modelID,
		usage.InputTokens, usage.CacheCreationTokens, usage.CacheCreationEphemeral5m, usage.CacheCreationEphemeral1h, usage.CacheReadTokens, usage.OutputTokens,
		int64(amounts.Input), int64(amounts.CacheWrite), int64(amounts.CacheRead), int64(amounts.Output), int64(amounts.Total()),
		snap.Basis, priceAsOf, boolToInt(errored), errorReason, ingestedAt,
	)
	if err != nil {
		return fmt.Errorf("cost: insert event %s:%d: %w", path, lineNo, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
