package roster

import "fmt"

// Lookup resolves id to its full roster row. id is normalized first (see normalize), so a
// dated transcript ID or one carrying a [1m] window selector resolves the same as its bare
// pinned form.
//
// A PackagingDefectError from a failed embed load takes precedence over everything else. A
// declared dispatch sentinel (e.g. "inherit") returns SentinelError, never a Model and never
// StaleError. Anything else absent from the roster returns StaleError naming the refresh
// action — never a fallback to "below floor" and never a silent pass.
func Lookup(id string) (Model, error) {
	doc, err := loadedDocument()
	if err != nil {
		return Model{}, err
	}
	norm, _ := normalize(id)
	if doc.isSentinel(norm) {
		return Model{}, &SentinelError{ID: norm}
	}
	r, ok := doc.Models[norm]
	if !ok {
		return Model{}, &StaleError{Query: norm, Reason: fmt.Sprintf("%q is not in the model roster", norm)}
	}
	return r.toModel(norm), nil
}

// EffortAvailable returns the effort levels a plan may pin for id, low to max. A model marked
// effort_exempt in the roster, and every declared dispatch sentinel (effort checks skip both),
// resolve to AllEfforts rather than the row's literal (possibly narrower or absent)
// effort_available list — exemption means every level is accepted, not that none is.
func EffortAvailable(id string) ([]Effort, error) {
	doc, err := loadedDocument()
	if err != nil {
		return nil, err
	}
	norm, _ := normalize(id)
	if doc.isSentinel(norm) {
		return AllEfforts, nil
	}
	r, ok := doc.Models[norm]
	if !ok {
		return nil, &StaleError{Query: norm, Reason: fmt.Sprintf("%q is not in the model roster", norm)}
	}
	if r.EffortExempt {
		return AllEfforts, nil
	}
	m := r.toModel(norm)
	return m.EffortAvailable, nil
}

// Selectable returns id's selection policy (new-work / legacy-pin-only / retired).
func Selectable(id string) (Selectability, error) {
	m, err := Lookup(id)
	if err != nil {
		return "", err
	}
	return m.Selectable, nil
}

// Lifecycle returns id's vendor lifecycle state (active / deprecated), independent of
// Selectable.
func Lifecycle(id string) (LifecycleState, error) {
	m, err := Lookup(id)
	if err != nil {
		return "", err
	}
	return m.Lifecycle, nil
}

// Price returns id's resolved rate table: a bare id resolves the row's own rates, an
// <id>[1m]-suffixed id resolves that row's declared "1m" context-variant rates instead. Either
// form prefers the contract basis over list whenever a contract rate is present.
//
// CONTEXT-VARIANT-CONTRACT: Price has no token count to test against the variant's declared
// premium_applies_above_input_tokens threshold, so a [1m] id always resolves the variant rate —
// a caller pricing a specific turn from this must treat that as a deliberate over-estimate for
// any turn that actually stayed under the threshold, not as the exact rate that turn was billed
// at. A row with neither price basis sourced, or a [1m] id whose row declares no "1m" variant,
// resolves StaleError: there is no rate to cost it with, never a zero, free, or guessed rate.
func Price(id string) (PriceTable, error) {
	norm, variant := normalize(id)
	m, err := Lookup(norm)
	if err != nil {
		return PriceTable{}, err
	}
	bases := m.Price
	if variant != "" {
		v, ok := m.ContextVariants[variant]
		if !ok {
			return PriceTable{}, &StaleError{Query: id, Reason: fmt.Sprintf("%q declares no %q context variant", m.ID, variant)}
		}
		bases = v
	}
	if bases.Contract != nil {
		return *bases.Contract, nil
	}
	if bases.List != nil {
		return *bases.List, nil
	}
	return PriceTable{}, &StaleError{Query: m.ID, Reason: fmt.Sprintf("%q has no sourced price (contract and list are both null)", m.ID)}
}
