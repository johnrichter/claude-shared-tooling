package retrieve

import (
	"reflect"
	"testing"
)

// TestRecomputedNotCached checks that Retrieve reflects the doc passed on
// THIS call, not a snapshot from an earlier call — i.e. no caching/memoization.
func TestRecomputedNotCached(t *testing.T) {
	d := sampleDoc()
	tax := testTaxonomy()

	got1, err := Retrieve(d, tax, LevelItem, "i1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got1.(itm).Name != "Item One" {
		t.Fatalf("unexpected first read: %+v", got1)
	}

	// Mutate the source doc between calls.
	d.Groups[0].Items[0].Name = "Renamed"
	d.Groups[0].Items = append(d.Groups[0].Items, itm{ID: "i3", Name: "Item Three"})

	got2, err := Retrieve(d, tax, LevelItem, "i1", "")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if got2.(itm).Name != "Renamed" {
		t.Fatalf("second call did not reflect mutated doc — looks cached: %+v", got2)
	}

	outline, err := Retrieve(d, tax, LevelOutline, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outline.([]Entry)) != 4 { // 1 group + 3 items
		t.Fatalf("outline did not pick up newly added item — looks cached: %+v", outline)
	}
}

// TestGroupViewIsIndependentOfSourceMutation checks that a group-level
// result's Items slice is not aliased to the source doc's backing array.
func TestGroupViewIsIndependentOfSourceMutation(t *testing.T) {
	d := sampleDoc()
	got, err := Retrieve(d, testTaxonomy(), LevelGroup, "g1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gv := got.(GroupView)
	before := len(gv.Items)

	d.Groups[0].Items[0].Name = "mutated after retrieve"
	if gv.Items[0].Name == "mutated after retrieve" {
		t.Fatal("group view Entry aliases source doc field")
	}
	if len(gv.Items) != before {
		t.Fatalf("group view item count changed unexpectedly: %d -> %d", before, len(gv.Items))
	}
}

// TestFieldAccessOnGroup checks field-level retrieval resolves against a
// group (not just an item) when the id names a group.
func TestFieldAccessOnGroup(t *testing.T) {
	got, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "g1", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fr := got.(FieldResult)
	if fr.Value != "Group One" {
		t.Fatalf("want Group One, got %v", fr.Value)
	}
}

// TestFieldByTagFallsBackToGoName checks a struct field with no json tag is
// still addressable by its Go field name, per FieldByTag's documented fallback.
func TestFieldByTagFallsBackToGoName(t *testing.T) {
	v, ok := FieldByTag(itm{ID: "i9"}, "ID")
	if !ok || v != "i9" {
		t.Fatalf("want (i9,true) for untagged Go-name fallback, got (%v,%v)", v, ok)
	}
}

// TestFieldByTagExcludesDashTag checks a field tagged json:"-" is not
// reachable via FieldByTag even if the caller asks for its literal "-" name
// or its Go field name.
func TestFieldByTagExcludesDashTag(t *testing.T) {
	type withHidden struct {
		Secret string `json:"-"`
	}
	if _, ok := FieldByTag(withHidden{Secret: "s"}, "-"); ok {
		t.Fatal("want json:\"-\" field unreachable via its literal tag value")
	}
	if _, ok := FieldByTag(withHidden{Secret: "s"}, "Secret"); ok {
		t.Fatal("want json:\"-\" field unreachable via its Go name once explicitly hidden")
	}
}

// TestFieldByTagOnNonNilPointer checks a non-nil pointer input is
// dereferenced transparently -- the caller shouldn't have to pass the
// pointee by value to reach its fields.
func TestFieldByTagOnNonNilPointer(t *testing.T) {
	it := &itm{ID: "i9", Name: "Nine"}
	v, ok := FieldByTag(it, "name")
	if !ok || v != "Nine" {
		t.Fatalf("want (Nine,true) for non-nil pointer input, got (%v,%v)", v, ok)
	}
}

// TestFieldByTagOnNilPointer checks a nil pointer input is handled gracefully.
func TestFieldByTagOnNilPointer(t *testing.T) {
	var p *itm
	if _, ok := FieldByTag(p, "name"); ok {
		t.Fatal("want false for nil pointer input, not a panic or spurious value")
	}
}

// TestFieldByTagOnNonStruct checks a non-struct input is handled gracefully.
func TestFieldByTagOnNonStruct(t *testing.T) {
	if _, ok := FieldByTag(42, "name"); ok {
		t.Fatal("want false for non-struct input")
	}
	if _, ok := FieldByTag("a string", "name"); ok {
		t.Fatal("want false for non-struct input")
	}
}

// TestDeepCopyUnexportedFieldSkipsRecursion checks a struct with an
// unexported field round-trips via DeepCopy without panicking: the
// whole-value copy preserves it, but per the documented limitation it isn't
// separately recursed into.
func TestDeepCopyUnexportedFieldSkipsRecursion(t *testing.T) {
	type withUnexported struct {
		Visible string
		hidden  int
	}
	src := withUnexported{Visible: "v", hidden: 7}
	cp := DeepCopy(src)
	if cp.Visible != "v" {
		t.Fatalf("want exported field preserved, got %q", cp.Visible)
	}
	if cp.hidden != 7 {
		t.Fatalf("want unexported field preserved via whole-value copy, got %d", cp.hidden)
	}
}

// TestDeepCopyMap checks map values are copied independently, including
// nested slices held inside the map.
func TestDeepCopyMap(t *testing.T) {
	type withMap struct {
		M map[string][]int
	}
	src := withMap{M: map[string][]int{"a": {1, 2, 3}}}
	cp := DeepCopy(src)
	cp.M["a"][0] = 99
	cp.M["b"] = []int{7}

	if src.M["a"][0] == 99 {
		t.Fatal("deep copy of map value slice aliases source")
	}
	if _, ok := src.M["b"]; ok {
		t.Fatal("adding a key to the copy's map leaked into source map")
	}
}

// TestDeepCopyPointerAndNil checks pointer fields are copied to a new
// address, and nil pointers/slices/maps round-trip as nil rather than being
// turned into non-nil empty values.
func TestDeepCopyPointerAndNil(t *testing.T) {
	type inner struct{ N int }
	type withPtr struct {
		P *inner
		S []int
		M map[string]int
	}
	n := inner{N: 1}
	src := withPtr{P: &n}
	cp := DeepCopy(src)
	if cp.P == src.P {
		t.Fatal("pointer field not deep-copied — same address")
	}
	cp.P.N = 2
	if src.P.N == 2 {
		t.Fatal("mutating copy's pointee reached source")
	}
	if cp.S != nil {
		t.Fatalf("nil slice field should stay nil after deep copy, got %#v", cp.S)
	}
	if cp.M != nil {
		t.Fatalf("nil map field should stay nil after deep copy, got %#v", cp.M)
	}
}

// TestDeepCopyOfRetrievedItemSlice checks that the Tags slice returned from
// an item-level fetch has its own distinct backing array (append-safety),
// not merely a distinct outer slice header sharing the source's array.
func TestDeepCopyOfRetrievedItemSlice(t *testing.T) {
	d := sampleDoc()
	got, err := Retrieve(d, testTaxonomy(), LevelItem, "i1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	it := got.(itm)
	if reflect.ValueOf(it.Tags).Pointer() == reflect.ValueOf(d.Groups[0].Items[0].Tags).Pointer() {
		t.Fatal("retrieved item's slice field shares backing array with source doc")
	}
}

// TestEmptyGroupNoItems checks a group with zero items projects cleanly at
// both group and outline level rather than erroring or panicking.
func TestEmptyGroupNoItems(t *testing.T) {
	d := doc{Groups: []grp{{ID: "g2", Name: "Empty Group"}}}
	got, err := Retrieve(d, testTaxonomy(), LevelGroup, "g2", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gv := got.(GroupView)
	if gv.ID != "g2" || len(gv.Items) != 0 {
		t.Fatalf("unexpected group view for empty group: %+v", gv)
	}

	out, err := Retrieve(d, testTaxonomy(), LevelOutline, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.([]Entry)) != 1 {
		t.Fatalf("want 1 outline entry (group only), got %+v", out)
	}
}

// TestFieldOnMissingIDNotConfusedWithFound checks that when neither an item
// nor a group resolves the id, the error names the id and does not silently
// fall through with a zero value.
func TestFieldOnMissingIDNotConfusedWithFound(t *testing.T) {
	_, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "does-not-exist", "name")
	if err == nil {
		t.Fatal("want error for field lookup against an id that resolves to neither item nor group")
	}
}

// TestLevelValid checks the closed set of defined levels and rejects
// anything outside it.
func TestLevelValid(t *testing.T) {
	for _, l := range []Level{LevelOutline, LevelGroup, LevelItem, LevelField} {
		if !l.Valid() {
			t.Fatalf("want %q valid", l)
		}
	}
	if Level("bogus").Valid() {
		t.Fatal("want unknown level invalid")
	}
	if Level("").Valid() {
		t.Fatal("want empty level invalid")
	}
}

// TestDeepCopyInvalidValue checks that DeepCopy on a nil `any` (the zero
// reflect.Value case) returns the input rather than panicking.
func TestDeepCopyInvalidValue(t *testing.T) {
	var v any
	if got := DeepCopy(v); got != nil {
		t.Fatalf("want nil round-trip for nil any, got %v", got)
	}
}

// TestGroupLevelRequiresID checks group-level retrieval rejects an empty id.
func TestGroupLevelRequiresID(t *testing.T) {
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelGroup, "", ""); err == nil {
		t.Fatal("want error for empty id at group level")
	}
}

// TestOutlineRejectsID checks outline takes no id: passing one is an error
// rather than being silently ignored.
func TestOutlineRejectsID(t *testing.T) {
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelOutline, "g1", ""); err == nil {
		t.Fatal("want error for id passed to outline level")
	}
}

