package schema

import (
	"encoding/json"
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema is a compiled draft-2020-12 JSON Schema, ready to validate documents against. It is
// built from bytes the caller supplies at runtime — this package ships no schema of its own.
type Schema struct {
	compiled *jsonschema.Schema
}

// Compile parses schemaJSON as a draft-2020-12 JSON Schema and compiles it. id identifies the
// schema for $ref resolution and in compiler diagnostics; it need not be a reachable URL, only
// unique among schemas compiled together.
func Compile(id string, schemaJSON []byte) (*Schema, error) {
	var doc any
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return nil, fmt.Errorf("schema: parse %s: %w", id, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("schema: add resource %s: %w", id, err)
	}
	compiled, err := c.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("schema: compile %s: %w", id, err)
	}
	return &Schema{compiled: compiled}, nil
}

// Validate checks doc — a JSON-decoded document tree (map[string]any, []any, or a leaf value) —
// against s. A valid doc returns a nil, empty diagnostics slice. An invalid doc returns one
// clikit error diagnostic per violated schema constraint, each naming the failing instance
// location and keyword. The returned error is non-nil only when a diagnostic itself could not be
// built (never on a validation failure).
func Validate(s *Schema, doc any) ([]clikit.Diagnostic, error) {
	err := s.compiled.Validate(doc)
	if err == nil {
		return nil, nil
	}
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return nil, fmt.Errorf("schema: validate: %w", err)
	}

	basic := verr.BasicOutput()
	leaves := basic.Errors
	if len(leaves) == 0 {
		leaves = []jsonschema.OutputUnit{*basic}
	}

	diags := make([]clikit.Diagnostic, 0, len(leaves))
	for _, leaf := range leaves {
		message := "schema violation"
		if leaf.Error != nil {
			message = leaf.Error.String()
		}
		context := map[string]any{
			"instance_location": orRoot(leaf.InstanceLocation),
			"keyword_location":  orRoot(leaf.KeywordLocation),
		}
		triage := clikit.Manual(fmt.Sprintf("fix %s in the document so it satisfies the schema", orRoot(leaf.InstanceLocation)))
		d, err := clikit.NewError("usage.schema_invalid", message, triage, context)
		if err != nil {
			return nil, fmt.Errorf("schema: normalize validation error: %w", err)
		}
		diags = append(diags, d)
	}
	return diags, nil
}

// orRoot returns loc, or "/" (the document root) when loc is empty.
func orRoot(loc string) string {
	if loc == "" {
		return "/"
	}
	return loc
}
