package compliance

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// PauseSchema marks a release-pause register document's shape.
const PauseSchema = "release-pause-register@1.0.0"

// A pause entry's status, mirroring release-pause-register.schema.json's enum. Only
// PauseStatusOpen is a value release-transaction's gate reads as a reason to refuse a release.
const (
	PauseStatusOpen       = "open"
	PauseStatusResolved   = "resolved"
	PauseStatusUnmeasured = "declared-unmeasured"
)

// PauseEntry is one release-pause register row -- the hand-off release-transaction's gate reads.
type PauseEntry struct {
	DefectID      string  `json:"defect_id"`
	Owner         string  `json:"owner"`
	OwnerKind     string  `json:"owner_kind"`
	InvariantID   string  `json:"invariant_id"`
	Status        string  `json:"status"`
	Model         string  `json:"model,omitempty"`
	DeclaredFloor float64 `json:"declared_floor,omitempty"`
	MeasuredRate  float64 `json:"measured_rate,omitempty"`
	Opened        string  `json:"opened,omitempty"`
}

// PauseRegister is the release-pause-register.json document.
type PauseRegister struct {
	Schema  string       `json:"schema"`
	Entries []PauseEntry `json:"entries"`
}

// LoadPauseRegister reads path, tolerating an absent file as a fresh, empty, schema-stamped
// register -- the first defect measured against a project does not require one to pre-exist.
func LoadPauseRegister(path string) (PauseRegister, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PauseRegister{Schema: PauseSchema}, nil
		}
		return PauseRegister{}, fmt.Errorf("compliance: read %s: %w", path, err)
	}
	var reg PauseRegister
	if err := json.Unmarshal(raw, &reg); err != nil {
		return PauseRegister{}, fmt.Errorf("compliance: parse %s: %w", path, err)
	}
	if reg.Schema == "" {
		reg.Schema = PauseSchema
	}
	return reg, nil
}

// Save writes reg to path atomically, pretty-printed for reviewability.
func (reg PauseRegister) Save(path string) error {
	raw, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("compliance: encode pause register: %w", err)
	}
	raw = append(raw, '\n')
	return fsx.WriteAtomic(path, raw, registryFilePerm)
}

// findByModel returns the index of reg's below-floor-shaped entry for (invariantID, model), or -1.
func (reg PauseRegister) findByModel(invariantID, model string) int {
	for i, e := range reg.Entries {
		if e.InvariantID == invariantID && e.Model == model {
			return i
		}
	}
	return -1
}

// findUnmeasured returns the index of reg's declared-unmeasured entry for invariantID, or -1.
func (reg PauseRegister) findUnmeasured(invariantID string) int {
	for i, e := range reg.Entries {
		if e.InvariantID == invariantID && e.Status == PauseStatusUnmeasured {
			return i
		}
	}
	return -1
}

// UpsertOpen records a below-floor finding as an open pause entry keyed by (invariant, model): a
// re-measurement of the same miss refreshes the existing entry's rate rather than duplicating it,
// and reopens one a prior measurement had resolved. defectID names the feedback-register entry
// this pause corresponds to; the original defect id and opened timestamp survive a refresh.
func (reg PauseRegister) UpsertOpen(f BelowFloor, ownerKind, defectID string, at time.Time) PauseRegister {
	entry := PauseEntry{
		DefectID:      defectID,
		Owner:         f.Owner,
		OwnerKind:     ownerKind,
		InvariantID:   f.InvariantID,
		Status:        PauseStatusOpen,
		Model:         f.Model,
		DeclaredFloor: f.DeclaredFloor,
		MeasuredRate:  f.MeasuredRate,
		Opened:        at.UTC().Format(time.RFC3339),
	}
	entries := append([]PauseEntry{}, reg.Entries...)
	if i := reg.findByModel(f.InvariantID, f.Model); i >= 0 {
		entry.DefectID = entries[i].DefectID
		if entries[i].Opened != "" {
			entry.Opened = entries[i].Opened
		}
		entries[i] = entry
	} else {
		entries = append(entries, entry)
	}
	out := reg
	out.Entries = entries
	return out
}

// ResolveIfOpen closes an existing open entry for (invariantID, model) once a later measurement is
// at or above the floor -- the pause is a live signal, not a one-way ratchet. A no-op when no open
// entry exists.
func (reg PauseRegister) ResolveIfOpen(invariantID, model string) PauseRegister {
	i := reg.findByModel(invariantID, model)
	if i < 0 || reg.Entries[i].Status != PauseStatusOpen {
		return reg
	}
	entries := append([]PauseEntry{}, reg.Entries...)
	entries[i].Status = PauseStatusResolved
	out := reg
	out.Entries = entries
	return out
}

// UpsertUnmeasured records a still-declared-unmeasured entry as a declared-unmeasured row: visible
// to anyone reading the register, but never the "open" status release-transaction's gate acts on,
// so it pauses nothing. This is what keeps an unmeasured advisory from being counted as
// enforcement while still surfacing it at the owner's release. A no-op once one is already
// recorded for invariantID.
func (reg PauseRegister) UpsertUnmeasured(invariantID, owner, ownerKind, defectID string) PauseRegister {
	if reg.findUnmeasured(invariantID) >= 0 {
		return reg
	}
	entry := PauseEntry{
		DefectID:    defectID,
		Owner:       owner,
		OwnerKind:   ownerKind,
		InvariantID: invariantID,
		Status:      PauseStatusUnmeasured,
	}
	out := reg
	out.Entries = append(append([]PauseEntry{}, reg.Entries...), entry)
	return out
}
