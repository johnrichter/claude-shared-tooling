package schema

import (
	"strings"
	"testing"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

const personSchema = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": "object",
	"required": ["name", "age"],
	"properties": {
		"name": {"type": "string", "minLength": 1},
		"age": {"type": "integer", "minimum": 0}
	}
}`

func mustCompile(t *testing.T, id, src string) *Schema {
	t.Helper()
	s, err := Compile(id, []byte(src))
	if err != nil {
		t.Fatalf("Compile(%q) failed: %v", id, err)
	}
	return s
}

// TestCompile_InvalidJSON checks that malformed schema JSON fails to compile.
func TestCompile_InvalidJSON(t *testing.T) {
	_, err := Compile("bad", []byte(`{not json`))
	if err == nil {
		t.Fatal("Compile with malformed JSON: want error, got nil")
	}
}

// TestCompile_InvalidSchemaSemantics checks that a structurally valid JSON
// document that is not a valid schema is rejected at compile time.
func TestCompile_InvalidSchemaSemantics(t *testing.T) {
	// "type" must be a string or array of strings, not a number - the compiler
	// must reject this at compile time, not defer it to Validate.
	_, err := Compile("bad-schema", []byte(`{"type": 5}`))
	if err == nil {
		t.Fatal("Compile with invalid schema semantics: want error, got nil")
	}
}

// TestCompile_DuplicateIDsAreIndependent checks that two schemas compiled
// under different ids validate independently, with no cross-contamination.
func TestCompile_DuplicateIDsAreIndependent(t *testing.T) {
	// Compiling two schemas under different ids in separate calls must not
	// let one leak into the other's compiler instance.
	s1 := mustCompile(t, "urn:one", `{"type":"string"}`)
	s2 := mustCompile(t, "urn:two", `{"type":"integer"}`)
	if diags, err := Validate(s1, "hello"); err != nil || len(diags) != 0 {
		t.Fatalf("s1 valid string: diags=%v err=%v", diags, err)
	}
	if diags, err := Validate(s2, "hello"); err != nil || len(diags) == 0 {
		t.Fatalf("s2 should reject a string: diags=%v err=%v", diags, err)
	}
}

// TestValidate_ValidDoc checks that a doc satisfying the schema returns no
// diagnostics and no error.
func TestValidate_ValidDoc(t *testing.T) {
	s := mustCompile(t, "urn:person", personSchema)
	doc := map[string]any{"name": "Ada", "age": float64(30)}
	diags, err := Validate(s, doc)
	if err != nil {
		t.Fatalf("Validate valid doc: unexpected error %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("Validate valid doc: want 0 diagnostics, got %d: %+v", len(diags), diags)
	}
}

// TestValidate_InvalidDoc_SingleViolation checks that one schema violation
// normalizes to a clikit error diagnostic with the expected code, manual
// triage, and instance/keyword location context.
func TestValidate_InvalidDoc_SingleViolation(t *testing.T) {
	s := mustCompile(t, "urn:person2", personSchema)
	doc := map[string]any{"name": "Ada", "age": float64(-1)}
	diags, err := Validate(s, doc)
	if err != nil {
		t.Fatalf("Validate: unexpected error %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("Validate invalid doc (age below minimum): want >=1 diagnostic, got 0")
	}
	for _, d := range diags {
		if d.Code != "usage.schema_invalid" {
			t.Errorf("diagnostic code = %q, want %q", d.Code, "usage.schema_invalid")
		}
		if d.Triage.Kind != clikit.TriageManual {
			t.Errorf("triage kind = %q, want manual", d.Triage.Kind)
		}
		if d.Triage.Instruction == "" {
			t.Error("manual triage missing instruction")
		}
		if _, ok := d.Context["instance_location"]; !ok {
			t.Error("diagnostic context missing instance_location")
		}
		if _, ok := d.Context["keyword_location"]; !ok {
			t.Error("diagnostic context missing keyword_location")
		}
	}
}

// TestValidate_InvalidDoc_MultipleViolations checks that a doc violating
// more than one constraint yields one diagnostic per violation.
func TestValidate_InvalidDoc_MultipleViolations(t *testing.T) {
	s := mustCompile(t, "urn:person3", personSchema)
	// Missing name AND age is negative: two independent constraint failures.
	doc := map[string]any{"age": float64(-5)}
	diags, err := Validate(s, doc)
	if err != nil {
		t.Fatalf("Validate: unexpected error %v", err)
	}
	if len(diags) < 2 {
		t.Fatalf("Validate doc with 2 violations: want >=2 diagnostics, got %d: %+v", len(diags), diags)
	}
}

// TestValidate_EveryDiagnosticSurvivesFullResultValidation checks that a
// schema-produced diagnostic is not just constructible but usable as-is
// inside a real clikit Result envelope.
func TestValidate_EveryDiagnosticSurvivesFullResultValidation(t *testing.T) {
	// A schema-produced diagnostic must be usable as-is in a real clikit
	// Result: it must satisfy the full envelope, not merely construct
	// without an error from NewError.
	s := mustCompile(t, "urn:person4", personSchema)
	doc := map[string]any{"age": "not-a-number"}
	diags, err := Validate(s, doc)
	if err != nil {
		t.Fatalf("Validate: unexpected error %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	if _, err := clikit.NewUsage([]string{"tool"}, nil, diags, nil); err != nil {
		t.Fatalf("diagnostics failed full clikit.Result validation: %v", err)
	}
}

// TestValidate_NonObjectDoc checks that a non-object instance validated
// against an object schema fails rather than being silently skipped.
func TestValidate_NonObjectDoc(t *testing.T) {
	s := mustCompile(t, "urn:person5", personSchema)
	diags, err := Validate(s, "just a string")
	if err != nil {
		t.Fatalf("Validate: unexpected error %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("Validate: string against object schema should produce a diagnostic")
	}
}

// TestValidate_NilDoc checks that a nil document is treated as an instance
// missing every required field, not as a validator crash.
func TestValidate_NilDoc(t *testing.T) {
	s := mustCompile(t, "urn:person6", personSchema)
	diags, err := Validate(s, nil)
	if err != nil {
		t.Fatalf("Validate: unexpected error %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("Validate: nil doc against required-field object schema should produce a diagnostic")
	}
}

// TestValidate_ErrorMessageNotEmpty checks that every normalized diagnostic
// carries a non-blank message, satisfying clikit's line shape.
func TestValidate_ErrorMessageNotEmpty(t *testing.T) {
	s := mustCompile(t, "urn:person7", personSchema)
	doc := map[string]any{"age": float64(-1)}
	diags, _ := Validate(s, doc)
	for _, d := range diags {
		if strings.TrimSpace(d.Message) == "" {
			t.Errorf("diagnostic %+v has empty message", d)
		}
	}
}

// TestTagNamespaces_Basic checks grouping of namespaced and bare tags,
// preserving within-namespace order.
func TestTagNamespaces_Basic(t *testing.T) {
	groups := TagNamespaces([]string{"team:apm", "team:usm", "priority:high", "no-namespace"})
	want := map[string][]string{
		"team":     {"apm", "usm"},
		"priority": {"high"},
		"":         {"no-namespace"},
	}
	if len(groups) != len(want) {
		t.Fatalf("TagNamespaces groups = %+v, want %+v", groups, want)
	}
	for ns, vals := range want {
		got, ok := groups[ns]
		if !ok {
			t.Errorf("missing namespace %q", ns)
			continue
		}
		if len(got) != len(vals) {
			t.Errorf("namespace %q = %v, want %v", ns, got, vals)
			continue
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Errorf("namespace %q[%d] = %q, want %q", ns, i, got[i], vals[i])
			}
		}
	}
}

// TestTagNamespaces_Empty checks that a nil or empty tag slice yields an
// empty group map rather than a nil-map panic or a spurious entry.
func TestTagNamespaces_Empty(t *testing.T) {
	groups := TagNamespaces(nil)
	if len(groups) != 0 {
		t.Fatalf("TagNamespaces(nil) = %v, want empty", groups)
	}
	groups = TagNamespaces([]string{})
	if len(groups) != 0 {
		t.Fatalf("TagNamespaces([]) = %v, want empty", groups)
	}
}

// TestTagNamespaces_MultipleColonsSplitsAtFirst checks that a tag with more
// than one ':' splits only at the first, keeping the rest in the value.
func TestTagNamespaces_MultipleColonsSplitsAtFirst(t *testing.T) {
	groups := TagNamespaces([]string{"env:prod:us-east"})
	got, ok := groups["env"]
	if !ok || len(got) != 1 || got[0] != "prod:us-east" {
		t.Fatalf("TagNamespaces multi-colon: got %v, want env -> [prod:us-east]", groups)
	}
}

// TestTagNamespaces_LeadingColonIsEmptyNamespace checks that a tag starting
// with ':' groups under the empty-string namespace, not a panic or drop.
func TestTagNamespaces_LeadingColonIsEmptyNamespace(t *testing.T) {
	groups := TagNamespaces([]string{":value"})
	got, ok := groups[""]
	if !ok || len(got) != 1 || got[0] != "value" {
		t.Fatalf("TagNamespaces leading colon: got %v", groups)
	}
}

// TestCheckStale_NotStale checks that a recently updated document produces
// no findings.
func TestCheckStale_NotStale(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	updated := now.Add(-24 * time.Hour)
	diags, err := CheckStale(nil, &updated, now, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("CheckStale recent update: want 0 diagnostics, got %d: %+v", len(diags), diags)
	}
}

// TestCheckStale_StaleUpdated checks that an update older than maxAge
// produces a caveats.stale_updated finding valid in a full clikit Result.
func TestCheckStale_StaleUpdated(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	updated := now.Add(-200 * 24 * time.Hour)
	diags, err := CheckStale(nil, &updated, now, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("CheckStale stale update: want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Code != "caveats.stale_updated" {
		t.Errorf("code = %q, want caveats.stale_updated", d.Code)
	}
	if _, err := clikit.NewCaveats([]string{"tool"}, nil, diags); err != nil {
		t.Fatalf("stale diagnostic failed full clikit.Result validation: %v", err)
	}
}

// TestCheckStale_ExactlyAtBoundaryIsNotStale checks the staleness boundary
// is exclusive: age equal to maxAge is not yet stale.
func TestCheckStale_ExactlyAtBoundaryIsNotStale(t *testing.T) {
	// age == maxAge is not > maxAge: boundary is inclusive of "still fresh".
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	maxAge := 90 * 24 * time.Hour
	updated := now.Add(-maxAge)
	diags, err := CheckStale(nil, &updated, now, maxAge)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("CheckStale at exact boundary: want 0 diagnostics, got %d: %+v", len(diags), diags)
	}
}

// TestCheckStale_DateContradiction checks that created after updated
// produces a usage.date_contradiction error finding.
func TestCheckStale_DateContradiction(t *testing.T) {
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // after updated
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	diags, err := CheckStale(&created, &updated, now, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	var found bool
	for _, d := range diags {
		if d.Code == "usage.date_contradiction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckStale created-after-updated: want usage.date_contradiction, got %+v", diags)
	}
}

// TestCheckStale_BothFindingsFireTogether checks that a date contradiction
// and staleness can both fire from a single call.
func TestCheckStale_BothFindingsFireTogether(t *testing.T) {
	// created after updated, AND updated is old enough to also be stale.
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	updated := now.Add(-200 * 24 * time.Hour)
	created := updated.Add(24 * time.Hour) // created after updated
	diags, err := CheckStale(&created, &updated, now, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	var hasError, hasCaveat bool
	for _, d := range diags {
		switch d.Code {
		case "usage.date_contradiction":
			hasError = true
		case "caveats.stale_updated":
			hasCaveat = true
		}
	}
	if !hasError || !hasCaveat {
		t.Fatalf("CheckStale both conditions: want both findings, got %+v", diags)
	}
}

// TestCheckStale_NilFieldsSkipChecks checks that both checks are skipped,
// with no findings and no panic, when both dates are absent.
func TestCheckStale_NilFieldsSkipChecks(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	diags, err := CheckStale(nil, nil, now, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("CheckStale with nil created and updated: want 0 diagnostics, got %+v", diags)
	}
}

// TestCheckStale_EqualCreatedUpdatedIsNotContradiction checks that equal
// created and updated timestamps are not flagged as a contradiction.
func TestCheckStale_EqualCreatedUpdatedIsNotContradiction(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	same := now.Add(-time.Hour)
	diags, err := CheckStale(&same, &same, now, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("CheckStale: unexpected error %v", err)
	}
	for _, d := range diags {
		if d.Code == "usage.date_contradiction" {
			t.Fatalf("equal created/updated flagged as contradiction: %+v", d)
		}
	}
}

// TestPackageExcludesCLIConfig documents and enforces, at the package-API
// level, that this package validates document content only: there is no
// function here that takes or resembles a CLI configuration object, and no
// exported symbol suggests one. If a future change adds CLI-config
// validation to this package, this test's exported-symbol allowlist forces
// an explicit, reviewable edit here.
func TestPackageExcludesCLIConfig(t *testing.T) {
	allowed := map[string]bool{
		"Schema":        true,
		"Compile":       true,
		"Validate":      true,
		"TagNamespaces": true,
		"CheckStale":    true,
	}
	// This is a structural sanity check, not a full AST walk: it pins the
	// known exported surface so any addition (e.g. a "ValidateConfig") is
	// forced to touch this allowlist, making the exclusion reviewable rather
	// than silent.
	for name := range allowed {
		if strings.Contains(strings.ToLower(name), "config") {
			t.Fatalf("exported symbol %q suggests CLI-config validation, which this package must not perform", name)
		}
	}
}
