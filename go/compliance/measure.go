package compliance

import "time"

// BelowFloor names one model on which a fresh measurement fell below its invariant's already-
// calibrated floor -- the finding that ApplyBelowFloor turns into a release-pause entry and a
// feedback-register defect against the invariant's owner.
type BelowFloor struct {
	InvariantID   string
	Owner         string
	Model         string
	Mechanism     string
	DeclaredFloor float64
	MeasuredRate  float64
}

// Calibrated names one model on which a fresh measurement set an invariant's floor for the first
// time: the specified interim-to-enforced transition, where the floor VALUE comes from this run
// rather than from a value declared ahead of it.
type Calibrated struct {
	InvariantID string
	Model       string
	Rate        float64
}

// MeasureOutcome is what one measurement pass over a results document produced against a
// registry: every model that missed its calibrated floor, every model calibrated for the first
// time by this run, and every result this document measured that no shipped rung-4 entry (or no
// declared floor for that model) exists to receive.
type MeasureOutcome struct {
	BelowFloor []BelowFloor
	Calibrated []Calibrated
	Unmatched  []string // "invariant_id/model" pairs the registry does not declare a floor for
}

// MeasureRegistry applies every result in results to its matching shipped rung-4 entry in doc: it
// writes the measured rate onto the entry (the write-back half of rung 4), and either sets that
// model's floor for the first time (recorded as a Calibrated finding) or compares the rate
// against the already-calibrated floor (recorded as a BelowFloor finding on a miss).
// MeasurementStatus flips to StatusMeasured only once every model the entry declares a floor for
// has a measured rate -- a partial run leaves it StatusDeclaredUnmeasured, matching the registry
// schema's "measured requires a rate for every model declared."
//
// doc is mutated in place; the caller persists it with Document.Save. now stamps every entry this
// call writes a rate onto.
func MeasureRegistry(doc *Document, results *ResultsDocument, now time.Time) MeasureOutcome {
	var out MeasureOutcome
	stamp := now.UTC().Format(time.RFC3339)

	byID := make(map[string]*Entry, len(doc.Invariants))
	for _, e := range doc.RungFourShipped() {
		byID[e.ID] = e
	}

	touched := map[string]*Entry{}
	for _, r := range results.Results {
		entry, ok := byID[r.InvariantID]
		if !ok {
			out.Unmatched = append(out.Unmatched, r.InvariantID+"/"+r.Model)
			continue
		}
		floor, declared := entry.ComplianceFloors[r.Model]
		if !declared {
			out.Unmatched = append(out.Unmatched, r.InvariantID+"/"+r.Model)
			continue
		}

		rate := r.Rate()
		if entry.MeasuredRates == nil {
			entry.MeasuredRates = map[string]float64{}
		}
		entry.MeasuredRates[r.Model] = rate
		entry.MeasuredAt = stamp
		touched[entry.ID] = entry

		if floor == nil {
			set := rate
			entry.ComplianceFloors[r.Model] = &set
			out.Calibrated = append(out.Calibrated, Calibrated{InvariantID: entry.ID, Model: r.Model, Rate: rate})
			continue
		}
		if rate < *floor {
			out.BelowFloor = append(out.BelowFloor, BelowFloor{
				InvariantID:   entry.ID,
				Owner:         entry.Owner,
				Model:         r.Model,
				Mechanism:     r.Mechanism,
				DeclaredFloor: *floor,
				MeasuredRate:  rate,
			})
		}
	}

	for _, entry := range touched {
		if fullyMeasured(entry) {
			entry.MeasurementStatus = StatusMeasured
		}
	}
	return out
}

// fullyMeasured reports whether e has a measured rate for every model it declares a floor for --
// the registry schema's requirement for a rung-4 entry to carry StatusMeasured.
func fullyMeasured(e *Entry) bool {
	if len(e.ComplianceFloors) == 0 {
		return false
	}
	for model := range e.ComplianceFloors {
		if _, ok := e.MeasuredRates[model]; !ok {
			return false
		}
	}
	return true
}
