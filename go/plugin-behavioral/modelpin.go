package plugin_behavioral

import (
	"os"
	"strings"
)

// ModelPinEnv is the ambient override a driver sets to force every trial in a metered behavioral
// matrix onto one model, regardless of what tier its caller requested -- the same pattern
// characterize.ModelPinEnv establishes for Phase 1, carried forward here as this package's own
// property for Phase 3.
const ModelPinEnv = "PLUGIN_BEHAVIORAL_MODEL_PIN"

// ResolveModel resolves the model a trial actually runs against. requested is the caller's own
// tier parameter -- every metered entry point in this package takes it as an argument, never a
// literal baked into call logic -- and ModelPinEnv, when set, always wins over it.
func ResolveModel(requested string) string {
	if pinned := strings.TrimSpace(os.Getenv(ModelPinEnv)); pinned != "" {
		return pinned
	}
	return requested
}
