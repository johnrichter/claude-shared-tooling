package adoption

import "fmt"

// HardFloorViolation is one HookEvalRecord that broke the invariant forced-use measurement can
// never trade off against a passing adoption rate: a routing hook denying that a tool exists at
// all, rather than denying a raw invocation and redirecting to its governed CLI. The two are not
// equivalent - deny-and-redirect teaches an agent the sanctioned path; denying existence teaches
// it a false model of its own capabilities, and no adoption number offsets that.
type HardFloorViolation struct {
	Record HookEvalRecord
}

// Error renders the violation for a report or a clikit diagnostic message.
func (v HardFloorViolation) Error() string {
	return fmt.Sprintf("hook-eval record for tool %q (session %s) denies the tool exists", v.Record.ToolName, v.Record.SessionID)
}

// CheckFloor scans records and returns every HardFloorViolation found, in record order. Unlike
// Rate's gate, this floor has no tolerance and no configurable threshold: a caller treats any
// non-empty result as a failing run regardless of how the per-operation adoption rates measure.
func CheckFloor(records []HookEvalRecord) []HardFloorViolation {
	var out []HardFloorViolation
	for _, rec := range records {
		if rec.DeniesToolExists {
			out = append(out, HardFloorViolation{Record: rec})
		}
	}
	return out
}
