package adoption

import (
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// Report is one forced-use adoption run's full result: per-operation adoption against the gate,
// every hard-floor violation found, and the hook-firing tally. FloorViolations governs
// regardless of Adoption - see CheckFloor's doc.
type Report struct {
	GatePercent     int
	Adoption        map[string]CLIAdoption
	FloorViolations []HardFloorViolation
	HookFiring      HookFiringReport
}

// BuildReport runs Rate and CheckFloor over classifications and hookRecords against gatePercent
// and assembles the combined Report.
func BuildReport(classifications []Classification, hookRecords []HookEvalRecord, gatePercent int) (Report, error) {
	adoption, err := Rate(classifications, gatePercent)
	if err != nil {
		return Report{}, err
	}
	return Report{
		GatePercent:     gatePercent,
		Adoption:        adoption,
		FloorViolations: CheckFloor(hookRecords),
		HookFiring:      ReportHookFiring(hookRecords),
	}, nil
}

// Result composes rep into the one clikit record a forced-use adoption CLI or CI job emits. Its
// governing class, in order: a hard-floor violation always governs first
// (precondition_unmet.adoption.tool_existence_denied, one error per violation) regardless of how
// adoption otherwise measures; short of that, any operation below its gate governs instead
// (gate_negative.adoption.rate_below_gate, one error per operation); a clean run reports
// success, with a caveat when the hook-firing tally shows at least one failed-open evaluation.
func (rep Report) Result(command []string) (*clikit.Result, error) {
	data := map[string]any{
		"gate_percent":          rep.GatePercent,
		"hook_fired":            rep.HookFiring.Fired,
		"hook_failed_open":      rep.HookFiring.FailedOpen,
		"operations_below_gate": belowGateCount(rep.Adoption),
	}

	if len(rep.FloorViolations) > 0 {
		errs := make([]clikit.Diagnostic, 0, len(rep.FloorViolations))
		for _, v := range rep.FloorViolations {
			e, err := clikit.NewError("precondition_unmet.adoption.tool_existence_denied", v.Error(),
				clikit.Manual("fix the routing hook to deny-and-redirect to the governed CLI instead of denying the tool exists, then re-run"),
				map[string]any{"session_id": v.Record.SessionID, "tool_name": v.Record.ToolName})
			if err != nil {
				return nil, err
			}
			errs = append(errs, e)
		}
		return clikit.NewPreconditionUnmet(command, data, errs, nil)
	}

	var belowGate []clikit.Diagnostic
	for _, name := range SortedOperationNames(rep.Adoption) {
		a := rep.Adoption[name]
		if a.MetGate() {
			continue
		}
		e, err := clikit.NewError("gate_negative.adoption.rate_below_gate",
			fmt.Sprintf("operation %q adopted its CLI in %d/%d invocations, below the %d%% gate", name, a.CLICount, a.Total(), rep.GatePercent),
			clikit.Manual("raise the operation's CLI adoption rate, or lower the gate if the target is not yet achievable"),
			map[string]any{"operation": name, "cli_count": a.CLICount, "raw_count": a.RawCount, "gate_percent": rep.GatePercent})
		if err != nil {
			return nil, err
		}
		belowGate = append(belowGate, e)
	}
	if len(belowGate) > 0 {
		return clikit.NewGateNegative(command, data, belowGate, nil)
	}

	if rep.HookFiring.FailedOpen > 0 {
		cv, err := clikit.NewCaveat("caveats.adoption.hook_failed_open",
			fmt.Sprintf("%d hook evaluation(s) failed open on a governed operation", rep.HookFiring.FailedOpen),
			clikit.Manual("investigate why the routing hook did not fire for these invocations"), nil)
		if err != nil {
			return nil, err
		}
		return clikit.NewCaveats(command, data, []clikit.Diagnostic{cv})
	}
	return clikit.NewSuccess(command, data)
}

func belowGateCount(adoption map[string]CLIAdoption) int {
	n := 0
	for _, a := range adoption {
		if !a.MetGate() {
			n++
		}
	}
	return n
}
