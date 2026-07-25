package bh

import (
	"encoding/json"
	"os"
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

const specsPath = "../../anthropic-specifications.json"

// ---- retirement note (model-set four-way sync) ----
//
// This file used to carry TestModelIDFourWaySync and TestKnownModelsArePriced: a regex-scraped
// diff of the selectable model-ID set across plan-schema.json's model.enum, types.go's Model
// consts, anthropic-specifications.json's pricing.list, and build-engine.workflow.js's
// DEFAULT_RATES. Retired now that three of those four are roster-gen targets — mechanical
// projections of schemas/model-roster/model-roster.json, currency-checked in CI by
// tooling/roster-currency (this repo's anthropic-specifications.json) and by the marketplace
// repo's own equivalent job (its plan-schema.json and build-engine.workflow.js) — each
// regenerating its target from the roster and failing on any byte difference. That's a
// stronger guarantee than a set-membership diff: it catches value drift, not just ID drift,
// and traces every failure to the one input that fixes it — the roster.
//
// types.go's Model consts are the fourth source and are NOT roster-gen output — the type system
// needs a real Go identifier per model, which the generator does not (and should not) emit.
// TestModelEnumMatchesSchema above still guards that hand-authored enumerator against the
// schema's roster-derived model.enum, so a model the roster adds but types.go never picks up is
// still caught; it just runs as a two-way check against the generated schema instead of a
// four-way regex scrape across four sources, two of which no longer need independent checking.
