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
type Entry struct {
	ID          string `json:"id"`
	Statement   string `json:"statement"`
	Impact      int    `json:"impact"`
	Urgency     int    `json:"urgency"`
	Criticality int    `json:"criticality"`
	Added       string `json:"added"` // RFC 3339
}

// Document is the on-disk shape of a ledger's canonical JSON file: a schema tag plus every
// entry ever added, in append order.
type Document struct {
	Schema  string  `json:"schema"`
	Entries []Entry `json:"entries"`
}
