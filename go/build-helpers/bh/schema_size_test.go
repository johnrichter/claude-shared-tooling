package bh

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
)

// SC5 guard: the agent-facing result schemas (IMPL/TEST/REVIEW, engine-schemas.json) must never be
// able to produce a serialized value larger than the smallest model output ceiling in the fleet.
// If a schema can, an agent hitting the worst case gets truncated JSON the engine can't parse.
// This file computes each schema's WORST-CASE serialized size by construction (every bound taken
// to its max) and asserts it stays under the ceiling with margin — so the guard fires the moment
// a future edit removes or loosens a bound, not just when a real response happens to overflow.

const engineSchemasPath = "../../schemas/engine-schemas.json"

// perElementOverhead approximates the comma/whitespace JSON adds around each array element beyond
// the element's own serialized bytes (worst case; slightly over-counts, never under-counts).
const perElementOverhead = 4

// perKeyOverhead approximates the quotes+colon+comma JSON adds around each object key beyond the
// key's own characters and the value's own serialized bytes (worst case; slightly over-counts).
const perKeyOverhead = 4

// bytesPerToken is a deliberately conservative (low) chars-per-token ratio. Real Anthropic tokenization
// averages well above 3 chars/token for JSON-ish structured text; using 3.0 means tokens = ceil(bytes/3)
// OVER-estimates the token count, so a schema that passes this guard has real margin, not borrowed margin.
const bytesPerToken = 3.0

// worstCaseBytes recursively computes the worst-case serialized JSON size (in bytes) of a value
// conforming to schema, taking every bound (maxLength, enum, maxItems) to its maximum. A schema
// that lacks a required bound (a string with neither maxLength nor enum, an array without maxItems,
// or an object without additionalProperties:false) is NOT bounded-by-construction and returns an
// error rather than a silently-optimistic size — the whole point of this guard is to catch exactly
// that kind of unbounded slip.
//
// ASSUMPTION (not a worst-case upper bound): string content is counted at 1 byte per maxLength unit.
// JSON escaping and multi-byte UTF-8 can inflate a code point to up to 6 serialized bytes (control
// char -> \u00xx) or 4 (supplementary-plane), and the agent fields bounded here are prose/enums that
// serialize ~1:1 in practice, so this holds for realistic content. It does NOT hold for adversarial
// content: at 4 bytes/char REVIEW sits at ~97% of ceiling and at 6 bytes/char TEST/REVIEW exceed it.
// Tightening this into a true byte-upper-bound (multiply string content by an escape factor) is a
// schema-design decision left to the owner — see the reviewer's residual-risk note for M1.P3.T3.
func worstCaseBytes(schema map[string]any) (int, error) {
	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		if rawEnum, ok := schema["enum"].([]any); ok {
			longest := 0
			for _, v := range rawEnum {
				s, _ := v.(string)
				if len(s) > longest {
					longest = len(s)
				}
			}
			return longest + 2, nil // quotes
		}
		if maxLen, ok := numOf(schema["maxLength"]); ok {
			return int(maxLen) + 2, nil // quotes
		}
		return 0, fmt.Errorf("unbounded string schema: no maxLength or enum (%v)", schema)

	case "boolean":
		return len("false"), nil // worst-case literal

	case "array":
		maxItems, ok := numOf(schema["maxItems"])
		if !ok {
			return 0, fmt.Errorf("unbounded array schema: no maxItems (%v)", schema)
		}
		itemsSchema, ok := schema["items"].(map[string]any)
		if !ok {
			return 0, fmt.Errorf("array schema missing items definition (%v)", schema)
		}
		itemSize, err := worstCaseBytes(itemsSchema)
		if err != nil {
			return 0, fmt.Errorf("array items: %w", err)
		}
		n := int(maxItems)
		return 2 + n*(itemSize+perElementOverhead), nil // 2 = brackets

	case "object":
		// additionalProperties MUST be literally false, else the object admits unlimited extra keys
		// (unbounded name + value) that this function never counts — a silent under-count. JSON Schema
		// permits additional properties by default, so absence, `true`, or a sub-schema all fail here:
		// dropping additionalProperties:false is exactly the kind of bound-loss the guard must catch.
		if ap, ok := schema["additionalProperties"].(bool); !ok || ap {
			return 0, fmt.Errorf("object schema is not bounded: additionalProperties must be false (%v)", schema)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			return 2, nil // empty object: braces only
		}
		total := 2 // braces
		for key, raw := range props {
			propSchema, ok := raw.(map[string]any)
			if !ok {
				return 0, fmt.Errorf("property %q schema is malformed (%v)", key, raw)
			}
			valSize, err := worstCaseBytes(propSchema)
			if err != nil {
				return 0, fmt.Errorf("property %q: %w", key, err)
			}
			total += len(key) + perKeyOverhead + valSize
		}
		return total, nil

	default:
		return 0, fmt.Errorf("unsupported/missing schema type %q (%v)", typ, schema)
	}
}

