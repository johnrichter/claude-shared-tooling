package characterize

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	shschema "github.com/johnrichter/claude-shared-tooling/go/schema"
)

// schema/capability-manifest.schema.json is a mechanical, byte-identical copy of
// schemas/plugin-validation/capability-manifest.schema.json (go:embed cannot reach outside this
// package's own directory) -- the real manifest contract, not a reimplementation of it, so a
// manifest this package builds is validated against the same rules a downstream consumer's own
// schema check applies.
//
//go:embed schema/capability-manifest.schema.json
var manifestSchemaJSON []byte

var manifestSchema *shschema.Schema

func init() {
	var err error
	manifestSchema, err = shschema.Compile("ai-shared-lib/characterize/capability-manifest", manifestSchemaJSON)
	if err != nil {
		panic(fmt.Sprintf("characterize: compile embedded capability-manifest schema: %v", err))
	}
}

// Validate checks m against the capability-manifest contract. A valid manifest returns a nil,
// empty diagnostics slice; an invalid one returns one diagnostic per violated constraint. The
// returned error is non-nil only when m itself could not be round-tripped through JSON (never on
// a validation failure).
func (m *Manifest) Validate() ([]clikit.Diagnostic, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("characterize: marshal manifest for validation: %w", err)
	}
	var doc any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		return nil, fmt.Errorf("characterize: decode manifest for validation: %w", err)
	}
	return shschema.Validate(manifestSchema, doc)
}
