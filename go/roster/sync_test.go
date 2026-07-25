package roster

import (
	"bytes"
	"os"
	"testing"
)

// canonicalRosterPath is the source of record this package's embedded model-roster.json is a
// mechanical copy of (go:embed cannot reach outside its package directory). Skips rather than
// fails when unreachable, so this package stays portable if extracted to its own module.
const canonicalRosterPath = "../../schemas/model-roster/model-roster.json"

func TestEmbeddedCopyMatchesCanonicalSource(t *testing.T) {
	canonical, err := os.ReadFile(canonicalRosterPath)
	if err != nil {
		t.Skipf("canonical roster not found at %s (skipping drift guard): %v", canonicalRosterPath, err)
	}
	if !bytes.Equal(canonical, embeddedRosterJSON) {
		t.Errorf("go/roster/model-roster.json has drifted from %s — recopy the canonical file", canonicalRosterPath)
	}
}
