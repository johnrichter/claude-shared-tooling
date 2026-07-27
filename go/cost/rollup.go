package cost

import "fmt"

// Dimension names one axis Rollup can group stored cost by.
type Dimension string

const (
	DimSession Dimension = "session"
	DimProject Dimension = "project"
	DimAgent   Dimension = "agent" // groups by "orchestrator", "agent:<name>", or "unmappable".
	DimTool    Dimension = "tool"
	DimError   Dimension = "error" // groups by whether the event was flagged errored.
)

// dimColumn maps a Dimension to the column(s) Rollup groups by in SQL. DimAgent needs both role
// and agent because "agent:<name>" isn't a stored column — it's composed in Go after the query.
func dimColumn(d Dimension) (string, error) {
	switch d {
	case DimSession:
		return "session_id", nil
	case DimProject:
		return "project", nil
	case DimAgent:
		return "role, agent", nil
	case DimTool:
		return "tool", nil
	case DimError:
		return "errored", nil
	default:
		return "", fmt.Errorf("cost: unknown rollup dimension %q", d)
	}
}

// RollupRow is one group's totals from a Rollup call.
type RollupRow struct {
	Key    string
	Cost   Money
	Events int
}

// agentKey renders a (role, agent) pair as DimAgent's group key.
func agentKey(role, agent string) string {
	switch Role(role) {
	case RoleAgent:
		return "agent:" + agent
	case RoleOrchestrator:
		return string(RoleOrchestrator)
	default:
		return string(RoleUnmappable)
	}
}

// Rollup groups every stored cost event matching f by dimension, returning one RollupRow per
// distinct group key, ordered by descending cost. Rollup is a read-only projection over Query's
// same event set — it never itself decides what counts as unmappable or residual; Identity is
// the function that turns an DimAgent-shaped rollup into an additive-identity verdict.
func (s *Store) Rollup(f QueryFilter, by Dimension) ([]RollupRow, error) {
	col, err := dimColumn(by)
	if err != nil {
		return nil, err
	}
	where, args := f.whereClause()

	if by == DimAgent {
		rows, err := s.db.Query(
			`SELECT role, agent, SUM(total_cost), COUNT(*) FROM cost_events WHERE `+where+` GROUP BY role, agent ORDER BY SUM(total_cost) DESC`,
			args...,
		)
		if err != nil {
			return nil, fmt.Errorf("cost: rollup by %s: %w", by, err)
		}
		defer rows.Close()
		var out []RollupRow
		for rows.Next() {
			var role, agent string
			var cost int64
			var n int
			if err := rows.Scan(&role, &agent, &cost, &n); err != nil {
				return nil, fmt.Errorf("cost: rollup by %s: %w", by, err)
			}
			out = append(out, RollupRow{Key: agentKey(role, agent), Cost: Money(cost), Events: n})
		}
		return out, rows.Err()
	}

	rows, err := s.db.Query(
		`SELECT `+col+`, SUM(total_cost), COUNT(*) FROM cost_events WHERE `+where+` GROUP BY `+col+` ORDER BY SUM(total_cost) DESC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("cost: rollup by %s: %w", by, err)
	}
	defer rows.Close()
	var out []RollupRow
	for rows.Next() {
		var key any
		var cost int64
		var n int
		if err := rows.Scan(&key, &cost, &n); err != nil {
			return nil, fmt.Errorf("cost: rollup by %s: %w", by, err)
		}
		out = append(out, RollupRow{Key: rollupKey(by, key), Cost: Money(cost), Events: n})
	}
	return out, rows.Err()
}

// rollupKey renders a scanned group value as a stable string key, special-casing DimError's
// 0/1 storage into "false"/"true" for a readable result.
func rollupKey(by Dimension, val any) string {
	if by == DimError {
		if n, ok := val.(int64); ok {
			return fmt.Sprint(n != 0)
		}
	}
	return fmt.Sprint(val)
}
