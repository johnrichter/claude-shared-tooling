package plugin_behavioral

import "github.com/johnrichter/claude-shared-tooling/go/transcript"

// CaseKind selects how a Case's compliance is established.
type CaseKind string

const (
	// KindProbe is a single-shot, live, metered `claude -p` invocation this package's own matrix
	// driver runs and grades -- valid only for a behavior a single turn can exercise.
	KindProbe CaseKind = "probe"
	// KindAgentic is an agentic/multi-step behavior (dispatch, orchestration): graded by a $0
	// MechanismCheck plus an Observer over an ALREADY-CAPTURED real multi-turn session
	// transcript. Run never spawns a probe for this kind -- a single-shot probe gives a model no
	// reason to dispatch at all.
	KindAgentic CaseKind = "agentic"
)

// Case is one behavioral case Run grades -- typically one per surface a Phase-1 capability
// manifest identified, though this package does not itself derive cases from a manifest; that
// derivation belongs to whichever caller owns case authoring.
type Case struct {
	// ID is this case's stable identifier -- the id a manifest surface's own case_ids names, so
	// Report.MissingCoverage can compute manifest-minus-executed against it.
	ID string
	// Kind selects which of the fields below Run reads for this case.
	Kind CaseKind
	// GuardsAgainstOverTriggering marks a NEGATIVE case (checking the behavior does NOT fire):
	// for such a case, an Inconclusive verdict is the expected, passing outcome. The zero value
	// (false) is a POSITIVE case -- the safe default -- where Inconclusive means the case failed
	// to put its own precondition to the test and is graded Violated. See GradeInconclusive.
	GuardsAgainstOverTriggering bool

	// Prompt is the exact text sent to the probe. KindProbe only.
	Prompt string
	// ForbiddenTerms is the leakage-lint list: every tool/skill/subagent-role/binary name this
	// case's own prompt must not name. KindProbe only.
	ForbiddenTerms []string
	// CwdSeed, if non-nil, lays down case-specific content (a git repo, fixture files) inside the
	// throwaway working directory Run provisions for this case, before the probe runs. KindProbe
	// only; nil leaves the throwaway a plain marked directory.
	CwdSeed func(dir string) error
	// Classify grades this case's captured trial. Required for KindProbe.
	Classify ProbeClassifier

	// Mechanism is the $0 wiring check. Required for KindAgentic.
	Mechanism MechanismCheck
	// TranscriptSource/TranscriptsRoot/TranscriptsScope/SessionID locate the real, already-
	// captured multi-turn session Observe grades -- the same TranscriptSource.ResolvePath
	// inputs. KindAgentic only.
	TranscriptSource transcript.TranscriptSource
	TranscriptsRoot  string
	TranscriptsScope string
	SessionID        string
	// Observe grades the located session transcript. Required for KindAgentic.
	Observe Observer
}

// CaseRecord is one case's graded outcome. Model and Trial are the zero value for a KindAgentic
// case, which runs once regardless of Options.Models/TrialsPerModel.
type CaseRecord struct {
	CaseID   string
	Kind     CaseKind
	Model    string
	Trial    int
	Outcome  Outcome
	Evidence string
	SpentUSD float64
	// Aborted is true when this specific trial's own per-trial budget ceiling was exceeded.
	Aborted bool
}
