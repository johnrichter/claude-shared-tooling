package retrieve

import "fmt"

// Retrieve projects doc at the requested level, per tax's declared
// hierarchy. id is required for group/item/field; field is additionally
// required for field. Every call walks tax.Groups(doc) fresh — nothing is
// cached — so the result always reflects doc as passed in on this call.
//
//   - outline: every group and item, id+name only, in tax.Groups/tax.Items
//     traversal order. Takes no id.
//   - group: one group's id/name plus its items at outline granularity.
//   - item: one item's full record, deep-copied.
//   - field: one named field of one group or item (group.ID/item.ID first),
//     via FieldByTag — no hand-maintained per-entity field switch.
//
// A doc with no groups (or a group with no items) is not an error: outline
// returns an empty result and group/item/field return a "not found" error
// only when the specific id being asked for doesn't resolve.
func Retrieve[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], level Level, id, field string) (any, error) {
	switch level {
	case LevelOutline:
		if id != "" {
			return nil, fmt.Errorf("retrieve: level %q does not take an id", LevelOutline)
		}
		return outline(doc, tax), nil
	case LevelGroup:
		return groupView(doc, tax, id)
	case LevelItem:
		return itemView(doc, tax, id)
	case LevelField:
		return fieldView(doc, tax, id, field)
	default:
		return nil, fmt.Errorf("retrieve: unknown level %q (want %s|%s|%s|%s)", level, LevelOutline, LevelGroup, LevelItem, LevelField)
	}
}

func findGroup[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], id string) (Group, bool) {
	for _, g := range tax.Groups(doc) {
		if tax.GroupID(g) == id {
			return g, true
		}
	}
	var zero Group
	return zero, false
}

func findItem[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], id string) (Item, bool) {
	for _, g := range tax.Groups(doc) {
		for _, it := range tax.Items(g) {
			if tax.ItemID(it) == id {
				return it, true
			}
		}
	}
	var zero Item
	return zero, false
}

func outline[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item]) []Entry {
	var out []Entry
	for _, g := range tax.Groups(doc) {
		out = append(out, Entry{ID: tax.GroupID(g), Name: tax.GroupName(g)})
		for _, it := range tax.Items(g) {
			out = append(out, Entry{ID: tax.ItemID(it), Name: tax.ItemName(it)})
		}
	}
	return out
}

func groupView[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], id string) (GroupView, error) {
	if id == "" {
		return GroupView{}, fmt.Errorf("retrieve: level %q requires an id", LevelGroup)
	}
	g, ok := findGroup(doc, tax, id)
	if !ok {
		return GroupView{}, fmt.Errorf("retrieve: group %q not found", id)
	}
	gv := GroupView{ID: tax.GroupID(g), Name: tax.GroupName(g)}
	for _, it := range tax.Items(g) {
		gv.Items = append(gv.Items, Entry{ID: tax.ItemID(it), Name: tax.ItemName(it)})
	}
	return gv, nil
}

func itemView[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], id string) (Item, error) {
	var zero Item
	if id == "" {
		return zero, fmt.Errorf("retrieve: level %q requires an id", LevelItem)
	}
	it, ok := findItem(doc, tax, id)
	if !ok {
		return zero, fmt.Errorf("retrieve: item %q not found", id)
	}
	return DeepCopy(it), nil
}

func fieldView[Doc, Group, Item any](doc Doc, tax Taxonomy[Doc, Group, Item], id, field string) (FieldResult, error) {
	if id == "" || field == "" {
		return FieldResult{}, fmt.Errorf("retrieve: level %q requires an id and a field", LevelField)
	}
	if it, ok := findItem(doc, tax, id); ok {
		v, ok := FieldByTag(it, field)
		if !ok {
			return FieldResult{}, fmt.Errorf("retrieve: item %q has no field %q", id, field)
		}
		return FieldResult{ID: id, Field: field, Value: v}, nil
	}
	if g, ok := findGroup(doc, tax, id); ok {
		v, ok := FieldByTag(g, field)
		if !ok {
			return FieldResult{}, fmt.Errorf("retrieve: group %q has no field %q", id, field)
		}
		return FieldResult{ID: id, Field: field, Value: v}, nil
	}
	return FieldResult{}, fmt.Errorf("retrieve: %q not found", id)
}