// TestFieldLevelRequiresIDAndField checks field-level retrieval rejects an
// empty id and, separately, an empty field name, rather than falling through
// to a not-found lookup with a blank key.
func TestFieldLevelRequiresIDAndField(t *testing.T) {
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "", "name"); err == nil {
		t.Fatal("want error for empty id at field level")
	}
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "i1", ""); err == nil {
		t.Fatal("want error for empty field name at field level")
	}
}

// TestGroupFieldNotFound checks a field-level lookup that resolves to a
// group (not an item) but names a field the group doesn't have still errors,
// instead of silently returning the item-not-found branch's zero value.
func TestGroupFieldNotFound(t *testing.T) {
	_, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "g1", "no-such-field")
	if err == nil {
		t.Fatal("want error for group field that doesn't exist")
	}
}

// TestDeepCopyNilPointerField checks a nil pointer nested inside a struct
// field round-trips as nil rather than being dereferenced into a panic.
func TestDeepCopyNilPointerField(t *testing.T) {
	type inner struct{ N int }
	type withNilPtr struct{ P *inner }
	cp := DeepCopy(withNilPtr{P: nil})
	if cp.P != nil {
		t.Fatalf("want nil pointer field to stay nil, got %#v", cp.P)
	}
}

// TestDeepCopyInterfaceField checks an interface-typed struct field --
// nil and holding a pointer -- is deep-copied independently of the source,
// and a nil interface field stays nil.
func TestDeepCopyInterfaceField(t *testing.T) {
	type inner struct{ N int }
	type withIface struct{ V any }

	// Nil interface field.
	cpNil := DeepCopy(withIface{V: nil})
	if cpNil.V != nil {
		t.Fatalf("want nil interface field to stay nil, got %#v", cpNil.V)
	}

	// Interface field holding a pointer: copy must not alias the pointee.
	n := &inner{N: 1}
	src := withIface{V: n}
	cp := DeepCopy(src)
	got, ok := cp.V.(*inner)
	if !ok {
		t.Fatalf("want *inner interface value to round-trip as *inner, got %T", cp.V)
	}
	if got == n {
		t.Fatal("interface field holding a pointer was not deep-copied — same address")
	}
	got.N = 99
	if n.N == 99 {
		t.Fatal("mutating copy's interface-held pointee reached source")
	}
}

