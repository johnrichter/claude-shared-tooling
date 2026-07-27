package bandcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/johnrichter/claude-shared-tooling/go/gate"
)

// ObservedGateFiring is one real, already-observed gate firing a caller extracted from wherever
// its own hook-evaluation log lives: which gate fired, and the path it fired on. This package
// never derives it from the registry — it is always the caller's ground truth to check the
// registry's declared triggers against.
type ObservedGateFiring struct {
	GateID string
	Path   string
}

// TriggerMiss names one rung-2 entry whose declared trigger glob never matched a single observed
// firing for its gate: either the gate never actually ran during the observation window, or its
// declared scope has drifted away from what fires in practice.
type TriggerMiss struct {
	EntryID string
	GateID  string
	Glob    string
}

// UndeclaredFiring names one observed gate firing that no shipped rung-2 entry's declared trigger,
// for that gate, would have matched — the gate fired on more than the registry declares.
type UndeclaredFiring struct {
	GateID string
	Path   string
}

// RegistryFiringReport is CheckRegistryFiring's declared-vs-actual verdict. Unparsed names every
// shipped rung-2 entry whose Trigger field is not a literal "<event> on <glob>[, <glob>]*" clause
// (e.g. a command-string or extension-set condition) — this check has no glob to hold those to,
// and reports that gap rather than silently skipping it. Clean is true only when Misses,
// Undeclared and Unparsed are all empty.
type RegistryFiringReport struct {
	Misses     []TriggerMiss
	Undeclared []UndeclaredFiring
	Unparsed   []string
}

// Clean reports whether the registry's declared triggers and the observed firings agree exactly:
// no declared-but-unfired trigger, no undeclared firing, and no shipped entry whose trigger could
// not be parsed to a glob to check at all. An Unparsed entry is a shipped declaration this check
// verified nothing about, so folding it in keeps a checked-nothing entry from passing as a clean
// bill -- the same false-clean-bill guard OverfireReport.Clean applies to an absent Scope.
func (r RegistryFiringReport) Clean() bool {
	return len(r.Misses) == 0 && len(r.Undeclared) == 0 && len(r.Unparsed) == 0
}

// TriggerGlobs extracts the literal glob patterns from a rung-2 Trigger clause of the shape
// "<event> on <glob>[, <glob>]*" (e.g. "PreToolUse:Write,Edit on **/.claude/agents/**,
// **/SKILL.md"). ok is false when the clause has no " on " separator, or any token after "on"
// still contains whitespace — a trigger declared as an open-ended condition ("on any command
// string, matched in-script...") names no literal glob set, and this function never guesses one
// out of prose.
func TriggerGlobs(trigger string) (globs []string, ok bool) {
	_, clause, found := strings.Cut(trigger, " on ")
	if !found {
		return nil, false
	}
	for _, tok := range strings.Split(clause, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" || strings.ContainsAny(tok, " \t") {
			return nil, false
		}
		globs = append(globs, tok)
	}
	return globs, len(globs) > 0
}

// CheckRegistryFiring is the SC-ENFORCE declared-vs-actual precision check for rung-2 registry
// entries: declared reads Rung2Declaration values sourced from gate.Rung2Declarations() — this
// function takes no registry path or JSON of its own, so a caller can never hand it a second
// copy of the registry to drift against the one gate owns. Only entries with Status == "shipped"
// are checked; "planned" declares intent with a target that need not fire yet, and "retired" is
// history — neither is a pass or fail input here.
func CheckRegistryFiring(declared []gate.Rung2Declaration, observed []ObservedGateFiring) (RegistryFiringReport, error) {
	globsByGate := map[string][]string{}
	fired := map[string]map[string]bool{}
	var unparsed []string

	for _, d := range declared {
		if d.Status != "shipped" {
			continue
		}
		globs, ok := TriggerGlobs(d.Trigger)
		if !ok {
			unparsed = append(unparsed, d.ID)
			continue
		}
		globsByGate[d.GateID] = append(globsByGate[d.GateID], globs...)
		if fired[d.GateID] == nil {
			fired[d.GateID] = map[string]bool{}
		}
		for _, g := range globs {
			fired[d.GateID][g] = false
		}
	}

	var undeclared []UndeclaredFiring
	for _, o := range observed {
		globs, known := globsByGate[o.GateID]
		if !known {
			undeclared = append(undeclared, UndeclaredFiring{GateID: o.GateID, Path: o.Path})
			continue
		}
		hit := false
		for _, g := range globs {
			matched, err := doublestar.Match(g, o.Path)
			if err != nil {
				return RegistryFiringReport{}, fmt.Errorf("bandcheck: gate %q declares invalid glob %q: %w", o.GateID, g, err)
			}
			if matched {
				hit = true
				fired[o.GateID][g] = true
			}
		}
		if !hit {
			undeclared = append(undeclared, UndeclaredFiring{GateID: o.GateID, Path: o.Path})
		}
	}

	var misses []TriggerMiss
	for _, d := range declared {
		if d.Status != "shipped" {
			continue
		}
		globs, ok := TriggerGlobs(d.Trigger)
		if !ok {
			continue
		}
		for _, g := range globs {
			if !fired[d.GateID][g] {
				misses = append(misses, TriggerMiss{EntryID: d.ID, GateID: d.GateID, Glob: g})
			}
		}
	}

	sort.Slice(misses, func(i, j int) bool {
		if misses[i].EntryID != misses[j].EntryID {
			return misses[i].EntryID < misses[j].EntryID
		}
		return misses[i].Glob < misses[j].Glob
	})
	sort.Slice(undeclared, func(i, j int) bool {
		if undeclared[i].GateID != undeclared[j].GateID {
			return undeclared[i].GateID < undeclared[j].GateID
		}
		return undeclared[i].Path < undeclared[j].Path
	})
	sort.Strings(unparsed)

	return RegistryFiringReport{Misses: misses, Undeclared: undeclared, Unparsed: unparsed}, nil
}
