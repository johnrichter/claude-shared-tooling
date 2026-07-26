package retrieve

import "testing"

type doc struct {
	Groups []grp
}

type grp struct {
	ID    string
	Name  string `json:"name"`
	Items []itm
}

type itm struct {
	ID   string
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func testTaxonomy() Taxonomy[doc, grp, itm] {
	return Taxonomy[doc, grp, itm]{
		Groups:    func(d doc) []grp { return d.Groups },
		GroupID:   func(g grp) string { return g.ID },
		GroupName: func(g grp) string { return g.Name },
		Items:     func(g grp) []itm { return g.Items },
		ItemID:    func(it itm) string { return it.ID },
		ItemName:  func(it itm) string { return it.Name },
	}
}

func sampleDoc() doc {
	return doc{Groups: []grp{
		{ID: "g1", Name: "Group One", Items: []itm{
			{ID: "i1", Name: "Item One", Tags: []string{"a", "b"}},
			{ID: "i2", Name: "Item Two"},
		}},
	}}
}

// TestOutline checks that outline flattens every group and item to id/name entries.
func TestOutline(t *testing.T) {
	got, err := Retrieve(sampleDoc(), testTaxonomy(), LevelOutline, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := got.([]Entry)
	if len(entries) != 3 {
		t.Fatalf("want 3 entries (1 group + 2 items), got %d", len(entries))
	}
}

// TestGroup checks that group projects one group's identity plus its items.
func TestGroup(t *testing.T) {
	got, err := Retrieve(sampleDoc(), testTaxonomy(), LevelGroup, "g1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gv := got.(GroupView)
	if gv.ID != "g1" || len(gv.Items) != 2 {
		t.Fatalf("unexpected group view: %+v", gv)
	}
}

// TestItemIsDeepCopy checks that mutating an item-level result never reaches the source doc.
func TestItemIsDeepCopy(t *testing.T) {
	d := sampleDoc()
	got, err := Retrieve(d, testTaxonomy(), LevelItem, "i1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	it := got.(itm)
	it.Tags[0] = "mutated"
	if d.Groups[0].Items[0].Tags[0] == "mutated" {
		t.Fatal("mutating retrieved item leaked into source doc — not a deep copy")
	}
}

// TestFieldIsDeepCopy checks that mutating a field-level result never reaches the source doc.
func TestFieldIsDeepCopy(t *testing.T) {
	d := sampleDoc()
	got, err := Retrieve(d, testTaxonomy(), LevelField, "i1", "tags")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fr := got.(FieldResult)
	tags := fr.Value.([]string)
	tags[0] = "mutated"
	if d.Groups[0].Items[0].Tags[0] == "mutated" {
		t.Fatal("mutating retrieved field value leaked into source doc — not a deep copy")
	}
}

// TestMissingIDAndField checks that a missing/empty id or field is a clear error, not a panic.
func TestMissingIDAndField(t *testing.T) {
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelGroup, "nope", ""); err == nil {
		t.Fatal("want error for missing group id")
	}
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelItem, "nope", ""); err == nil {
		t.Fatal("want error for missing item id")
	}
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelField, "i1", "nope"); err == nil {
		t.Fatal("want error for missing field")
	}
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), LevelItem, "", ""); err == nil {
		t.Fatal("want error for empty id at item level")
	}
}

// TestEmptyDoc checks that a doc with no groups yields an empty outline, not an error or panic.
func TestEmptyDoc(t *testing.T) {
	empty := doc{}
	got, err := Retrieve(empty, testTaxonomy(), LevelOutline, "", "")
	if err != nil {
		t.Fatalf("unexpected error on empty doc: %v", err)
	}
	if entries := got.([]Entry); len(entries) != 0 {
		t.Fatalf("want empty outline, got %d entries", len(entries))
	}
	if _, err := Retrieve(empty, testTaxonomy(), LevelItem, "i1", ""); err == nil {
		t.Fatal("want not-found error for item lookup on empty doc")
	}
}

// TestUnknownLevel checks that an undeclared Level value is a clear error.
func TestUnknownLevel(t *testing.T) {
	if _, err := Retrieve(sampleDoc(), testTaxonomy(), Level("bogus"), "", ""); err == nil {
		t.Fatal("want error for unknown level")
	}
}
