package cost

import "math"

// Money is a USD amount in micro-dollars (1e-6 USD): a fixed-point integer so additive-identity
// checks, rollups, and even-splits never accumulate the rounding drift float64 arithmetic would
// introduce over many turns. The model roster prices every bucket in USD per 1,000,000 tokens,
// so a raw token count converts straight to Money by multiplying against the rate with no
// separate "divide by a million" step — see moneyFromTokens.
type Money int64

// USD renders m as a dollar amount for display. It is a presentation-boundary conversion only:
// nothing in this package does further arithmetic on the float64 it returns.
func (m Money) USD() float64 { return float64(m) / 1e6 }

// moneyFromTokens prices a raw token count at ratePerMillion (USD per 1,000,000 tokens),
// rounding to the nearest micro-dollar rather than truncating so a long rollup's cumulative
// rounding error stays bounded in both directions instead of drifting consistently low.
func moneyFromTokens(tokens int64, ratePerMillion float64) Money {
	return Money(math.Round(float64(tokens) * ratePerMillion))
}