func numOf(v any) (float64, bool) {
	f, ok := v.(float64) // encoding/json unmarshals all JSON numbers into map[string]any as float64
	return f, ok
}

// deepCopy clones a decoded-JSON value (map[string]any / []any / scalars) so a test can mutate the
// copy (e.g. strip a bound) without corrupting the shared loaded schema used by other assertions.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v
	}
}

func loadEngineSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(engineSchemasPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", engineSchemasPath, err)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s is not valid JSON: %v", engineSchemasPath, err)
	}
	if len(m) == 0 {
		t.Fatalf("%s decoded to zero schemas", engineSchemasPath)
	}
	return m
}

// smallestMaxOutputTokens reads anthropic-specifications.json's model.<id>.max_output_tokens for
// every model and returns the MINIMUM. Deliberately does not hardcode a ceiling: adding a cheaper/
// smaller model to the specs file automatically tightens this guard on the next run.
func smallestMaxOutputTokens(t *testing.T) (int, string) {
	t.Helper()
	raw, err := os.ReadFile(specsPath) // defined in schema_sync_test.go: "../../anthropic-specifications.json"
	if err != nil {
		t.Fatalf("anthropic-specifications.json not found at %s: %v", specsPath, err)
	}
	var doc struct {
		Model map[string]struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("anthropic-specifications.json is not valid JSON: %v", err)
	}
	if len(doc.Model) == 0 {
		t.Fatalf("anthropic-specifications.json 'model' map is empty")
	}
	smallestID := ""
	smallest := math.MaxInt
	for id, spec := range doc.Model {
		if spec.MaxOutputTokens <= 0 {
			t.Fatalf("model %q has no positive max_output_tokens", id)
		}
		if spec.MaxOutputTokens < smallest {
			smallest = spec.MaxOutputTokens
			smallestID = id
		}
	}
	return smallest, smallestID
}

// TestSchemaWorstCaseUnderModelCeiling is the durable enforcement of SC5: every agent-facing
// result schema's worst-case serialized size, converted to a conservative (over-counting) token
// estimate, must stay under the smallest max_output_tokens across the whole model fleet. Fails
// the instant a schema's bounds are loosened enough to risk truncation on the cheapest model.
func TestSchemaWorstCaseUnderModelCeiling(t *testing.T) {
	schemas := loadEngineSchemas(t)
	ceiling, ceilingModel := smallestMaxOutputTokens(t)
	t.Logf("model output ceiling: %d tokens (smallest, model=%s)", ceiling, ceilingModel)

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		name, schema := name, schemas[name]
		t.Run(name, func(t *testing.T) {
			bytes, err := worstCaseBytes(schema)
			if err != nil {
				t.Fatalf("schema %q is not bounded-by-construction: %v", name, err)
			}
			tokens := int(math.Ceil(float64(bytes) / bytesPerToken))
			margin := ceiling - tokens
			t.Logf("schema=%s worst-case-bytes=%d est-tokens=%d ceiling=%d margin=%d", name, bytes, tokens, ceiling, margin)
			if tokens >= ceiling {
				t.Errorf("schema %q worst-case estimate (%d tokens) meets/exceeds the model output ceiling (%d tokens, model=%s) — tighten a bound", name, tokens, ceiling, ceilingModel)
			}
		})
	}
}

