package adoption

import (
	"fmt"
	"math"
	"sort"

	"github.com/johnrichter/claude-shared-tooling/go/gate"
)

// PhaseAStartGatePercent is the adoption-rate floor Phase-A forced-use measurement starts at:
// at least this share of a governed operation's classified invocations must take the CLI route.
// Later phases raise this number; Rate always takes the gate as an argument, so raising it is a
// configuration change at the call site, never a change here.
const PhaseAStartGatePercent = 80

// CLIAdoption is one governed operation's adoption measurement against a gate: how many of its
// classified invocations took each route, the resulting rate, and gate.Band's verdict.
type CLIAdoption struct {
	Operation string
	CLICount  int
	RawCount  int
	Rate      float64
	Verdict   gate.Verdict
}

// Total is the number of classified invocations (CLI + raw) this measurement is based on.
func (a CLIAdoption) Total() int { return a.CLICount + a.RawCount }

// MetGate reports whether a's Rate cleared the gate it was measured against. Rate always bands
// against a ceiling of 100, so VerdictAbort is the only verdict that fails a's gate.
func (a CLIAdoption) MetGate() bool { return a.Verdict != gate.VerdictAbort }

// Rate computes, per GovernedOperation named across classifications, the share of its
// invocations that took RouteCLI and gate.Band's verdict for that share against
// [gatePercent, 100] - a floor-only band, so VerdictAbort is the sole failing outcome and
// VerdictWarn never fires (a rate can't exceed 100). An operation with no classified
// invocations is omitted: it has no rate to gate, and reporting 0% would be indistinguishable
// from measured non-adoption.
func Rate(classifications []Classification, gatePercent int) (map[string]CLIAdoption, error) {
	if gatePercent < 0 || gatePercent > 100 {
		return nil, fmt.Errorf("adoption: gatePercent %d out of range [0,100]", gatePercent)
	}

	counts := map[string]*CLIAdoption{}
	var order []string
	for _, c := range classifications {
		a, ok := counts[c.Operation]
		if !ok {
			a = &CLIAdoption{Operation: c.Operation}
			counts[c.Operation] = a
			order = append(order, c.Operation)
		}
		if c.Route == RouteCLI {
			a.CLICount++
		} else {
			a.RawCount++
		}
	}

	out := make(map[string]CLIAdoption, len(counts))
	for _, name := range order {
		a := counts[name]
		a.Rate = float64(a.CLICount) / float64(a.Total())
		rank := int(math.Round(a.Rate * 100))
		a.Verdict = gate.Band(rank, gatePercent, 100)
		out[name] = *a
	}
	return out, nil
}

// SortedOperationNames returns adoption's operation names in a stable, deterministic order, for
// rendering a Report without depending on Go's randomized map iteration.
func SortedOperationNames(adoption map[string]CLIAdoption) []string {
	names := make([]string, 0, len(adoption))
	for name := range adoption {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
