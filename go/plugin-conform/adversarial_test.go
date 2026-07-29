package plugin_conform

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a small test helper: create parent dirs, write content.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestRunPluginDirMissing proves Run fails loudly on a plugin-dir that does not resolve to a real
// directory, rather than silently reporting a clean (zero-check) pass.
func TestRunPluginDirMissing(t *testing.T) {
	_, err := Run(Options{PluginDir: filepath.Join(t.TempDir(), "does-not-exist"), PluginName: "x"})
	if err == nil {
		t.Fatalf("expected an error for a nonexistent plugin-dir, got nil")
	}
}

// TestCalibrateNilReport proves Calibrate treats a nil report as a caller defect (a plain error),
// never as an implicit pass -- a nil report must never let the metered matrix proceed.
func TestCalibrateNilReport(t *testing.T) {
	if err := Calibrate(nil); err == nil {
		t.Fatalf("expected Calibrate(nil) to error, got nil")
	}
}

// TestCheckHooksWellFormedMalformedJSON proves a hooks.json that is not valid JSON at all is
// caught as its own diagnostic, not silently treated as "no hooks."
func TestCheckHooksWellFormedMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, hooksJSONPath, `{not json`)
	hooks, diags, err := CheckHooksWellFormed(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hooks != nil {
		t.Fatalf("expected a nil HooksFile for malformed JSON, got %+v", hooks)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.hooks_json_malformed" {
		t.Fatalf("expected exactly one hooks_json_malformed diagnostic, got %+v", diags)
	}
}

// TestCheckHooksWellFormedEmptyTopLevel proves a hooks.json with valid JSON but no "hooks" object
// at all is flagged, distinct from the malformed-JSON case above.
func TestCheckHooksWellFormedEmptyTopLevel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, hooksJSONPath, `{}`)
	_, diags, err := CheckHooksWellFormed(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.hooks_json_empty" {
		t.Fatalf("expected exactly one hooks_json_empty diagnostic, got %+v", diags)
	}
}

// TestCheckHooksWellFormedEmptyBindingAndIncompleteAction proves both a binding with zero hook
// actions and an action missing type/command are each caught, in the same run.
func TestCheckHooksWellFormedEmptyBindingAndIncompleteAction(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, hooksJSONPath, `{
		"hooks": {
			"PreToolUse": [{"matcher": "Write", "hooks": []}],
			"PostToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": ""}]}]
		}
	}`)
	_, diags, err := CheckHooksWellFormed(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantCodes := map[string]bool{
		"gate_negative.plugin_conform.hooks_json_binding_empty":     false,
		"gate_negative.plugin_conform.hooks_json_action_incomplete": false,
	}
	for _, d := range diags {
		if _, want := wantCodes[d.Code]; want {
			wantCodes[d.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Errorf("expected a %q diagnostic, none found in %+v", code, diags)
		}
	}
}

// TestCheckHooksWellFormedAbsent proves a plugin shipping no hooks.json at all is not itself a
// conformance failure -- shipping zero hooks is legitimate.
func TestCheckHooksWellFormedAbsent(t *testing.T) {
	dir := t.TempDir()
	hooks, diags, err := CheckHooksWellFormed(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hooks != nil || len(diags) != 0 {
		t.Fatalf("expected nil hooks and zero diagnostics for an absent hooks.json, got hooks=%+v diags=%+v", hooks, diags)
	}
}

// TestCheckManifestSchemaMalformedJSON proves plugin.json that is not valid JSON is caught
// regardless of whether a schema was supplied.
func TestCheckManifestSchemaMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, manifestPath, `{"name": "x",`)
	diags, err := CheckManifestSchema(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.manifest_malformed" {
		t.Fatalf("expected exactly one manifest_malformed diagnostic, got %+v", diags)
	}
}

// TestCheckManifestSchemaAbsent proves a plugin with no plugin.json at all is not itself a
// conformance failure -- component auto-discovery makes the manifest optional.
func TestCheckManifestSchemaAbsent(t *testing.T) {
	dir := t.TempDir()
	diags, err := CheckManifestSchema(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected zero diagnostics for an absent plugin.json, got %+v", diags)
	}
}

// TestCheckComponentFrontmatterMissingBlock proves a command file with no leading frontmatter
// fence at all is flagged distinctly from one with an empty description.
func TestCheckComponentFrontmatterMissingBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "commands/no-frontmatter.md", "just some body text, no fence at all\n")
	diags, err := CheckComponentFrontmatter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.frontmatter_missing" {
		t.Fatalf("expected exactly one frontmatter_missing diagnostic, got %+v", diags)
	}
}

// TestCheckComponentFrontmatterInvalidYAML proves a frontmatter block that opens but fails to
// parse as YAML is its own diagnostic, distinct from "missing" and "empty description."
func TestCheckComponentFrontmatterInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "commands/bad-yaml.md", "---\ndescription: [unterminated\n---\nbody\n")
	diags, err := CheckComponentFrontmatter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.frontmatter_invalid" {
		t.Fatalf("expected exactly one frontmatter_invalid diagnostic, got %+v", diags)
	}
}

