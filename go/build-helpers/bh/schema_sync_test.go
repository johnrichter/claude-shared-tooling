package bh

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Guards against drift between plan-schema.json (the product-architect's output CONTRACT) and
// this package's ValidatePlan/CheckTiers ENFORCEMENT. The schema is the agent-facing source of
// truth for the model/effort value sets; if it changes, these constants must change with it.
// Skips (not fails) when the schema isn't co-located, so the package stays portable if extracted.
const schemaPath = "../../../../agents/product-architect/plan-schema.json"
const examplePath = "../../../../agents/product-architect/plan-example.json"

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("plan-schema.json not found at %s (skipping drift guard): %v", schemaPath, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("plan-schema.json is not valid JSON: %v", err)
	}
	return m
}

func schemaEnum(t *testing.T, schema map[string]any, prop string) []string {
	t.Helper()
	defs, _ := schema["$defs"].(map[string]any)
	task, _ := defs["task"].(map[string]any)
	props, _ := task["properties"].(map[string]any)
	field, _ := props[prop].(map[string]any)
	rawEnum, ok := field["enum"].([]any)
	if !ok {
		t.Fatalf("plan-schema.json $defs.task.properties.%s.enum missing", prop)
	}
	out := make([]string, len(rawEnum))
	for i, v := range rawEnum {
		out[i], _ = v.(string)
	}
	return out
}

func TestModelEnumMatchesSchema(t *testing.T) {
	for _, m := range schemaEnum(t, loadSchema(t), "model") {
		if !Model(m).Known() {
			t.Errorf("schema lists model %q but bh.Model.Known() rejects it — update types.go", m)
		}
	}
}

func TestEffortEnumMatchesSchema(t *testing.T) {
	for _, e := range schemaEnum(t, loadSchema(t), "effort") {
		if !Effort(e).Known() {
			t.Errorf("schema lists effort %q but bh.Effort.Known() rejects it — update types.go", e)
		}
	}
}

// TestPlanExampleValidates guards the worked example against drift from the schema/enforcement.
// The example is the architect's few-shot shape reference; if it silently stops conforming (e.g.
// a new required field, a renamed enum) the agent is primed with an invalid template. This makes
// `go test` fail the moment the example diverges from ValidatePlanBytes — the same gate the
// orchestrator runs on real plans. Skips (not fails) when the example isn't co-located.
func TestPlanExampleValidates(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Skipf("plan-example.json not found at %s (skipping drift guard): %v", examplePath, err)
	}
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("plan-example.json no longer validates against the schema/enforcement — update the example: %v", res.Errors)
	}
}

func TestDeliverableKindEnumMatchesSchema(t *testing.T) {
	for _, k := range schemaEnum(t, loadSchema(t), "deliverable_kind") {
		if !DeliverableKind(k).Known() {
			t.Errorf("schema lists deliverable_kind %q but bh.DeliverableKind.Known() rejects it — update types.go", k)
		}
	}
}

func TestFileSurfaceInSchema(t *testing.T) {
	schema := loadSchema(t)
	defs, _ := schema["$defs"].(map[string]any)
	task, _ := defs["task"].(map[string]any)
	props, _ := task["properties"].(map[string]any)
	if _, ok := props["file_surface"]; !ok {
		t.Fatal("plan-schema.json $defs.task.properties must include 'file_surface' (parallel overlap guard depends on it)")
	}
}

// TestTaskNameRequiredInSchema guards the M12.P1.T1 invariant: plan-schema.json's task
// definition must list 'name' as required, matching ValidatePlanBytes's enforcement in plan.go
// (task %s: name required). A schema that drops the requirement while Go still enforces it
// would reject architect-authored plans the schema itself claims are valid.
func TestTaskNameRequiredInSchema(t *testing.T) {
	schema := loadSchema(t)
	defs, _ := schema["$defs"].(map[string]any)
	task, _ := defs["task"].(map[string]any)
	props, _ := task["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatal("plan-schema.json $defs.task.properties must include 'name'")
	}
	required, _ := task["required"].([]any)
	for _, r := range required {
		if r == "name" {
			return
		}
	}
	t.Fatal("plan-schema.json $defs.task.required must include 'name' (bh.ValidatePlanBytes enforces it — keep schema/Go in sync)")
}

func TestProvenanceIsAllowedRootKey(t *testing.T) {
	// classify reads plan.provenance.design_updated; the schema must permit it (it's
	// additionalProperties:false at the root). Guards the fix from drifting back out.
	schema := loadSchema(t)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["provenance"]; !ok {
		t.Fatal("plan-schema.json root properties must include 'provenance' (orchestrator injects it; classify reads it)")
	}
	if !rootKeys["provenance"] {
		t.Fatal("bh rootKeys must include 'provenance' so validate does not warn on injected provenance")
	}
}

// ---- four-way model-ID sync: plan-schema.json's model enum, types.go's Model
// consts, anthropic-specifications.json's pricing.list map, and build-engine.workflow.js's
// DEFAULT_RATES must all name exactly the same set of priced model IDs. "inherit" is a
// Go/schema-only sentinel (no rate anywhere) and is excluded from the priced-model comparison.
// Without this test, a model ID added to the schema and to types.go but never priced falls
// through outRate()'s unconditional Opus-tier fallback in build-engine.workflow.js — the task
// runs and is silently billed at the Opus rate, right or wrong, with no error and no test
// failure.

