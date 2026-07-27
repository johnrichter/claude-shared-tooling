package gate

import "fmt"

// Verdict is the collapsed outcome of evaluating one rank against a band: below the floor,
// above the ceiling, or silently inside it.
type Verdict int

const (
	// VerdictAbort means rank fell below the band's floor.
	VerdictAbort Verdict = iota
	// VerdictSilent means rank landed inside [floor, ceiling] — nothing to report.
	VerdictSilent
	// VerdictWarn means rank rose above the band's ceiling, or the input could not be
	// measured at all (see Dimension).
	VerdictWarn
)

// String names a Verdict for logging and error messages.
func (v Verdict) String() string {
	switch v {
	case VerdictAbort:
		return "abort"
	case VerdictSilent:
		return "silent"
	case VerdictWarn:
		return "warn"
	default:
		return fmt.Sprintf("Verdict(%d)", int(v))
	}
}

// Band collapses a floor/ceiling threshold pair to one closed integer range and evaluates
// rank against it: below floor aborts, above ceiling warns, and anything in
// [floor, ceiling] — the collapsed range — is silent. floor and ceiling are always caller
// supplied; this function hardcodes neither.
//
// floor > ceiling is a caller error (an empty band, nothing can land silently): Band still
// evaluates it, since rank is necessarily either below floor or above ceiling in that case,
// but a caller constructing bands from configuration should validate floor <= ceiling itself.
func Band(rank, floor, ceiling int) Verdict {
	switch {
	case rank < floor:
		return VerdictAbort
	case rank > ceiling:
		return VerdictWarn
	default:
		return VerdictSilent
	}
}

// Dimension is one axis of a multi-axis check — one signal among several a caller evaluates
// independently against the same or different bands. Rank is nil when the signal could not be
// measured for this axis at all, as distinct from a measured rank that happens to fail.
type Dimension struct {
	Name string
	Rank *int
}

// EvaluateDimension resolves one Dimension against a floor/ceiling band. A nil Rank never
// falls through to Band — it cannot be compared to anything — and never resolves to
// VerdictSilent by default: an undetectable dimension is reported as VerdictWarn with ok
// false and an explicit warning naming the dimension and the band it could not be checked
// against. A caller that treats ok==false as a pass has misused this function; the whole
// point is that "could not measure" and "measured and fine" are never the same value.
func EvaluateDimension(d Dimension, floor, ceiling int) (verdict Verdict, ok bool, warning string) {
	if d.Rank == nil {
		return VerdictWarn, false, fmt.Sprintf(
			"dimension %q is undetectable: no signal to measure against band [%d, %d] — "+
				"treated as a loud warning, never a silent pass",
			d.Name, floor, ceiling,
		)
	}
	return Band(*d.Rank, floor, ceiling), true, ""
}
