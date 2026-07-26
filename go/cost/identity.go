package cost

import (
	"fmt"
	"sort"
)

// ResidualItem is one itemized line of an IdentityReport's unmappable residual: never a single
// blended number, always traceable back to the specific transcript that could not be attributed.
type ResidualItem struct {
	TranscriptPath string
	Cost           Money
	Events         int
}

// IdentityReport is the additive-identity partition of every cost event matching a QueryFilter:
// Total always equals Orchestrator + sum(Agents) + Fixed + Residual, by construction — every
// event is assigned to exactly one of Orchestrator, Agents, or Residual, and Fixed is a purely
// additive extra term a caller supplies for spend this package never sees in a transcript (e.g.
// flat infrastructure cost). Diff exists only to catch an accounting bug in this package itself;
// a healthy report always has Diff == 0.
type IdentityReport struct {
	Total           Money
	Orchestrator    Money
	Agents          map[string]Money
	Fixed           Money
	Residual        Money
	Itemized        []ResidualItem
	Diff            Money
	WithinTolerance bool
}

// Identity computes the additive-identity partition for every event matching f and verifies
// Total - (Orchestrator + sum(Agents) + Fixed + Residual) is within tolerance of zero. fixed is
// a caller-supplied additive cost this package has no transcript signal for; pass 0 when there
// is none. tolerance is in Money (micro-USD); pass 0 to require an exact match.
func (s *Store) Identity(f QueryFilter, fixed, tolerance Money) (IdentityReport, error) {
	events, err := s.Query(f)
	if err != nil {
		return IdentityReport{}, fmt.Errorf("cost: identity: %w", err)
	}

	report := IdentityReport{Fixed: fixed, Agents: map[string]Money{}}
	residualByPath := map[string]*ResidualItem{}

	for _, e := range events {
		report.Total += e.Total
		switch e.Role {
		case RoleOrchestrator:
			report.Orchestrator += e.Total
		case RoleAgent:
			report.Agents[e.Agent] += e.Total
		default: // RoleUnmappable
			report.Residual += e.Total
			item := residualByPath[e.TranscriptPath]
			if item == nil {
				item = &ResidualItem{TranscriptPath: e.TranscriptPath}
				residualByPath[e.TranscriptPath] = item
			}
			item.Cost += e.Total
			item.Events++
		}
	}

	paths := make([]string, 0, len(residualByPath))
	for p := range residualByPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		report.Itemized = append(report.Itemized, *residualByPath[p])
	}

	identityTotal := report.Orchestrator + sumMoney(report.Agents) + report.Fixed + report.Residual
	report.Diff = report.Total - identityTotal
	d := report.Diff
	if d < 0 {
		d = -d
	}
	report.WithinTolerance = d <= tolerance
	return report, nil
}

func sumMoney(m map[string]Money) Money {
	var total Money
	for _, v := range m {
		total += v
	}
	return total
}

// EvenSplit distributes report's unmappable residual evenly (in dollars, never by token share —
// different buckets and models carry different per-token rates, so a token-proportional split
// would misrepresent cost) across agents, for a caller that wants a fully-attributed view
// without mutating the underlying itemized ledger. A remainder left after integer division is
// handed out one micro-dollar at a time to agents in sorted order, so the split's total always
// equals report.Residual exactly. Splitting a non-zero residual across zero agents is refused —
// there is nowhere for it to go.
func EvenSplit(report IdentityReport, agents []string) (map[string]Money, error) {
	if len(agents) == 0 {
		if report.Residual == 0 {
			return map[string]Money{}, nil
		}
		return nil, fmt.Errorf("cost: even-split: %d residual with no agents to split it across", report.Residual)
	}
	sorted := append([]string(nil), agents...)
	sort.Strings(sorted)

	share := report.Residual / Money(len(sorted))
	remainder := report.Residual % Money(len(sorted))

	out := make(map[string]Money, len(sorted))
	for i, a := range sorted {
		out[a] = share
		if Money(i) < remainder {
			out[a]++
		}
	}
	return out, nil
}
