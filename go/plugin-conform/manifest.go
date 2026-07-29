package plugin_conform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/schema"
)

// manifestPath is where a plugin's own manifest lives, relative to its root.
const manifestPath = ".claude-plugin/plugin.json"

// CheckManifestSchema parses pluginDir's plugin.json as JSON and, when pluginSchema is non-nil,
// validates it against that caller-supplied schema -- never a schema baked into this package
// (the same never-bundle-a-schema contract go/schema itself keeps). A plugin with no plugin.json
// returns no diagnostics: Claude Code's component auto-discovery makes the manifest optional, so
// its absence is not a conformance defect by itself.
func CheckManifestSchema(pluginDir string, pluginSchema *schema.Schema) ([]clikit.Diagnostic, error) {
	data, err := os.ReadFile(filepath.Join(pluginDir, manifestPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugin-conform: read %s: %w", manifestPath, err)
	}

	var doc any
	if jsonErr := json.Unmarshal(data, &doc); jsonErr != nil {
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.manifest_malformed",
			oneLine(fmt.Sprintf("%s is not well-formed JSON: %v", manifestPath, jsonErr)),
			clikit.Manual("fix "+manifestPath+" so it parses as JSON"),
			map[string]any{"path": manifestPath},
		)
		if buildErr != nil {
			return nil, buildErr
		}
		return []clikit.Diagnostic{d}, nil
	}

	if pluginSchema == nil {
		return nil, nil
	}
	return schema.Validate(pluginSchema, doc)
}