const typesGoPath = "types.go"
const specsPath = "../../anthropic-specifications.json"
const buildEnginePath = "../../build-engine.workflow.js"

var modelConstRE = regexp.MustCompile(`Model[A-Za-z0-9]+\s+Model\s*=\s*"([^"]+)"`)

// goModelIDs parses the Model const block directly out of types.go source (rather than
// hard-coding the list in the test) so a new/renamed const is picked up automatically — the
// same reason schemaEnum reads plan-schema.json instead of a copied literal.
func goModelIDs(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(typesGoPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", typesGoPath, err)
	}
	matches := modelConstRE.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("no `ModelXxx Model = \"...\"` consts found in %s — regex out of sync with types.go?", typesGoPath)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// specsPricingModelIDs reads anthropic-specifications.json's `pricing.list` map keys — the
// canonical rate table (replaces the deleted cost-rates.json's `models` map).
func specsPricingModelIDs(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specsPath)
	if err != nil {
		t.Fatalf("anthropic-specifications.json not found at %s: %v", specsPath, err)
	}
	var doc struct {
		Pricing struct {
			List map[string]any `json:"list"`
		} `json:"pricing"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("anthropic-specifications.json is not valid JSON: %v", err)
	}
	out := make([]string, 0, len(doc.Pricing.List))
	for k := range doc.Pricing.List {
		out = append(out, k)
	}
	return out
}

var defaultRatesBlockRE = regexp.MustCompile(`(?s)DEFAULT_RATES\s*=\s*\{(.*?)\}`)
var defaultRatesKeyRE = regexp.MustCompile(`'([^']+)'\s*:`)

// defaultRatesModelIDs parses DEFAULT_RATES's keys out of build-engine.workflow.js source.
func defaultRatesModelIDs(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(buildEnginePath)
	if err != nil {
		t.Skipf("build-engine.workflow.js not found at %s (skipping four-way sync guard): %v", buildEnginePath, err)
	}
	block := defaultRatesBlockRE.FindStringSubmatch(string(raw))
	if block == nil {
		t.Fatalf("could not find `const DEFAULT_RATES = { ... }` in %s — regex out of sync with build-engine.workflow.js?", buildEnginePath)
	}
	matches := defaultRatesKeyRE.FindAllStringSubmatch(block[1], -1)
	if len(matches) == 0 {
		t.Fatalf("DEFAULT_RATES block in %s parsed but yielded no keys", buildEnginePath)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func toSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func sortedList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// TestModelIDFourWaySync is the empirical enforcement the model-id-enum-set-sync rule only
// reminds toward: it diffs the selectable model-ID set across all four independent enumerators
// and fails loudly the moment any one of them drifts from the other three.
func TestModelIDFourWaySync(t *testing.T) {
	schema := toSet(schemaEnum(t, loadSchema(t), "model"))
	delete(schema, string(ModelInherit)) // sentinel: Go/schema-only, never priced

	goIDs := toSet(goModelIDs(t))
	delete(goIDs, string(ModelInherit))

	specsPricing := toSet(specsPricingModelIDs(t))
	defaultRates := toSet(defaultRatesModelIDs(t))

	sources := map[string]map[string]bool{
		"plan-schema.json model.enum":                schema,
		"types.go Model consts":                      goIDs,
		"anthropic-specifications.json pricing.list": specsPricing,
		"build-engine.workflow.js DEFAULT_RATES":     defaultRates,
	}

	// Union of every ID seen anywhere; any ID missing from any one source is a drift.
	union := map[string]bool{}
	for _, set := range sources {
		for id := range set {
			union[id] = true
		}
	}

	for _, id := range sortedList(union) {
		var missingFrom []string
		for name, set := range sources {
			if !set[id] {
				missingFrom = append(missingFrom, name)
			}
		}
		if len(missingFrom) > 0 {
			sort.Strings(missingFrom)
			t.Errorf("model ID %q is missing from: %s — update all four enumerators together", id, strings.Join(missingFrom, ", "))
		}
	}
}

// TestKnownModelsArePriced is the silent-fallback guard the model-id-enum-set-sync rule's
// Failure mode describes: every Model the Go side treats as Known() (except the "inherit"
// sentinel) must resolve to a rate in BOTH anthropic-specifications.json's pricing.list and
// build-engine.workflow.js's DEFAULT_RATES. Without this, a model recognized by types.go/the
// schema but never priced falls through outRate()'s unconditional Opus-tier fallback with no
// error — this test is what closes that gap (referenced by the rule's Remediation).
func TestKnownModelsArePriced(t *testing.T) {
	specsPricing := toSet(specsPricingModelIDs(t))
	defaultRates := toSet(defaultRatesModelIDs(t))

	for _, id := range goModelIDs(t) {
		if id == string(ModelInherit) {
			continue // sentinel: Go/schema-only, never priced
		}
		if !Model(id).Known() {
			continue // covered by TestModelEnumMatchesSchema / drift elsewhere
		}
		if !specsPricing[id] {
			t.Errorf("Known() model %q has no rate in anthropic-specifications.json pricing.list", id)
		}
		if !defaultRates[id] {
			t.Errorf("Known() model %q has no entry in build-engine.workflow.js DEFAULT_RATES", id)
		}
	}
}
