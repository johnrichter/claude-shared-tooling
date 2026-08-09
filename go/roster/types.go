package roster

// document is the on-disk shape of model-roster.json, decoded verbatim from its JSON keys.
// Only the fields a projection in this package reads are declared; the schema's provenance
// fields (_as_of, _source, _unit, _cross_family_order) carry no decision and are not modeled
// here.
type document struct {
	SchemaVersion         int            `json:"_schema_version"`
	EffortExemptSentinels []string       `json:"effort_exempt_sentinels"`
	Models                map[string]row `json:"models"`
}

// isSentinel reports whether id (already normalized) is a declared dispatch sentinel rather
// than a model — read from the document itself, never a hardcoded literal, so a roster refresh
// that adds a sentinel needs no code change here.
func (d *document) isSentinel(id string) bool {
	for _, s := range d.EffortExemptSentinels {
		if s == id {
			return true
		}
	}
	return false
}

// row is one entry of the roster's "models" map, decoded verbatim from its JSON keys.
type row struct {
	Family             string                    `json:"family"`
	Generation         []int                     `json:"generation"`
	ReleaseDate        *string                   `json:"release_date"`
	ContextWindow      *int                      `json:"context_window"`
	MaxOutputTokens    *int                      `json:"max_output_tokens"`
	KnowledgeCutoff    *string                   `json:"knowledge_cutoff"`
	MinCacheablePrefix *int                      `json:"min_cacheable_prefix"`
	BatchDiscount      *float64                  `json:"batch_discount"`
	Price              rowPrice                  `json:"price"`
	EffortAvailable    []string                  `json:"effort_available"`
	EffortExempt       bool                      `json:"effort_exempt"`
	Lifecycle          string                    `json:"lifecycle"`
	DeprecationDate    *string                   `json:"deprecation_date"`
	Selectable         string                    `json:"selectable"`
	CrossFamilyRank    *int                      `json:"cross_family_rank"`
	Notes              string                    `json:"notes,omitempty"`
	ContextVariants    map[string]contextVariant `json:"context_variants,omitempty"`
}

// contextVariant is one entry of a row's "context_variants" map (currently only "1m"), decoded
// verbatim from its JSON keys. Only price is modeled: context_window and
// premium_applies_above_input_tokens carry no decision any projection in this package reads
// today — Price resolves the variant's rate table but has no token count to test the threshold
// against (see CONTEXT-VARIANT-CONTRACT at Price's call sites).
type contextVariant struct {
	Price rowPrice `json:"price"`
}

type rowPrice struct {
	Contract *priceRates `json:"contract"`
	List     *priceRates `json:"list"`
}

type priceRates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
}

// Effort is a plan-pinnable effort level, per model-roster.schema.json's effort enum.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// AllEfforts is every effort level the schema defines, low to max. EffortAvailable returns
// this for a model that is effort-exempt or for a dispatch sentinel: exemption means every
// level is accepted, not that none is.
var AllEfforts = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// LifecycleState is the vendor's active/deprecated state for a model, independent of
// Selectability.
type LifecycleState string

const (
	LifecycleActive     LifecycleState = "active"
	LifecycleDeprecated LifecycleState = "deprecated"
)

// Selectability is this workspace's selection policy for a model, independent of
// LifecycleState.
type Selectability string

const (
	SelectableNewWork       Selectability = "new-work"
	SelectableLegacyPinOnly Selectability = "legacy-pin-only"
	SelectableRetired       Selectability = "retired"
)

// PriceTable is one basis's per-million-token rates. Basis names which basis was resolved
// ("contract" or "list") so a caller never has to re-derive which one it got.
type PriceTable struct {
	Basis        string
	Input        float64
	Output       float64
	CacheWrite5m float64
	CacheWrite1h float64
	CacheRead    float64
}

func (p *priceRates) toTable(basis string) PriceTable {
	return PriceTable{
		Basis:        basis,
		Input:        p.Input,
		Output:       p.Output,
		CacheWrite5m: p.CacheWrite5m,
		CacheWrite1h: p.CacheWrite1h,
		CacheRead:    p.CacheRead,
	}
}

// toBases converts one price object (both bases as authored) into the exported PriceBases shape.
// Shared by a row's own price and each of its context_variants entries, so the two can never
// diverge on how a basis becomes a *PriceTable.
func (p rowPrice) toBases() PriceBases {
	var bases PriceBases
	if p.Contract != nil {
		t := p.Contract.toTable("contract")
		bases.Contract = &t
	}
	if p.List != nil {
		t := p.List.toTable("list")
		bases.List = &t
	}
	return bases
}

// PriceBases carries both price bases of a row exactly as authored, for a caller that needs to
// know whether a contract rate exists rather than just the resolved rate. Either or both may
// be nil.
type PriceBases struct {
	Contract *PriceTable
	List     *PriceTable
}

// Model is one roster row, resolved for consumption: an id absent from the roster or a
// dispatch sentinel never reaches here — Lookup returns StaleError or SentinelError instead.
type Model struct {
	ID                 string
	Family             string
	Generation         []int
	ReleaseDate        *string
	ContextWindow      *int
	MaxOutputTokens    *int
	KnowledgeCutoff    *string
	MinCacheablePrefix *int
	BatchDiscount      *float64
	Price              PriceBases
	EffortAvailable    []Effort
	EffortExempt       bool
	Lifecycle          LifecycleState
	DeprecationDate    *string
	Selectable         Selectability
	CrossFamilyRank    *int
	Notes              string

	// ContextVariants carries each declared window variant's price bases, keyed by selector
	// ("1m"), for Price to resolve a suffixed id against instead of this row's own Price. Nil
	// when the row declares no context_variants; a caller checks presence with the map's
	// comma-ok form rather than assuming every model has one.
	ContextVariants map[string]PriceBases
}

func (r *row) toModel(id string) Model {
	efforts := make([]Effort, len(r.EffortAvailable))
	for i, e := range r.EffortAvailable {
		efforts[i] = Effort(e)
	}
	var variants map[string]PriceBases
	if len(r.ContextVariants) > 0 {
		variants = make(map[string]PriceBases, len(r.ContextVariants))
		for k, v := range r.ContextVariants {
			variants[k] = v.Price.toBases()
		}
	}
	return Model{
		ID:                 id,
		Family:             r.Family,
		Generation:         r.Generation,
		ReleaseDate:        r.ReleaseDate,
		ContextWindow:      r.ContextWindow,
		MaxOutputTokens:    r.MaxOutputTokens,
		KnowledgeCutoff:    r.KnowledgeCutoff,
		MinCacheablePrefix: r.MinCacheablePrefix,
		BatchDiscount:      r.BatchDiscount,
		Price:              r.Price.toBases(),
		EffortAvailable:    efforts,
		EffortExempt:       r.EffortExempt,
		Lifecycle:          LifecycleState(r.Lifecycle),
		DeprecationDate:    r.DeprecationDate,
		Selectable:         Selectability(r.Selectable),
		CrossFamilyRank:    r.CrossFamilyRank,
		Notes:              r.Notes,
		ContextVariants:    variants,
	}
}
