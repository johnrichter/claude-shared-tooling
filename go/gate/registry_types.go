package gate

// Registry is the on-disk shape of invariant-registry.json, decoded verbatim from its JSON
// keys. Only the fields this package's consumers read are declared; "discovery" (the gate
// crawler's roots) is the registry lint's concern, not this package's.
type Registry struct {
	Schema     string             `json:"schema"`
	Gates      map[string]RegGate `json:"gates"`
	Invariants []RegistryEntry    `json:"invariants"`
}

// RegGate is one entry of the registry's "gates" map: where a gate lives, who owns it, and
// whether it has shipped.
type RegGate struct {
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// RegistryEntry is one declared invariant, decoded verbatim from its JSON keys. Fields are a
// superset across every rung — only the ones a given rung's schema branch requires are
// populated for that entry.
type RegistryEntry struct {
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

	// Rung 1
	FailFastSymbol string `json:"fail_fast_symbol,omitempty"`

	// Rung 2
	Trigger   string `json:"trigger,omitempty"`
	GateID    string `json:"gate_id,omitempty"`
	Condition string `json:"condition,omitempty"`

	// Rung 3
	TestID string `json:"test_id,omitempty"`

	// Rung 4
	ComplianceFloors  map[string]*float64 `json:"compliance_floors,omitempty"`
	MeasurementStatus string              `json:"measurement_status,omitempty"`
	MeasuredRates     map[string]float64  `json:"measured_rates,omitempty"`
	MeasuredAt        string              `json:"measured_at,omitempty"`
	RegisterEntryID   string              `json:"register_entry_id,omitempty"`

	// Rung 5
	DocPath string `json:"doc_path,omitempty"`
}
