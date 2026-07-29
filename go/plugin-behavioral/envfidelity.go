package plugin_behavioral

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// PluginInactiveError reports a metered matrix refusing to grade any trial because the
// plugin-under-test is not active in the environment a live probe will actually run in -- M5's
// #1 lesson: a project-scoped plugin does not load in an isolated probe unless it is explicitly
// wired in, and silently grading against an inactive plugin measures nothing.
type PluginInactiveError struct {
	PluginKey           string
	TrackedSettingsPath string
}

func (e *PluginInactiveError) Error() string {
	return fmt.Sprintf("plugin-behavioral: plugin-under-test %q is not enabled in %q -- refusing to grade any trial against it", e.PluginKey, e.TrackedSettingsPath)
}

type enabledPluginsSettings struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

// AssertPluginActive fails loud unless pluginKey (e.g. "my-plugin@my-marketplace") reads true
// from trackedSettingsPath's own enabledPlugins map -- the one tracked settings file a fresh
// clone actually receives, so a plugin wired only into a user-scope or untracked settings file is
// reported exactly as if it were absent.
func AssertPluginActive(pluginKey, trackedSettingsPath string) error {
	data, err := os.ReadFile(trackedSettingsPath)
	if err != nil {
		return fmt.Errorf("plugin-behavioral: read tracked settings %q: %w", trackedSettingsPath, err)
	}
	var settings enabledPluginsSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("plugin-behavioral: tracked settings %q is not well-formed JSON: %w", trackedSettingsPath, err)
	}
	if settings.EnabledPlugins[pluginKey] {
		return nil
	}
	return &PluginInactiveError{PluginKey: pluginKey, TrackedSettingsPath: trackedSettingsPath}
}

// CCVersionMismatchError reports a live run whose observed Claude Code version does not match
// the version this matrix pinned -- M5's own precedent: the transcript schema is internal and
// version-volatile, so a behavioral verdict is only trustworthy against a named, confirmed
// version.
type CCVersionMismatchError struct {
	Want string
	Got  string
}

func (e *CCVersionMismatchError) Error() string {
	return fmt.Sprintf("plugin-behavioral: Claude Code version pin mismatch: want %q, got %q", e.Want, e.Got)
}

// VersionResolver reports the Claude Code version currently on PATH -- ResolveInstalledCCVersion
// in production, a fixture-backed fake in a test.
type VersionResolver func(ctx context.Context) (string, error)

// ResolveInstalledCCVersion is the production VersionResolver: runs `claude --version` and
// returns its first whitespace-delimited token, the version number in Claude Code's own
// `--version` output shape.
func ResolveInstalledCCVersion(ctx context.Context) (string, error) {
	res, err := sysops.Run(ctx, "claude", []string{"--version"}, sysops.Options{})
	if err != nil {
		return "", fmt.Errorf("plugin-behavioral: run claude --version: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("plugin-behavioral: claude --version exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	fields := strings.Fields(string(res.Stdout))
	if len(fields) == 0 {
		return "", fmt.Errorf("plugin-behavioral: claude --version produced no output")
	}
	return fields[0], nil
}

// AssertCCVersionPinned fails loud unless resolve reports exactly want, so a metered matrix never
// runs silently against a Claude Code release its own classifiers were never validated against.
func AssertCCVersionPinned(ctx context.Context, want string, resolve VersionResolver) error {
	if strings.TrimSpace(want) == "" {
		return fmt.Errorf("plugin-behavioral: a pinned Claude Code version is required, got empty")
	}
	got, err := resolve(ctx)
	if err != nil {
		return fmt.Errorf("plugin-behavioral: resolve Claude Code version: %w", err)
	}
	got = strings.TrimSpace(got)
	if got != strings.TrimSpace(want) {
		return &CCVersionMismatchError{Want: want, Got: got}
	}
	return nil
}
