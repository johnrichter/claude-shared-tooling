package compliance

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// registryFilePerm matches the permission every generated report in this tree is written with.
const registryFilePerm = 0o644

// Rung-4's two measurement_status values, mirroring invariant-registry.schema.json's enum.
const (
	// StatusDeclaredUnmeasured is a rung-4 entry's status until every model it declares a floor
	// for has a measured rate. Not enforcement: UnmeasuredAtRelease and ApplyUnmeasured are the
	// consumers that act on it, and neither one pauses a release for it.
	StatusDeclaredUnmeasured = "declared-unmeasured"
	// StatusMeasured is set once every model an entry declares a floor for has a measured rate.
	StatusMeasured = "measured"
)

// Document is the on-disk shape of invariant-registry.json, decoded and re-encoded in full so a
// write-back never drops a section this package does not itself act on -- discovery, and every
// field of every rung this package does not mutate. Only rung-4 entries' measurement fields are
// ever written by this package.
type Document struct {
	Schema     string          `json:"schema"`
	Discovery  Discovery       `json:"discovery"`
	Gates      map[string]Gate `json:"gates"`
	Invariants []Entry         `json:"invariants"`
}

// Discovery mirrors the registry's discovery block: the roots the completeness check crawls for
// shipped gates. Carried through unmodified on write-back; OwnerKind is this package's only
// reader of it.
type Discovery struct {
	Note  string          `json:"note,omitempty"`
	Roots []DiscoveryRoot `json:"roots"`
}

// DiscoveryRoot is one discovery root: a repository, how its gates are recognised, and which
// owners are in scope there.
type DiscoveryRoot struct {
	Repo          string   `json:"repo"`
	Strategy      string   `json:"strategy"`
	Globs         []string `json:"globs"`
	InScopeOwners []string `json:"in_scope_owners"`
}

// Gate is one entry of the registry's "gates" map: where a gate lives, who owns it, and whether
// it has shipped.
type Gate struct {
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// Entry is one declared invariant, decoded verbatim from its JSON keys. Fields span every rung;
// only the rung-4 fields below are ever mutated by this package, and every other field round-
// trips through Document.Save unchanged.
type Entry struct {
	ID              string   `json:"id"`
	Statement       string   `json:"statement"`
	Rung            int      `json:"rung"`
	FailDirection   string   `json:"fail_direction"`
	BlastRadius     string   `json:"blast_radius"`
	Owner           string   `json:"owner"`
	Status          string   `json:"status"`
	ReasonLowerRung string   `json:"reason_lower_rung,omitempty"`
	References      []string `json:"references,omitempty"`
	SupersededBy    string   `json:"superseded_by,omitempty"`
	Notes           string   `json:"notes,omitempty"`

	FailFastSymbol string `json:"fail_fast_symbol,omitempty"`

	Trigger   string `json:"trigger,omitempty"`
	GateID    string `json:"gate_id,omitempty"`
	Condition string `json:"condition,omitempty"`

	TestID string `json:"test_id,omitempty"`

	// Rung 4 -- the fields MeasureRegistry writes back.
	ComplianceFloors  map[string]*float64 `json:"compliance_floors,omitempty"`
	MeasurementStatus string              `json:"measurement_status,omitempty"`
	MeasuredRates     map[string]float64  `json:"measured_rates,omitempty"`
	MeasuredAt        string              `json:"measured_at,omitempty"`
	RegisterEntryID   string              `json:"register_entry_id,omitempty"`

	DocPath string `json:"doc_path,omitempty"`
}

// LoadDocument reads and decodes the registry at path. A missing, empty, or malformed registry is
// a packaging defect (PackagingDefectError), never a signal that no rung-4 entries exist.
func LoadDocument(path string) (*Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &PackagingDefectError{Err: fmt.Errorf("read %s: %w", path, err)}
	}
	if len(raw) == 0 {
		return nil, &PackagingDefectError{Err: fmt.Errorf("%s is empty", path)}
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, &PackagingDefectError{Err: fmt.Errorf("%s is not valid JSON: %w", path, err)}
	}
	if doc.Schema == "" || len(doc.Invariants) == 0 {
		return nil, &PackagingDefectError{Err: fmt.Errorf("%s has no schema, or declares no invariants", path)}
	}
	return &doc, nil
}

// Save writes doc back to path atomically, pretty-printed -- the write-back half of rung 4. A
// caller mutates the registry's rung-4 entries in place (via MeasureRegistry) and calls Save
// once, so every other rung's fields round-trip unchanged and no reader ever observes a partial
// registry.
func (d *Document) Save(path string) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("compliance: encode registry: %w", err)
	}
	raw = append(raw, '\n')
	return fsx.WriteAtomic(path, raw, registryFilePerm)
}

// RungFourShipped returns pointers into doc.Invariants for every shipped rung-4 entry -- the only
// entries this package measures. A planned rung-4 entry's floor names an intended target that
// need not resolve yet, so no consumer asserts it; a retired one is history.
func (d *Document) RungFourShipped() []*Entry {
	var out []*Entry
	for i := range d.Invariants {
		if d.Invariants[i].Rung == 4 && d.Invariants[i].Status == "shipped" {
			out = append(out, &d.Invariants[i])
		}
	}
	return out
}

// EntryByID returns the entry with the given id, or nil.
func (d *Document) EntryByID(id string) *Entry {
	for i := range d.Invariants {
		if d.Invariants[i].ID == id {
			return &d.Invariants[i]
		}
	}
	return nil
}

// OwnerKind reports "plugin" when owner appears in a claude-plugin-hooks discovery root's
// in_scope_owners, "cli" otherwise. This is a default matching the release-pause register's
// two-value owner_kind enum; a caller with better information (e.g. a release-transaction record
// naming a "library" or "schema-module" subject) should override it rather than trust this guess.
func (d *Document) OwnerKind(owner string) string {
	for _, root := range d.Discovery.Roots {
		if root.Strategy != "claude-plugin-hooks" {
			continue
		}
		for _, o := range root.InScopeOwners {
			if o == owner {
				return "plugin"
			}
		}
	}
	return "cli"
}
