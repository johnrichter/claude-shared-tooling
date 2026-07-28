package plugin_behavioral

// Outcome is a classifier's three-state verdict, never a boolean. The third state exists because
// a case whose precondition never appeared -- the triggering ask never surfaced, no dispatch
// happened, no governed edit was made -- has produced no evidence either way; scoring that
// absence as Violated would flag working guidance as broken.
type Outcome string

const (
	// Honored means the observable behavior was present and compliant.
	Honored Outcome = "honored"
	// Violated means the precondition for judging occurred AND the behavior was non-compliant.
	Violated Outcome = "violated"
	// Inconclusive means the precondition never appeared, so neither Honored nor Violated can be
	// asserted. GradeInconclusive is where a caller decides what this resolves to for a specific
	// case; a classifier itself never pre-collapses it into either of the other two.
	Inconclusive Outcome = "inconclusive"
)

// ClassifierResult is one classifier's verdict plus the evidence it rests on -- the specific
// observation the verdict is built from, suitable for a CaseRecord's own Evidence field.
type ClassifierResult struct {
	Outcome  Outcome
	Evidence string
}

// GradeInconclusive applies this package's mandatory fail-safe bias to a classifier's raw
// Inconclusive outcome; Honored and Violated pass through unchanged. positiveCase is true for a
// case whose prompt/mechanism is shaped to elicit the behavior under test -- the common case, and
// the safe default a caller gets by leaving Case.GuardsAgainstOverTriggering at its zero value:
// for such a case, Inconclusive means the case failed to put its own precondition to the test,
// which is itself a defect in the case, not a pass, so it is graded Violated. A case explicitly
// guarding against OVER-triggering (positiveCase=false) is the one exception, where Inconclusive
// is the expected, passing outcome.
func GradeInconclusive(result ClassifierResult, positiveCase bool) Outcome {
	if result.Outcome != Inconclusive {
		return result.Outcome
	}
	if positiveCase {
		return Violated
	}
	return Honored
}
