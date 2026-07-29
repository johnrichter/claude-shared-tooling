package plugin_behavioral

import (
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/adoption"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// MechanismCheck is the $0, no-model half of validating a KindAgentic case: does the wiring this
// behavior depends on actually exist (a subagent role file is present and registered, a launcher
// a dispatch depends on resolves on PATH, ...). It never touches a transcript or spends anything,
// and it is always deterministic -- a mechanism check never returns Inconclusive. A caller
// supplies this function; this package hardcodes no specific plugin's wiring shape.
type MechanismCheck func() (ClassifierResult, error)

// ProbeClassifier grades one captured KindProbe trial. A caller supplies this per case; this
// package's own capture harness (capture.go) never calls into a classifier itself, keeping the
// invocation harness and the grading logic in separate files with no shared state.
type ProbeClassifier func(ProbeObservation) ClassifierResult

// Observer grades an agentic/multi-step behavior against a real, already-captured multi-turn
// session transcript -- never a probe this package launches itself. source/root/scope/sessionID
// are the same TranscriptSource.ResolvePath inputs a caller resolves the session by; a caller
// supplies this function so this package hardcodes no specific behavior's shape.
type Observer func(source transcript.TranscriptSource, root, scope, sessionID string) (ClassifierResult, error)

// FileExists returns a MechanismCheck for the simplest wiring archetype: does path resolve to a
// real file on disk. It never returns Inconclusive -- a file either exists or it does not.
func FileExists(path string) MechanismCheck {
	return func() (ClassifierResult, error) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return ClassifierResult{Outcome: Violated, Evidence: fmt.Sprintf("%q does not resolve to a real file", path)}, nil
		}
		return ClassifierResult{Outcome: Honored, Evidence: fmt.Sprintf("%q resolves to a real file", path)}, nil
	}
}

// ToolInvoked returns a ProbeClassifier for the simplest probe archetype: did the trial's own
// captured transcript record wantToolName being invoked at all. Honored when found; Inconclusive
// (never Violated -- this archetype cannot distinguish "explicitly avoided" from "never came up")
// otherwise, so GradeInconclusive's positive/negative framing is what turns that into a case's
// real verdict.
func ToolInvoked(wantToolName string) ProbeClassifier {
	return func(obs ProbeObservation) ClassifierResult {
		for _, inv := range obs.Invocations {
			if inv.ToolName == wantToolName {
				return ClassifierResult{
					Outcome:  Honored,
					Evidence: fmt.Sprintf("tool %q observed in this trial's own transcript (line %d)", wantToolName, inv.LineNo),
				}
			}
		}
		return ClassifierResult{
			Outcome:  Inconclusive,
			Evidence: fmt.Sprintf("tool %q never observed in this trial's own transcript", wantToolName),
		}
	}
}

// DispatchObserved is the multi-turn observation half of validating an agentic dispatch: it
// grades a real, already-captured session transcript for a Task-tool invocation whose
// subagent_type equals wantSubagentType, at any nesting depth (a subagent's own subagent
// transcripts are included, via adoption.LoadSessionInvocations). This is deliberately never a
// probe this package launches -- a single-shot invocation gives a model no reason to dispatch at
// all, so an agentic case is only gradeable against a session that actually ran multi-turn.
// Bind wantSubagentType with a closure to get an Observer: Case.Observe = func(src
// transcript.TranscriptSource, root, scope, sessionID string) (ClassifierResult, error) {
// return DispatchObserved(src, root, scope, sessionID, "the-role") }.
func DispatchObserved(source transcript.TranscriptSource, root, scope, sessionID, wantSubagentType string) (ClassifierResult, error) {
	invocations, err := adoption.LoadSessionInvocations(source, root, scope, sessionID)
	if err != nil {
		return ClassifierResult{}, fmt.Errorf("plugin-behavioral: load session invocations: %w", err)
	}
	for _, inv := range invocations {
		if inv.ToolName != "Task" {
			continue
		}
		subagentType, _ := inv.Input["subagent_type"].(string)
		if subagentType == wantSubagentType {
			return ClassifierResult{
				Outcome:  Honored,
				Evidence: fmt.Sprintf("Task dispatch to subagent_type=%q observed in the session transcript (line %d)", subagentType, inv.LineNo),
			}, nil
		}
	}
	return ClassifierResult{
		Outcome:  Inconclusive,
		Evidence: fmt.Sprintf("no Task dispatch to subagent_type=%q appears anywhere in this session's transcript (main or subagent)", wantSubagentType),
	}, nil
}
