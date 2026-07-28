package plugin_conform

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// trackedSettings is the minimal shape CheckAddDirsTracked reads from a Claude Code
// settings.json file -- only the one field this check needs.
type trackedSettings struct {
	Permissions struct {
		AdditionalDirectories []string `json:"additionalDirectories"`
	} `json:"permissions"`
}

// CheckAddDirsTracked requires every directory in required to appear in trackedSettingsPath's
// own permissions.additionalDirectories. trackedSettingsPath is the file the caller has
// confirmed is git-tracked -- this check reads only that one path and does not itself inspect
// git status. A required directory declared only in a user-scope or .local settings file is
// reported exactly as if it were absent from the tracked one, which is the real failure this
// check exists to catch: a fresh clone that never receives that untracked file attaches zero of
// the directories the plugin depends on.
func CheckAddDirsTracked(required []string, trackedSettingsPath string) ([]clikit.Diagnostic, error) {
	if len(required) == 0 {
		return nil, nil
	}

	data, err := os.ReadFile(trackedSettingsPath)
	if os.IsNotExist(err) {
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.tracked_settings_missing",
			oneLine(fmt.Sprintf("tracked settings file %q does not exist -- none of this plugin's required additionalDirectories can be tracked", trackedSettingsPath)),
			clikit.Manual("create "+trackedSettingsPath+" and declare the required additionalDirectories in its permissions.additionalDirectories"),
			map[string]any{"path": trackedSettingsPath},
		)
		if buildErr != nil {
			return nil, buildErr
		}
		return []clikit.Diagnostic{d}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugin-conform: read %q: %w", trackedSettingsPath, err)
	}

	var settings trackedSettings
	if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.tracked_settings_malformed",
			oneLine(fmt.Sprintf("tracked settings file %q is not well-formed JSON: %v", trackedSettingsPath, jsonErr)),
			clikit.Manual("fix "+trackedSettingsPath+" so it parses"),
			map[string]any{"path": trackedSettingsPath},
		)
		if buildErr != nil {
			return nil, buildErr
		}
		return []clikit.Diagnostic{d}, nil
	}

	tracked := make(map[string]bool, len(settings.Permissions.AdditionalDirectories))
	for _, d := range settings.Permissions.AdditionalDirectories {
		tracked[expandTilde(d)] = true
	}

	var diags []clikit.Diagnostic
	for _, dir := range required {
		if tracked[expandTilde(dir)] {
			continue
		}
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.add_dir_untracked",
			oneLine(fmt.Sprintf("required additionalDirectory %q is not declared in the tracked settings file %q", dir, trackedSettingsPath)),
			clikit.Manual("add "+dir+" to permissions.additionalDirectories in "+trackedSettingsPath),
			map[string]any{"path": dir},
		)
		if buildErr != nil {
			return nil, buildErr
		}
		diags = append(diags, d)
	}
	return diags, nil
}
