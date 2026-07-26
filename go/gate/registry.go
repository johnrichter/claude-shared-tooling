package gate

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed invariant-registry.json
var embeddedRegistryJSON []byte

var (
	registryDoc     *Registry
	registryLoadErr error
)

func init() {
	registryDoc, registryLoadErr = parseRegistry(embeddedRegistryJSON)
}

// parseRegistry decodes one registry document, reporting missing, empty, or malformed data as
// an error. Kept separate from the embed so it can be exercised directly against hand-built
// byte slices without needing a broken build.
func parseRegistry(data []byte) (*Registry, error) {
	if len(data) == 0 {
		return nil, errors.New("embedded invariant registry is empty")
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("embedded invariant registry is not valid JSON: %w", err)
	}
	if reg.Schema == "" {
		return nil, errors.New("embedded invariant registry has no schema field")
	}
	if len(reg.Invariants) == 0 {
		return nil, errors.New("embedded invariant registry declares no invariants")
	}
	return &reg, nil
}

// LoadRegistry returns the embedded invariant registry, or a PackagingDefectError if the
// embed failed to load or parse. Every other exported function in this file routes through
// this first, so a load failure never falls through to "no rung-2 entries" or to a silent
// pass.
func LoadRegistry() (*Registry, error) {
	if registryLoadErr != nil {
		return nil, &PackagingDefectError{Err: registryLoadErr}
	}
	return registryDoc, nil
}

// Rung2Declaration is what a rung-2 entry publishes for declared-vs-actual firing
// verification: the trigger and condition a gate's actual firing is compared against, plus
// the gate it names and that gate's own packaging metadata. Status is carried through
// unfiltered — a caller asserts a declaration only while Status is "shipped"; "planned"
// reports intent with a target that need not resolve yet, and "retired" is history. Neither
// is a pass.
type Rung2Declaration struct {
	ID            string
	Status        string
	FailDirection string
	GateID        string
	Trigger       string
	Condition     string
	GatePath      string
	GateStatus    string
}

// Rung2Declarations returns every rung-2 entry's declared trigger, condition, and fail
// direction, for a caller (bandcheck's overfire precision check) to compare against the named
// gate's actual firing. Rung-1 and rung-3 entries are excluded — they route to the invariant
// linter's symbol and test-id resolution, not to this library — and rung-4/rung-5 entries have
// no gate consumer by design (see CheckCompleteness).
func Rung2Declarations() ([]Rung2Declaration, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	var out []Rung2Declaration
	for _, entry := range reg.Invariants {
		if entry.Rung != 2 {
			continue
		}
		gate := reg.Gates[entry.GateID]
		out = append(out, Rung2Declaration{
			ID:            entry.ID,
			Status:        entry.Status,
			FailDirection: entry.FailDirection,
			GateID:        entry.GateID,
			Trigger:       entry.Trigger,
			Condition:     entry.Condition,
			GatePath:      gate.Path,
			GateStatus:    gate.Status,
		})
	}
	return out, nil
}

// CompletenessViolation names one registry entry that fails a schema-completeness
// requirement, and which requirement it failed.
type CompletenessViolation struct {
	EntryID string
	Message string
}

// CheckCompleteness runs the schema-completeness checks this package is the consumer of
// record for: every rung weaker than a deny gate (3, 4, 5) carries a reason_lower_rung, every
// rung-4 entry carries its compliance-floor fields, and every rung-5 entry carries a doc_path.
// This asserts presence and shape only — per invariant-registry.schema.json's
// x-verification-model, rungs 4 and 5 have no consumer that checks anything beyond it, and
// this function never claims otherwise. Full schema validation (types, patterns, the
// registry-wide restatement and resolution checks) is the registry lint's job
// (schemas/invariant-registry/check.py); this is not a second copy of that.
func CheckCompleteness(reg *Registry) []CompletenessViolation {
	var violations []CompletenessViolation
	for _, entry := range reg.Invariants {
		if entry.Rung >= 3 && strings.TrimSpace(entry.ReasonLowerRung) == "" {
			violations = append(violations, CompletenessViolation{
				EntryID: entry.ID,
				Message: "rung is weaker than a deny gate but reason_lower_rung is missing",
			})
		}
		switch entry.Rung {
		case 4:
			if len(entry.ComplianceFloors) == 0 {
				violations = append(violations, CompletenessViolation{entry.ID, "rung 4 entry has no compliance_floors"})
			}
			if entry.MeasurementStatus == "" {
				violations = append(violations, CompletenessViolation{entry.ID, "rung 4 entry has no measurement_status"})
			}
			if entry.RegisterEntryID == "" {
				violations = append(violations, CompletenessViolation{entry.ID, "rung 4 entry has no register_entry_id"})
			}
		case 5:
			if entry.DocPath == "" {
				violations = append(violations, CompletenessViolation{entry.ID, "rung 5 entry has no doc_path"})
			}
		}
	}
	return violations
}

// CheckRegistryCompleteness loads the embedded registry and runs CheckCompleteness against
// it. A PackagingDefectError from the load is returned as-is and carries no violations — a
// caller must report it as a packaging defect, never as a clean or failed completeness check.
func CheckRegistryCompleteness() ([]CompletenessViolation, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	return CheckCompleteness(reg), nil
}