// TestUnboundedSchemaIsRejected proves the guard bites: worstCaseBytes must error on schemas that
// are not bounded-by-construction (an array missing maxItems, a string missing maxLength/enum).
// If a future edit accidentally drops a maxItems/maxLength from engine-schemas.json, this is the
// shape of failure that would surface it rather than silently under-counting the worst case.
func TestUnboundedSchemaIsRejected(t *testing.T) {
	t.Run("unbounded-array", func(t *testing.T) {
		synthetic := map[string]any{
			"type": "array",
			// no maxItems: unbounded
			"items": map[string]any{"type": "string", "maxLength": float64(10)},
		}
		if _, err := worstCaseBytes(synthetic); err == nil {
			t.Fatal("expected worstCaseBytes to reject an array schema with no maxItems, got nil error")
		}
	})

	t.Run("unbounded-string", func(t *testing.T) {
		synthetic := map[string]any{
			"type": "string",
			// no maxLength, no enum: unbounded
		}
		if _, err := worstCaseBytes(synthetic); err == nil {
			t.Fatal("expected worstCaseBytes to reject a string schema with no maxLength/enum, got nil error")
		}
	})

	t.Run("unbounded-nested-in-object", func(t *testing.T) {
		synthetic := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"notes": map[string]any{"type": "string"}, // unbounded, nested one level down
			},
		}
		if _, err := worstCaseBytes(synthetic); err == nil {
			t.Fatal("expected worstCaseBytes to reject an object whose property is unbounded, got nil error")
		}
	})

	// additionalProperties defaults to permissive in JSON Schema: an object that omits it (or sets it
	// true / to a sub-schema) admits unlimited extra keys of unbounded name+value, so it is NOT
	// bounded-by-construction even when every declared property is bounded. worstCaseBytes must error.
	t.Run("object-missing-additionalProperties-false", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			ap   any
		}{
			{"absent", nil},
			{"true", true},
			{"subschema", map[string]any{"type": "string", "maxLength": float64(10)}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				synthetic := map[string]any{
					"type":       "object",
					"properties": map[string]any{"summary": map[string]any{"type": "string", "maxLength": float64(10)}},
				}
				if tc.ap != nil {
					synthetic["additionalProperties"] = tc.ap
				}
				if _, err := worstCaseBytes(synthetic); err == nil {
					t.Fatalf("expected worstCaseBytes to reject an object whose additionalProperties is %s, got nil error", tc.name)
				}
			})
		}
	})
}

// TestStrippingBoundFromRealSchemaIsDetected is the regression-proof for the guard itself: it
// deep-copies a REAL engine schema (never mutating the file on disk or the shared loaded map),
// strips one maxItems bound from a real array property, and asserts worstCaseBytes now errors.
// This is what "someone drops a maxItems from engine-schemas.json" looks like in practice — the
// guard must catch it, not just catch hand-written synthetic examples.
func TestStrippingBoundFromRealSchemaIsDetected(t *testing.T) {
	schemas := loadEngineSchemas(t)
	impl, ok := schemas["IMPL"]
	if !ok {
		t.Fatal("expected engine-schemas.json to contain an IMPL schema for this regression check")
	}

	// sanity: the untouched copy must still be bounded (proves the strip below is what breaks it).
	if _, err := worstCaseBytes(deepCopy(impl).(map[string]any)); err != nil {
		t.Fatalf("IMPL schema unexpectedly unbounded before mutation: %v", err)
	}

	mutated := deepCopy(impl).(map[string]any)
	props := mutated["properties"].(map[string]any)
	acceptance, ok := props["acceptance"].(map[string]any)
	if !ok {
		t.Fatal("expected IMPL.properties.acceptance to exist (array with maxItems) for this regression check")
	}
	delete(acceptance, "maxItems")

	if _, err := worstCaseBytes(mutated); err == nil {
		t.Fatal("expected worstCaseBytes to reject IMPL schema after stripping acceptance.maxItems, got nil error — the guard would miss a real regression")
	}
}
