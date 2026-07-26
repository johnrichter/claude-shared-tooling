package cost

import (
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// tokenCountsToUsage reconstructs the transcript.Usage priceUsage expects from a stored event's
// TokenCounts, so Reprice can reuse the same pricing logic Ingest used rather than a second copy
// of the cache-write tiering rule.
func tokenCountsToUsage(t TokenCounts) transcript.Usage {
	return transcript.Usage{
		InputTokens:              t.Input,
		CacheCreationTokens:      t.CacheWrite,
		CacheCreationEphemeral5m: t.CacheWrite5m,
		CacheCreationEphemeral1h: t.CacheWrite1h,
		CacheReadTokens:          t.CacheRead,
		OutputTokens:             t.Output,
	}
}

// RateHistoryEntry is one dated rate this package has observed for a model, at the point in
// time Ingest first saw it. It is never updated after insert — a model's rate history only ever
// grows.
type RateHistoryEntry struct {
	ModelID  string
	AsOf     string // "YYYY-MM-DD", the date this rate was first observed.
	Snapshot PriceSnapshot
}

// RateHistory returns every rate observed for modelID, oldest first.
func (s *Store) RateHistory(modelID string) ([]RateHistoryEntry, error) {
	rows, err := s.db.Query(
		`SELECT model_id, as_of, basis, input, output, cache_read, cache_write_5m, cache_write_1h
		 FROM rate_history WHERE model_id = ? ORDER BY as_of ASC`,
		modelID,
	)
	if err != nil {
		return nil, fmt.Errorf("cost: rate history for %s: %w", modelID, err)
	}
	defer rows.Close()

	var out []RateHistoryEntry
	for rows.Next() {
		var e RateHistoryEntry
		if err := rows.Scan(&e.ModelID, &e.AsOf, &e.Snapshot.Basis, &e.Snapshot.Input, &e.Snapshot.Output,
			&e.Snapshot.CacheRead, &e.Snapshot.CacheWrite5m, &e.Snapshot.CacheWrite1h); err != nil {
			return nil, fmt.Errorf("cost: rate history for %s: %w", modelID, err)
		}
		e.Snapshot.ModelID = e.ModelID
		out = append(out, e)
	}
	return out, rows.Err()
}

// Reprice recomputes cost for events using the rate that was in effect as of asOf ("YYYY-MM-DD")
// for each event's model, without touching any stored cost_events row — the sanctioned way to
// answer "what would this have cost under an earlier/later rate" without mutating the immutable
// per-turn snapshot Query returns. An event whose model has no rate_history entry at or before
// asOf falls back to its own stored, as-ingested snapshot (the earliest rate this package has
// ever recorded is the earliest one it can price a historical question against).
func (s *Store) Reprice(events []CostEvent, asOf string) (Money, error) {
	cache := map[string][]RateHistoryEntry{}
	var total Money
	for _, e := range events {
		history, ok := cache[e.ModelID]
		if !ok {
			var err error
			history, err = s.RateHistory(e.ModelID)
			if err != nil {
				return 0, err
			}
			cache[e.ModelID] = history
		}
		snap := rateAsOf(history, asOf, e)
		total += priceUsage(tokenCountsToUsage(e.Tokens), snap).Total()
	}
	return total, nil
}

// rateAsOf picks the latest rate_history entry at or before asOf, falling back to fallback's own
// stored price_basis/tokens-implied snapshot when no history entry qualifies.
func rateAsOf(history []RateHistoryEntry, asOf string, fallback CostEvent) PriceSnapshot {
	var chosen *PriceSnapshot
	for i := range history {
		if history[i].AsOf <= asOf {
			chosen = &history[i].Snapshot
		} else {
			break
		}
	}
	if chosen != nil {
		return *chosen
	}
	return snapshotFromEvent(fallback)
}

// snapshotFromEvent reconstructs the rate a stored event was actually priced at, from its own
// stored amounts and token counts — the exact rate divides back out per bucket.
func snapshotFromEvent(e CostEvent) PriceSnapshot {
	rate := func(cost Money, tokens int64) float64 {
		if tokens == 0 {
			return 0
		}
		return float64(cost) / float64(tokens)
	}
	return PriceSnapshot{
		ModelID:      e.ModelID,
		Basis:        e.PriceBasis,
		Input:        rate(e.Amounts.Input, e.Tokens.Input),
		Output:       rate(e.Amounts.Output, e.Tokens.Output),
		CacheRead:    rate(e.Amounts.CacheRead, e.Tokens.CacheRead),
		CacheWrite5m: rate(e.Amounts.CacheWrite, e.Tokens.CacheWrite),
		CacheWrite1h: rate(e.Amounts.CacheWrite, e.Tokens.CacheWrite),
	}
}
