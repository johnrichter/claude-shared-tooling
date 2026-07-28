package plugin_conform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// hooksJSONPath is where a plugin's own hooks.json lives, relative to its root, per Claude
// Code's default (unconfigured) hooks location.
const hooksJSONPath = "hooks/hooks.json"

// HookAction is one hook binding's own invoked step: a command with its own argv, per Claude
// Code's hooks.json contract.
type HookAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookBinding is one entry under a hooks.json event: an optional matcher scoping which tool
// invocations it fires for, and the actions it runs when it does. Matcher is empty for an event
// with no matcher concept (e.g. SessionStart) and "*" for an explicit match-everything binding.
type HookBinding struct {
	Matcher string       `json:"matcher,omitempty"`
	Hooks   []HookAction `json:"hooks"`
}

// HooksFile is one plugin's hooks.json, keyed by Claude Code hook event name.
type HooksFile struct {
	Hooks map[string][]HookBinding `json:"hooks"`
}

// sortedEvents returns hooks' event names in a fixed, sorted order -- map iteration order is
// not stable in Go, and this package's diagnostics must come out in the same order on every run
// against the same fixture.
func sortedEvents(hooks map[string][]HookBinding) []string {
	events := make([]string, 0, len(hooks))
	for e := range hooks {
		events = append(events, e)
	}
	sort.Strings(events)
	return events
}

// CheckHooksWellFormed reads pluginDir's hooks.json and validates its shape: valid JSON, a
// top-level "hooks" object, and every binding's every action carrying a non-empty type and
// command. A plugin with no hooks.json returns a nil file and no diagnostics -- shipping no
// hooks is not itself a conformance failure.
func CheckHooksWellFormed(pluginDir string) (*HooksFile, []clikit.Diagnostic, error) {
	data, err := os.ReadFile(filepath.Join(pluginDir, hooksJSONPath))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("plugin-conform: read %s: %w", hooksJSONPath, err)
	}

	var hooks HooksFile
	if jsonErr := json.Unmarshal(data, &hooks); jsonErr != nil {
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.hooks_json_malformed",
			oneLine(fmt.Sprintf("%s is not well-formed: %v", hooksJSONPath, jsonErr)),
			clikit.Manual("fix "+hooksJSONPath+" so it parses as the hooks.json contract"),
			map[string]any{"path": hooksJSONPath},
		)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		return nil, []clikit.Diagnostic{d}, nil
	}

	if hooks.Hooks == nil {
		d, buildErr := clikit.NewError(
			"gate_negative.plugin_conform.hooks_json_empty",
			oneLine(hooksJSONPath+" has no top-level \"hooks\" object"),
			clikit.Manual("add a top-level \"hooks\" object to "+hooksJSONPath+", or remove the file if this plugin ships no hooks"),
			map[string]any{"path": hooksJSONPath},
		)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		return &hooks, []clikit.Diagnostic{d}, nil
	}

	var diags []clikit.Diagnostic
	for _, event := range sortedEvents(hooks.Hooks) {
		for i, b := range hooks.Hooks[event] {
			if len(b.Hooks) == 0 {
				d, buildErr := clikit.NewError(
					"gate_negative.plugin_conform.hooks_json_binding_empty",
					oneLine(fmt.Sprintf("%s: %s binding [%d] declares no hooks to run", hooksJSONPath, event, i)),
					clikit.Manual("add at least one hook action, or remove the empty binding, in "+hooksJSONPath),
					map[string]any{"path": hooksJSONPath, "event": event},
				)
				if buildErr != nil {
					return nil, nil, buildErr
				}
				diags = append(diags, d)
				continue
			}
			for j, action := range b.Hooks {
				if action.Type == "" || action.Command == "" {
					d, buildErr := clikit.NewError(
						"gate_negative.plugin_conform.hooks_json_action_incomplete",
						oneLine(fmt.Sprintf("%s: %s binding [%d] action [%d] is missing type or command", hooksJSONPath, event, i, j)),
						clikit.Manual("give every hook action a non-empty type and command in "+hooksJSONPath),
						map[string]any{"path": hooksJSONPath, "event": event},
					)
					if buildErr != nil {
						return nil, nil, buildErr
					}
					diags = append(diags, d)
				}
			}
		}
	}
	return &hooks, diags, nil
}