// TestDeepCopyArrayField checks a fixed-size array field is copied
// element-by-element, independent of the source array.
func TestDeepCopyArrayField(t *testing.T) {
	type withArray struct{ A [3]int }
	src := withArray{A: [3]int{1, 2, 3}}
	cp := DeepCopy(src)
	cp.A[0] = 99
	if src.A[0] == 99 {
		t.Fatal("array field mutation on copy reached source array")
	}
}

// TestFieldByTagUnexportedFieldUnreachable checks an unexported struct field
// -- matched via its Go-name fallback since it carries no json tag -- cannot
// be read out through FieldByTag, since reflection cannot legally interface
// an unexported value.
func TestFieldByTagUnexportedFieldUnreachable(t *testing.T) {
	type withUnexported struct {
		secret string
	}
	v := withUnexported{secret: "s"}
	if val, ok := FieldByTag(v, "secret"); ok {
		t.Fatalf("want unexported field unreachable via FieldByTag, got (%v,true)", val)
	}
}

// TestRetrieveWithPointerTaxonomyTypes checks Retrieve works when Doc/Group/
// Item are pointer types in the taxonomy's type parameters, not just values --
// the generic machinery must not assume value semantics.
func TestRetrieveWithPointerTaxonomyTypes(t *testing.T) {
	type pitem struct {
		ID   string
		Name string `json:"name"`
	}
	type pgroup struct {
		ID    string
		Name  string `json:"name"`
		Items []*pitem
	}
	type pdoc struct {
		Groups []*pgroup
	}
	tax := Taxonomy[*pdoc, *pgroup, *pitem]{
		Groups:    func(d *pdoc) []*pgroup { return d.Groups },
		GroupID:   func(g *pgroup) string { return g.ID },
		GroupName: func(g *pgroup) string { return g.Name },
		Items:     func(g *pgroup) []*pitem { return g.Items },
		ItemID:    func(it *pitem) string { return it.ID },
		ItemName:  func(it *pitem) string { return it.Name },
	}
	d := &pdoc{Groups: []*pgroup{{ID: "g1", Name: "G1", Items: []*pitem{{ID: "i1", Name: "I1"}}}}}

	got, err := Retrieve(d, tax, LevelItem, "i1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	it := got.(*pitem)
	if it == d.Groups[0].Items[0] {
		t.Fatal("pointer-typed item result aliases source doc's pointer — not a deep copy")
	}
	it.Name = "mutated"
	if d.Groups[0].Items[0].Name == "mutated" {
		t.Fatal("mutating pointer-typed item result leaked into source doc")
	}
}
