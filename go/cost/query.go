package cost

import (
	"fmt"
	"strings"
)

// TokenCounts is one cost event's raw token counts, preserved alongside its priced amounts so a
// caller can re-derive cost under a different rate (see Reprice) without re-reading transcripts.
type TokenCounts struct {
	Input        int64
	CacheWrite   int64 // the transcript's flat cache-write total, always populated.
	CacheWrite5m int64 // 0 when the source transcript carries no TTL split.
	CacheWrite1h int64 // 0 when the source transcript carries no TTL split.
	CacheRead    int64
	Output       int64
}

// CostEvent is one stored, immutable per-turn ledger row.
type CostEvent struct {
	ID             int64
	TranscriptPath string
	LineNo         int
	SessionID      string
	Project        string
	Role           Role
	Agent          string // non-empty only when Role == RoleAgent.
	Tool           string // joined tool_use names this turn invoked; "" if none.
	ModelID        string
	Tokens         TokenCounts
	Amounts        BucketAmounts
	Total          Money
	PriceBasis     string
	PriceAsOf      string
	Errored        bool
	ErrorReason    string
	IngestedAt     string
}

// QueryFilter narrows Query and Identity to a subset of stored cost events. Every non-empty
// field is ANDed together; a zero-value QueryFilter matches every event in the store.
type QueryFilter struct {
	TranscriptPath string
	Session        string
	Project        string
	Role           Role
	Agent          string
	Tool           string
	Errored        *bool
}

// whereClause renders f as a SQL WHERE fragment (without the leading "WHERE") plus its bound
// arguments, in a fixed field order so the same filter always renders identically.
func (f QueryFilter) whereClause() (string, []any) {
	var clauses []string
	var args []any
	add := func(col, val string) {
		if val != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, val)
		}
	}
	add("transcript_path", f.TranscriptPath)
	add("session_id", f.Session)
	add("project", f.Project)
	add("role", string(f.Role))
	add("agent", f.Agent)
	add("tool", f.Tool)
	if f.Errored != nil {
		clauses = append(clauses, "errored = ?")
		args = append(args, boolToInt(*f.Errored))
	}
	if len(clauses) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(clauses, " AND "), args
}

const eventColumns = `id, transcript_path, line_no, session_id, project, role, agent, tool, model_id,
	input_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens, cache_read_tokens, output_tokens,
	input_cost, cache_write_cost, cache_read_cost, output_cost, total_cost,
	price_basis, price_as_of, errored, error_reason, ingested_at`

// Query returns every stored cost event matching f, ordered by transcript path then line number
// for a deterministic, replayable result.
func (s *Store) Query(f QueryFilter) ([]CostEvent, error) {
	where, args := f.whereClause()
	rows, err := s.db.Query(
		`SELECT `+eventColumns+` FROM cost_events WHERE `+where+` ORDER BY transcript_path, line_no`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("cost: query events: %w", err)
	}
	defer rows.Close()

	var out []CostEvent
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("cost: scan event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cost: query events: %w", err)
	}
	return out, nil
}

// rowScanner is the subset of *sql.Rows this package's row-to-struct helpers need, so a single
// row read via QueryRow can share the same scan logic as a multi-row Query result.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(r rowScanner) (CostEvent, error) {
	var e CostEvent
	var role, errorReason string
	var errored int
	var inputCost, writeCost, readCost, outputCost, totalCost int64
	err := r.Scan(
		&e.ID, &e.TranscriptPath, &e.LineNo, &e.SessionID, &e.Project, &role, &e.Agent, &e.Tool, &e.ModelID,
		&e.Tokens.Input, &e.Tokens.CacheWrite, &e.Tokens.CacheWrite5m, &e.Tokens.CacheWrite1h, &e.Tokens.CacheRead, &e.Tokens.Output,
		&inputCost, &writeCost, &readCost, &outputCost, &totalCost,
		&e.PriceBasis, &e.PriceAsOf, &errored, &errorReason, &e.IngestedAt,
	)
	if err != nil {
		return CostEvent{}, err
	}
	e.Role = Role(role)
	e.ErrorReason = errorReason
	e.Errored = errored != 0
	e.Amounts = BucketAmounts{Input: Money(inputCost), CacheWrite: Money(writeCost), CacheRead: Money(readCost), Output: Money(outputCost)}
	e.Total = Money(totalCost)
	return e, nil
}
