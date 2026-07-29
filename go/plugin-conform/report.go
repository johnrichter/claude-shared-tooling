package plugin_conform

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/schema"
)

// Options configures one deterministic-conformance Run over a single plugin.
type Options struct {
	// PluginDir is the plugin's own checkout root -- every check reads only files inside it.
	PluginDir string
	// PluginName is the plugin's own declared name, carried on Report for a caller's own
	// reporting. It is never read from disk by this package.
	PluginName string
	// ManifestSchema, when non-nil, is the schema CheckManifestSchema validates plugin.json
	// against. A nil schema skips validation but still requires plugin.json, when present, to
	// be well-formed JSON.
	ManifestSchema *schema.Schema
	// RequiredLaunchers are the external CLI base names this plugin depends on resolving on
	// PATH.
	RequiredLaunchers []string
	// RequiredAdditionalDirs are the additionalDirectories this plugin requires a consumer to
	// have registered.
	RequiredAdditionalDirs []string
	// TrackedSettingsPath is the git-tracked settings.json RequiredAdditionalDirs is checked
	// against. Required when RequiredAdditionalDirs is non-empty; ignored otherwise.
	TrackedSettingsPath string
	// LookPath resolves a launcher name to its PATH location; defaults to exec.LookPath.
	LookPath PathResolver
}

// Report is one plugin's deterministic-conformance outcome: every check's findings, split the
// same way a clikit result splits them -- Errors block, Caveats qualify without blocking.
type Report struct {
	PluginName string
	PluginDir  string
	Errors     []clikit.Diagnostic
	Caveats    []clikit.Diagnostic
}

// Passed reports whether r has zero errors. A report carrying only caveats still passes: a
// caveat qualifies the result, it does not block it -- the same split clikit.NewCaveats draws.
func (r *Report) Passed() bool { return len(r.Errors) == 0 }

// Run executes all five deterministic-conformance checks against opts.PluginDir and returns
// their combined Report. Every check is a static read of the plugin's own files; none of them
// invokes a model or spends money.
func Run(opts Options) (*Report, error) {
	info, err := os.Stat(opts.PluginDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("plugin-conform: plugin-dir %q does not resolve to a real directory", opts.PluginDir)
	}

	report := &Report{PluginName: opts.PluginName, PluginDir: opts.PluginDir}

	diags, err := CheckManifestSchema(opts.PluginDir, opts.ManifestSchema)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	diags, err = CheckComponentFrontmatter(opts.PluginDir)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	hooks, diags, err := CheckHooksWellFormed(opts.PluginDir)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	diags, err = CheckRuleGlobs(opts.PluginDir)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	errs, caveats, err := CheckMatcherFires(hooks)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, errs...)
	report.Caveats = append(report.Caveats, caveats...)

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	diags, err = CheckLauncherOnPath(opts.RequiredLaunchers, lookPath)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	diags, err = CheckAddDirsTracked(opts.RequiredAdditionalDirs, opts.TrackedSettingsPath)
	if err != nil {
		return nil, err
	}
	report.Errors = append(report.Errors, diags...)

	return report, nil
}
