package characterize

import "fmt"

// BudgetCeilingExceededError reports a metered run whose real spend exceeded the per-run ceiling
// its caller declared -- even though that same ceiling was also passed to the probe itself. A
// caller must never assume a budget flag handed to a subprocess is self-enforcing; this is the
// check that catches it not having been honored.
type BudgetCeilingExceededError struct {
	CeilingUSD float64
	SpentUSD   float64
}

func (e *BudgetCeilingExceededError) Error() string {
	return fmt.Sprintf("characterize: run spent $%.4f, over its $%.4f per-run ceiling", e.SpentUSD, e.CeilingUSD)
}

// checkBudget enforces ceilingUSD against a completed run's real spentUSD. ceilingUSD <= 0 is a
// caller defect -- a metered run with no declared ceiling -- and is rejected before any probe
// runs; it is never treated as "no limit".
func checkBudget(ceilingUSD, spentUSD float64) error {
	if ceilingUSD <= 0 {
		return fmt.Errorf("characterize: per-run cost ceiling must be > 0, got %v", ceilingUSD)
	}
	if spentUSD > ceilingUSD {
		return &BudgetCeilingExceededError{CeilingUSD: ceilingUSD, SpentUSD: spentUSD}
	}
	return nil
}
