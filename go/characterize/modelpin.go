package characterize

import (
	"os"
	"strings"
)

// ModelPinEnv is the ambient override a driver sets to force every metered characterizing run
// onto one model, regardless of what tier its caller requested -- e.g. pinning a whole batch of
// runs to a cheaper tier for a dry pass, with no call site edited.
const ModelPinEnv = "CHARACTERIZE_MODEL_PIN"

// ResolveModel resolves the model tier a metered run actually uses. requested is the caller's
// own tier parameter -- every metered entry point in this package takes it as an argument, never
// as a literal baked into this package's call logic -- and ModelPinEnv, when set, always wins
// over it. A driver therefore has exactly one place to force a tier from outside the call graph,
// instead of threading an override through every characterizing call.
func ResolveModel(requested string) string {
	if pinned := strings.TrimSpace(os.Getenv(ModelPinEnv)); pinned != "" {
		return pinned
	}
	return requested
}
