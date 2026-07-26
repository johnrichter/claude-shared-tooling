package gate

import (
	"bytes"
	"os"
	"testing"
)

// canonicalRegistryPath is the source of record this package's embedded
// invariant-registry.json is a mechanical copy of (go:embed cannot reach outside its package
// directory). Skips rather than fails when unreachable, so this package stays portable if
// extracted to its own module.
const canonicalRegistryPath = "../../schemas/invariant-registry/invariant-registry.json"

// TestEmbeddedRegistryMatchesCanonicalSource guards against drift between the embedded copy
// and the canonical registry source, skipping when the source is unreachable.
func TestEmbeddedRegistryMatchesCanonicalSource(t *testing.T) {
	canonical, err := os.ReadFile(canonicalRegistryPath)
	if err != nil {
		t.Skipf("canonical registry not found at %s (skipping drift guard): %v", canonicalRegistryPath, err)
	}
	if !bytes.Equal(canonical, embeddedRegistryJSON) {
		t.Errorf("go/gate/invariant-registry.json has drifted from %s — recopy the canonical file", canonicalRegistryPath)
	}
}
