package characterize

import (
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// modelHonorCheck is verifyModelHonored's outcome. attempted is false when there was nothing to
// check against (no source configured, or no readable transcript for the session) -- that is not
// a failure, just the absence of corroborating evidence. honored is only meaningful when attempted
// is true.
type modelHonorCheck struct {
	attempted bool
	honored   bool
	reason    string
}

// verifyModelHonored corroborates a characterizing run's resolved model against the session
// transcript the probe actually produced, through source rather than any hardcoded assumption
// about the transcript's on-disk format. This exists for the same reason checkBudget re-checks
// real spend instead of trusting the --max-budget-usd flag: a --model flag handed to a subprocess
// is not proof the subprocess honored it.
//
// The check is best-effort corroboration, never a precondition -- a nil source, an empty root, or
// a transcript this run's environment never wrote (e.g. transcripts are not persisted, or a test
// double never wrote one) all resolve to "nothing to attempt", not an error and not a reason to
// doubt an otherwise-valid manifest.
func verifyModelHonored(source transcript.TranscriptSource, root, scope, sessionID, wantModel string) modelHonorCheck {
	if source == nil || root == "" || sessionID == "" {
		return modelHonorCheck{}
	}
	f, err := os.Open(source.ResolvePath(root, scope, sessionID))
	if err != nil {
		return modelHonorCheck{}
	}
	defer f.Close()

	var seenModel string
	_ = source.Turns(f, func(t transcript.Turn) error {
		if seenModel == "" && t.Model != "" {
			seenModel = t.Model
		}
		return nil
	})

	switch {
	case seenModel == "":
		return modelHonorCheck{attempted: true, honored: false,
			reason: "the session transcript carried no turn reporting a model, so the resolved tier could not be corroborated"}
	case seenModel != wantModel:
		return modelHonorCheck{attempted: true, honored: false,
			reason: fmt.Sprintf("the session transcript reports model %q, but this run resolved to %q", seenModel, wantModel)}
	default:
		return modelHonorCheck{attempted: true, honored: true}
	}
}
