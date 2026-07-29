package plugin_conform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	shschema "github.com/johnrichter/claude-shared-tooling/go/schema"
)

func mustPass(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRunValidPluginPassesAndCalibrates proves the deterministic tier's happy path end to end: a
// plugin with well-formed frontmatter, a well-formed hooks.json, a matcher that fires on its own
// declared alternatives and stays silent on a control token, and a rule glob that resolves --
// runs at $0, reports zero errors, and clears the calibration gate.
func TestRunValidPluginPassesAndCalibrates(t *testing.T) {
	report, err := Run(Options{PluginDir: "testdata/valid-plugin", PluginName: "valid-plugin"})
	mustPass(t, err)

	if !report.Passed() {
		t.Fatalf("expected a clean report, got %d error(s): %+v", len(report.Errors), report.Errors)
	}
	if err := Calibrate(report); err != nil {
		t.Fatalf("expected calibration to pass, got: %v", err)
	}
}

// TestRunInvalidPluginFailsAndBlocksCalibration proves the planted-invalid fixture is caught --
// an empty description, an overbroad ".*" matcher, and a rule glob resolving to zero files each
// surface as their own error -- and that a failing report blocks the calibration gate rather
// than being silently absorbed.
func TestRunInvalidPluginFailsAndBlocksCalibration(t *testing.T) {
	report, err := Run(Options{PluginDir: "testdata/invalid-plugin", PluginName: "invalid-plugin"})
	mustPass(t, err)

	if report.Passed() {
		t.Fatalf("expected planted defects to fail conformance, got a clean report")
	}

	wantCodes := map[string]bool{
		"gate_negative.plugin_conform.frontmatter_description_missing": false,
		"gate_negative.plugin_conform.hook_matcher_overbroad":          false,
		"gate_negative.plugin_conform.rule_glob_no_match":              false,
	}
	for _, d := range report.Errors {
		if _, want := wantCodes[d.Code]; want {
			wantCodes[d.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Errorf("expected a %q diagnostic, none found in %+v", code, report.Errors)
		}
	}

	calErr := Calibrate(report)
	if calErr == nil {
		t.Fatalf("expected calibration to block on a failing report")
	}
	var blocked *CalibrationBlockedError
	if !errors.As(calErr, &blocked) {
		t.Fatalf("expected a *CalibrationBlockedError, got %T: %v", calErr, calErr)
	}
	if blocked.Report != report {
		t.Fatalf("expected the blocked error to carry the same report instance")
	}
}

// TestCheckMatcherFiresPlainAlternationFullyChecked proves a well-formed "tool|tool" matcher
// clears both checks with no caveat: it does not fire on the control token, and its shape is
// fully accounted for.
func TestCheckMatcherFiresPlainAlternationFullyChecked(t *testing.T) {
	hooks := &HooksFile{Hooks: map[string][]HookBinding{
		"PreToolUse": {{Matcher: "Write|Edit"}},
	}}
	errs, caveats, err := CheckMatcherFires(hooks)
	mustPass(t, err)
	if len(errs) != 0 {
		t.Fatalf("expected a well-scoped matcher to pass clean, got %+v", errs)
	}
	if len(caveats) != 0 {
		t.Fatalf("expected no caveat for a plain literal alternation, got %+v", caveats)
	}
}

// TestCheckMatcherFiresComplexPatternCaveats proves a matcher outside the plain-alternation
// shape that is not itself overbroad still surfaces the "not fully accounted for" caveat rather
// than a silent, false-clean pass.
func TestCheckMatcherFiresComplexPatternCaveats(t *testing.T) {
	hooks := &HooksFile{Hooks: map[string][]HookBinding{
		"PreToolUse": {{Matcher: "^Write$|^Not[A-Z][a-z]+$"}},
	}}
	errs, caveats, err := CheckMatcherFires(hooks)
	mustPass(t, err)
	if len(errs) != 0 {
		t.Fatalf("expected no error for a non-overbroad complex pattern, got %+v", errs)
	}
	if len(caveats) != 1 || caveats[0].Code != "caveats.plugin_conform.hook_matcher_complex_pattern" {
		t.Fatalf("expected exactly one complex-pattern caveat, got %+v", caveats)
	}
}

// TestCheckLauncherOnPathMissing proves the launcher-on-PATH check fires on a name the injected
// resolver cannot find, without depending on the test host's real PATH.
func TestCheckLauncherOnPathMissing(t *testing.T) {
	lookPath := func(name string) (string, error) {
		return "", errors.New("not found")
	}
	diags, err := CheckLauncherOnPath([]string{"never-installed-tool"}, lookPath)
	mustPass(t, err)
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.launcher_not_on_path" {
		t.Fatalf("expected exactly one launcher_not_on_path diagnostic, got %+v", diags)
	}
}

// TestCheckAddDirsTrackedCatchesUntrackedDir proves the add-dirs-tracked check reports a
// required directory declared in a user-scope file but absent from the tracked one -- the real
// J3-shaped defect (a fresh clone attaches zero KBs) this check exists to catch -- and passes
// clean once the tracked file declares it.
func TestCheckAddDirsTrackedCatchesUntrackedDir(t *testing.T) {
	tracked := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(tracked, []byte(`{"permissions":{"additionalDirectories":[]}}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	diags, err := CheckAddDirsTracked([]string{"/kb/one"}, tracked)
	mustPass(t, err)
	if len(diags) != 1 || diags[0].Code != "gate_negative.plugin_conform.add_dir_untracked" {
		t.Fatalf("expected exactly one add_dir_untracked diagnostic, got %+v", diags)
	}

	if err := os.WriteFile(tracked, []byte(`{"permissions":{"additionalDirectories":["/kb/one"]}}`), 0o644); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	diags, err = CheckAddDirsTracked([]string{"/kb/one"}, tracked)
	mustPass(t, err)
	if len(diags) != 0 {
		t.Fatalf("expected a clean pass once the directory is tracked, got %+v", diags)
	}
}

// TestCheckManifestSchemaValidatesAgainstInjectedSchema proves CheckManifestSchema validates
// plugin.json against a schema the caller supplies at runtime -- never one this package bundles
// -- matching go/schema's own never-bake-in-a-schema contract.
func TestCheckManifestSchemaValidatesAgainstInjectedSchema(t *testing.T) {
	requireVersion := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"required": ["name", "version"]
	}`)
	compiled, err := shschema.Compile("plugin-conform-test/plugin-manifest", requireVersion)
	mustPass(t, err)

	diags, err := CheckManifestSchema("testdata/invalid-plugin", compiled)
	mustPass(t, err)
	if len(diags) != 0 {
		t.Fatalf("expected the fixture's own plugin.json to satisfy the injected schema, got %+v", diags)
	}

	requireMissingField := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"required": ["name", "a-field-no-fixture-declares"]
	}`)
	strict, err := shschema.Compile("plugin-conform-test/plugin-manifest-strict", requireMissingField)
	mustPass(t, err)

	diags, err = CheckManifestSchema("testdata/invalid-plugin", strict)
	mustPass(t, err)
	if len(diags) == 0 {
		t.Fatalf("expected a schema violation for a required field the fixture never declares")
	}
}
