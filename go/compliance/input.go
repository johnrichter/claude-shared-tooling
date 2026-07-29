package compliance

import (
	"encoding/json"
	"fmt"
	"os"
)

// MinTrials is the fewest trials a single measurement may rest on. A rate computed from fewer
// trials is not a rate, it is one coin flip reported as one, so LoadResults refuses anything below
// it outright: no document this loader accepts can carry a single-shot probe.
const MinTrials = 2

// ResultsSchema marks a measurement-input document's shape.
const ResultsSchema = "compliance-measurement-input/v1"

// TrialResult is one measurement mechanism's verdict on one trial: whether the behavioral tier's
// observed, actual behavior honored the invariant's declared statement.
type TrialResult struct {
	Honored bool `json:"honored"`
}

// InvariantResult is every trial the behavioral tier ran for one invariant on one model. Mechanism
// names the repeatable check that produced Trials -- the plugin-behavioral matrix, a dispatch-log
// classifier, or any other declared-vs-actual comparison -- and its presence is what makes this a
// mechanism check rather than an unexplained number; LoadResults refuses an entry naming none.
type InvariantResult struct {
	InvariantID string        `json:"invariant_id"`
	Model       string        `json:"model"`
	Mechanism   string        `json:"mechanism"`
	Trials      []TrialResult `json:"trials"`
}

// Rate returns the fraction of r's trials that honored the invariant.
func (r InvariantResult) Rate() float64 {
	honored := 0
	for _, t := range r.Trials {
		if t.Honored {
			honored++
		}
	}
	return float64(honored) / float64(len(r.Trials))
}

// ResultsDocument is the behavioral tier's own measurement output: one InvariantResult per
// invariant/model pair it ran this pass. This package never runs a probe itself -- it wires an
// already-measured mechanism's results into the registry and the release-pause hand-off.
type ResultsDocument struct {
	Schema     string            `json:"schema"`
	MeasuredAt string            `json:"measured_at,omitempty"`
	Results    []InvariantResult `json:"results"`
}

// LoadResults reads and validates a measurement-input document at path. Every result must name a
// non-empty invariant id and model, name a mechanism, and carry at least MinTrials trials; a
// document failing any of those is rejected outright, so no caller can feed a single-shot probe
// through this loader and have it reach the registry.
func LoadResults(path string) (*ResultsDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("compliance: read %s: %w", path, err)
	}
	var doc ResultsDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compliance: %s is not valid JSON: %w", path, err)
	}
	if len(doc.Results) == 0 {
		return nil, fmt.Errorf("compliance: %s declares no results", path)
	}
	for _, r := range doc.Results {
		if r.InvariantID == "" || r.Model == "" {
			return nil, fmt.Errorf("compliance: %s: a result is missing invariant_id or model", path)
		}
		if r.Mechanism == "" {
			return nil, fmt.Errorf("compliance: %s: %s/%s names no mechanism -- an unnamed measurement is not a mechanism check", path, r.InvariantID, r.Model)
		}
		if len(r.Trials) < MinTrials {
			return nil, fmt.Errorf("compliance: %s: %s/%s has %d trial(s), fewer than the %d-trial floor -- a single-shot probe is not a measured rate", path, r.InvariantID, r.Model, len(r.Trials), MinTrials)
		}
	}
	return &doc, nil
}