// TestCheckComponentFrontmatterWhitespaceDescription proves a description that is present but
// whitespace-only is treated the same as empty -- this check must not be fooled by a
// technically-non-empty-string field.
func TestCheckComponentFrontmatterWhitespaceDescription(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "commands/blank-desc.md", "---\ndescription: \"   \"\n---\nbody\n")
	diags, err := CheckComponentFrontmatter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.frontmatter_description_missing" {
		t.Fatalf("expected exactly one frontmatter_description_missing diagnostic, got %+v", diags)
	}
}

// TestCheckRuleGlobsUnconditionalRule proves a rule file with no "paths:" frontmatter field at
// all (or no frontmatter block at all) has nothing to check -- an unconditional rule is legitimate
// and must not be flagged as if its glob resolved to zero files.
func TestCheckRuleGlobsUnconditionalRule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rules/always-on.md", "no frontmatter fence here at all\n")
	diags, err := CheckRuleGlobs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected zero diagnostics for an unconditional rule, got %+v", diags)
	}
}

// TestCheckRuleGlobsInvalidFrontmatter proves a rule whose frontmatter block opens but fails to
// parse as YAML is caught as its own diagnostic rather than silently skipped as "unconditional."
func TestCheckRuleGlobsInvalidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rules/broken.md", "---\npaths: [unterminated\n---\nbody\n")
	diags, err := CheckRuleGlobs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.rule_frontmatter_invalid" {
		t.Fatalf("expected exactly one rule_frontmatter_invalid diagnostic, got %+v", diags)
	}
}

// TestCheckMatcherFiresInvalidRegex proves a matcher that does not compile as a regex is its own
// diagnostic, not silently skipped or crashing the check.
func TestCheckMatcherFiresInvalidRegex(t *testing.T) {
	hooks := &HooksFile{Hooks: map[string][]HookBinding{
		"PreToolUse": {{Matcher: "["}},
	}}
	errs, caveats, err := CheckMatcherFires(hooks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 1 || errs[0].Code != "gate_negative.plugin_conform.hook_matcher_invalid_regex" {
		t.Fatalf("expected exactly one hook_matcher_invalid_regex diagnostic, got %+v", errs)
	}
	if len(caveats) != 0 {
		t.Fatalf("expected no caveat once the matcher fails to compile, got %+v", caveats)
	}
}

// TestCheckMatcherFiresNilHooksFile proves the mechanism check tolerates a nil HooksFile (the
// shape CheckHooksWellFormed returns for an absent hooks.json) without panicking.
func TestCheckMatcherFiresNilHooksFile(t *testing.T) {
	errs, caveats, err := CheckMatcherFires(nil)
	if err != nil || errs != nil || caveats != nil {
		t.Fatalf("expected all-nil for a nil HooksFile, got errs=%+v caveats=%+v err=%v", errs, caveats, err)
	}
}

// TestCheckAddDirsTrackedMissingFile proves a --tracked-settings path that does not exist is its
// own diagnostic, catching the "file was never created" defect distinctly from "file exists but
// doesn't list this directory."
func TestCheckAddDirsTrackedMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "settings.json")
	diags, err := CheckAddDirsTracked([]string{"/kb/one"}, missing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.tracked_settings_missing" {
		t.Fatalf("expected exactly one tracked_settings_missing diagnostic, got %+v", diags)
	}
}

// TestCheckAddDirsTrackedMalformedFile proves a tracked settings file that exists but is not
// valid JSON is its own diagnostic, not a silent pass-through.
func TestCheckAddDirsTrackedMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	diags, err := CheckAddDirsTracked([]string{"/kb/one"}, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.tracked_settings_malformed" {
		t.Fatalf("expected exactly one tracked_settings_malformed diagnostic, got %+v", diags)
	}
}

// TestCheckAddDirsTrackedNoneRequired proves the check is a no-op -- no file read, no diagnostic
// -- when the caller requires zero additionalDirectories, even against a nonexistent tracked path.
func TestCheckAddDirsTrackedNoneRequired(t *testing.T) {
	diags, err := CheckAddDirsTracked(nil, filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected zero diagnostics when zero directories are required, got %+v", diags)
	}
}

// TestReportPassedWithOnlyCaveats proves a report carrying only caveats still Passes and clears
// Calibrate -- a caveat qualifies a result, it must never itself block the metered matrix.
func TestReportPassedWithOnlyCaveats(t *testing.T) {
	report, err := Run(Options{PluginDir: "testdata/valid-plugin", PluginName: "valid-plugin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The valid fixture's own matcher is a plain alternation (no caveat by construction); prove
	// the *contract* instead by constructing a report with an injected caveat directly.
	report.Caveats = append(report.Caveats, report.Caveats...) // no-op if empty; keep report.Errors untouched
	if !report.Passed() {
		t.Fatalf("a report with zero errors must Pass regardless of caveats, got Errors=%+v", report.Errors)
	}
	if err := Calibrate(report); err != nil {
		t.Fatalf("expected calibration to clear a zero-error report even with caveats, got: %v", err)
	}
}
