package ledger

// SchemaVersion is the current on-disk shape of a ledger's canonical JSON document. A ledger
// loaded from a file declaring any other value is refused rather than read with guessed
// semantics.
const SchemaVersion = "ledger@1.0.0"

// MinScore and MaxScore bound Impact and Urgency: a 1-5 scale is wide enough to separate
// "barely matters" from "must act", narrow enough that a caller cannot manufacture false
// precision by picking an arbitrary large number.
const (
	MinScore = 1
	MaxScore = 5
)

// Entry is one register row. ID and Criticality are always engine-derived by Add — nothing in
// this package accepts them as caller input.
//
// Resolution, Citation, Retraction, Recurrence, and RecurCycles start at their zero values —
// unresolved, uncited, never retracted, never recurred — and are only ever advanced by
// Resolve, Retract, and Recur, never set directly on a fresh Add.
//
// RecurCycles is the set of distinct planning cycles the entry has already recurred in, kept so
// Recur is idempotent per cycle even when a caller returns to an earlier cycle out of order;
// Recurrence is its count, carried as its own integer per the resolution-vocabulary contract.
type Entry struct {
	ID          string     `json:"id"`
	Statement   string     `json:"statement"`
	Impact      int        `json:"impact"`
	Urgency     int        `json:"urgency"`
	Criticality int        `json:"criticality"`
	Added       string     `json:"added"` // RFC 3339
	Resolution  Resolution `json:"resolution"`
	Citation    Citation   `json:"citation"`
	Retraction  Retraction `json:"retraction"`
	Recurrence  int        `json:"recurrence"`
	RecurCycles []string   `json:"recur_cycles"`
}

// Document is the on-disk shape of a ledger's canonical JSON file: a schema tag plus every
// entry ever added, in append order.
type Document struct {
	Schema  string  `json:"schema"`
	Entries []Entry `json:"entries"`
}
