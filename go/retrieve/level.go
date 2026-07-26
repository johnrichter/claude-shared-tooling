package retrieve

// Level selects retrieval granularity, coarse to fine.
type Level string

const (
	LevelOutline Level = "outline"
	LevelGroup   Level = "group"
	LevelItem    Level = "item"
	LevelField   Level = "field"
)

// Valid reports whether l is one of the four defined levels. The set is
// closed so a caller validating user input (e.g. a CLI flag) can do so
// exhaustively without duplicating the level list.
func (l Level) Valid() bool {
	switch l {
	case LevelOutline, LevelGroup, LevelItem, LevelField:
		return true
	}
	return false
}

// Entry is the outline/group projection of one group or item: identity only,
// never the full record — that's what the item and field levels are for.
type Entry struct {
	ID   string
	Name string
}

// GroupView is the group-level projection: the group's own identity plus its
// items flattened to outline entries.
type GroupView struct {
	ID    string
	Name  string
	Items []Entry
}

// FieldResult is the field-level projection: exactly one named field of one
// group or item, in a self-describing envelope so the caller never has to
// guess the value's shape from the field name alone.
type FieldResult struct {
	ID    string
	Field string
	Value any
}

// Taxonomy is a caller-declared description of a document's hierarchy: how
// to enumerate its groups, how to enumerate a group's items, and how to read
// each one's id and display name. Retrieve drives every level of detail off
// these functions plus reflection over Group/Item struct tags for field
// access — the taxonomy is the only place a caller's schema knowledge lives,
// so a new field or entity kind never requires a matching case here.
type Taxonomy[Doc, Group, Item any] struct {
	Groups    func(doc Doc) []Group
	GroupID   func(g Group) string
	GroupName func(g Group) string
	Items     func(g Group) []Item
	ItemID    func(it Item) string
	ItemName  func(it Item) string
}
