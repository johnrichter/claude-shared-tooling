package cost

import (
	"fmt"
	"math"
)

// SanityResult compares the store's precise, bucketed cost total against an independent, rough
// estimate, to catch a gross pricing defect (e.g. a rate applied against the wrong unit) that a
// unit test over one fixture would not: RelativeDelta is |Precise-Estimated| / Precise, undefined
// (left at 0, WithinTolerance forced false) when Precise is 0 but Estimated is not.
type SanityResult struct {
	PreciseTotal    Money
	EstimatedTotal  Money
	RelativeDelta   float64
	WithinTolerance bool
}

// SanityCheck sums every stored cost event matching f (the precise total, from the per-bucket
// rate card) and compares it against totalTokens*blendedRatePerMillion (the estimate) — a
// caller-supplied, independent reference figure, deliberately not derived from this package's
// own rate card, since checking the roster against itself would never catch a defect in how
// this package applies it. tolerancePct bounds the acceptable relative delta (e.g. 0.5 for 50%)
// — SanityCheck is a coarse smoke check for an order-of-magnitude bug, not a precision audit.
func (s *Store) SanityCheck(f QueryFilter, blendedRatePerMillion, tolerancePct float64) (SanityResult, error) {
	events, err := s.Query(f)
	if err != nil {
		return SanityResult{}, fmt.Errorf("cost: sanity check: %w", err)
	}

	var precise Money
	var totalTokens int64
	for _, e := range events {
		precise += e.Total
		totalTokens += e.Tokens.Input + e.Tokens.CacheWrite + e.Tokens.CacheRead + e.Tokens.Output
	}
	estimate := moneyFromTokens(totalTokens, blendedRatePerMillion)

	result := SanityResult{PreciseTotal: precise, EstimatedTotal: estimate}
	if precise == 0 {
		result.WithinTolerance = estimate == 0
		return result, nil
	}
	diff := precise - estimate
	if diff < 0 {
		diff = -diff
	}
	result.RelativeDelta = float64(diff) / math.Abs(float64(precise))
	result.WithinTolerance = result.RelativeDelta <= tolerancePct
	return result, nil
}
