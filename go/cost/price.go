package cost

import (
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/roster"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// PriceSnapshot is the resolved per-million-token rate table a cost event was priced against,
// captured once at ingestion time. It is never recomputed after a cost event is stored — a
// later roster refresh reprices nothing already ingested. RateHistory and Reprice are the
// sanctioned path to a historical what-if instead of mutating a stored snapshot.
type PriceSnapshot struct {
	ModelID      string
	Basis        string // "contract" or "list", per roster.PriceTable.Basis.
	Input        float64
	Output       float64
	CacheRead    float64
	CacheWrite5m float64
	CacheWrite1h float64
}

// resolvePrice resolves modelID's current roster rate. modelID may carry the roster's documented
// dated-snapshot suffix (e.g. claude-sonnet-5-20260724) or its "[1m]" long-context-window
// selector (e.g. claude-sonnet-5[1m]) — roster.Price normalizes both before lookup; this
// function never strips or reinterprets the id itself.
//
// CONTEXT-VARIANT-CONTRACT: roster.Price resolves a [1m]-suffixed id to that variant's rate
// table for the whole turn, with no token count to test against the variant's declared
// premium-applies-above-input-tokens threshold. A turn recorded under a [1m] model id whose
// actual input stayed under that threshold is therefore still billed at the variant rate here —
// a deliberate over-estimate, accepted rather than threading a per-turn token count through
// roster.Price for every consumer. A model roster.Price cannot resolve (unknown id, no sourced
// rate) fails loudly here rather than pricing the turn at zero or a guessed rate.
func resolvePrice(modelID string) (PriceSnapshot, error) {
	t, err := roster.Price(modelID)
	if err != nil {
		return PriceSnapshot{}, fmt.Errorf("cost: resolve rate for model %q: %w", modelID, err)
	}
	return PriceSnapshot{
		ModelID:      modelID,
		Basis:        t.Basis,
		Input:        t.Input,
		Output:       t.Output,
		CacheRead:    t.CacheRead,
		CacheWrite5m: t.CacheWrite5m,
		CacheWrite1h: t.CacheWrite1h,
	}, nil
}

// BucketAmounts is one turn's cost split across the four token classes transcript.Usage reports:
// input, cache write, cache read, output.
type BucketAmounts struct {
	Input      Money
	CacheWrite Money
	CacheRead  Money
	Output     Money
}

// Total sums the four buckets.
func (b BucketAmounts) Total() Money {
	return b.Input + b.CacheWrite + b.CacheRead + b.Output
}

// priceUsage prices one turn's Usage against snap. A cache write splits by TTL tier when the
// transcript carries the ephemeral 5m/1h breakdown — a cache write held for the 1h tier costs
// roughly double the 5m tier's rate, so pricing a mixed-TTL turn at one flat cache-write rate
// would misprice it. A transcript that only reports the flat CacheCreationTokens total (no
// tier split) is priced at the 5m rate, Claude Code's default cache_control ttl.
func priceUsage(u transcript.Usage, snap PriceSnapshot) BucketAmounts {
	var write Money
	if u.CacheCreationEphemeral5m > 0 || u.CacheCreationEphemeral1h > 0 {
		write = moneyFromTokens(u.CacheCreationEphemeral5m, snap.CacheWrite5m) +
			moneyFromTokens(u.CacheCreationEphemeral1h, snap.CacheWrite1h)
	} else {
		write = moneyFromTokens(u.CacheCreationTokens, snap.CacheWrite5m)
	}
	return BucketAmounts{
		Input:      moneyFromTokens(u.InputTokens, snap.Input),
		CacheWrite: write,
		CacheRead:  moneyFromTokens(u.CacheReadTokens, snap.CacheRead),
		Output:     moneyFromTokens(u.OutputTokens, snap.Output),
	}
}
