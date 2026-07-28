package plugin_behavioral

import "fmt"

// EstimateTotalBudget computes the M5 §6 default hard total ceiling: trials x models x
// per-probe-cost. Options.TotalBudgetUSD uses this when left at zero; a caller may still declare
// a stricter explicit ceiling instead.
func EstimateTotalBudget(trialsPerModel, modelCount int, perProbeCostUSD float64) float64 {
	if trialsPerModel <= 0 || modelCount <= 0 || perProbeCostUSD <= 0 {
		return 0
	}
	return float64(trialsPerModel*modelCount) * perProbeCostUSD
}

// TotalBudgetExceededError reports a matrix run whose cumulative real spend crossed its hard
// total ceiling. Run aborts (stops launching further trials) the moment this is detected; the
// records already collected are still returned.
type TotalBudgetExceededError struct {
	CeilingUSD float64
	SpentUSD   float64
}

func (e *TotalBudgetExceededError) Error() string {
	return fmt.Sprintf("plugin-behavioral: matrix spent $%.4f, over its $%.4f hard total ceiling", e.SpentUSD, e.CeilingUSD)
}

// checkTotalBudget enforces ceilingUSD against the matrix's cumulative real spentUSD so far.
// ceilingUSD <= 0 is a caller defect -- a metered matrix with no declared ceiling -- and is
// rejected before any trial runs, never treated as "no limit".
func checkTotalBudget(ceilingUSD, spentUSD float64) error {
	if ceilingUSD <= 0 {
		return fmt.Errorf("plugin-behavioral: total budget ceiling must be > 0, got %v", ceilingUSD)
	}
	if spentUSD > ceilingUSD {
		return &TotalBudgetExceededError{CeilingUSD: ceilingUSD, SpentUSD: spentUSD}
	}
	return nil
}

// TrialAbortedError reports one trial whose own real spend exceeded its per-trial ceiling -- the
// second, independent half of M5 §6's discipline: a hard total ceiling only catches an overrun
// after the fact, at the matrix level; a single trial that spikes (e.g. a probe design change that
// actually forces a real dispatch, multiplying cost for that one trial) must be caught on its own,
// without waiting for the total to notice. The trial's own spend is still counted toward the
// matrix total -- the spend already happened -- but the trial itself is graded Violated, never a
// clean pass.
type TrialAbortedError struct {
	CeilingUSD float64
	SpentUSD   float64
}

func (e *TrialAbortedError) Error() string {
	return fmt.Sprintf("plugin-behavioral: trial spent $%.4f, over its $%.4f per-trial ceiling -- aborted", e.SpentUSD, e.CeilingUSD)
}

// checkTrialBudget enforces ceilingUSD against one trial's own real spentUSD -- the same
// never-trust-the-flag discipline characterize.checkBudget applies at the run level, reapplied
// per trial: a per-trial ceiling handed to the probe as --max-budget-usd is never assumed
// self-enforcing.
func checkTrialBudget(ceilingUSD, spentUSD float64) error {
	if ceilingUSD <= 0 {
		return fmt.Errorf("plugin-behavioral: per-trial budget ceiling must be > 0, got %v", ceilingUSD)
	}
	if spentUSD > ceilingUSD {
		return &TrialAbortedError{CeilingUSD: ceilingUSD, SpentUSD: spentUSD}
	}
	return nil
}
