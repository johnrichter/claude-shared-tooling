package jsondoc

import (
	"reflect"
	"strings"
	"testing"
)

func TestDiff_Classifications(t *testing.T) {
	before := map[string]any{
		"carried": map[string]any{"x": 1},
		"changed": map[string]any{"x": 1},
		"removed": map[string]any{"x": 1},
	}
	after := map[string]any{
		"carried": map[string]any{"x": 1},
		"changed": map[string]any{"x": 2},
		"added":   map[string]any{"x": 1},
	}

	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	if len(d.Carried) != 1 || d.Carried[0].Key != "carried" {
		t.Fatalf("Carried = %+v, want one entry keyed 'carried'", d.Carried)
	}
	if len(d.Changed) != 1 || d.Changed[0].Key != "changed" {
		t.Fatalf("Changed = %+v, want one entry keyed 'changed'", d.Changed)
	}
	if d.Changed[0].OldHash == d.Changed[0].NewHash {
		t.Fatalf("Changed entry has identical OldHash/NewHash: %+v", d.Changed[0])
	}
	if len(d.Added) != 1 || d.Added[0].Key != "added" {
		t.Fatalf("Added = %+v, want one entry keyed 'added'", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0].Key != "removed" {
		t.Fatalf("Removed = %+v, want one entry keyed 'removed'", d.Removed)
	}
}

func TestDiff_EmptyBothSides(t *testing.T) {
	d, err := Diff(map[string]any{}, map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(d.Carried)+len(d.Changed)+len(d.Added)+len(d.Removed) != 0 {
		t.Fatalf("expected empty DocDiff, got %+v", d)
	}
}

func TestDiff_NilMapsTreatedAsEmpty(t *testing.T) {
	d, err := Diff(nil, nil)
	if err != nil {
		t.Fatalf("Diff(nil, nil): %v", err)
	}
	if len(d.Carried)+len(d.Changed)+len(d.Added)+len(d.Removed) != 0 {
		t.Fatalf("expected empty DocDiff for nil/nil, got %+v", d)
	}
}

func TestDiff_MissingBeforeIsAllAdded(t *testing.T) {
	after := map[string]any{"a": 1, "b": 2}
	d, err := Diff(nil, after)
	if err != nil {
		t.Fatalf("Diff(nil, after): %v", err)
	}
	if len(d.Added) != 2 || len(d.Carried) != 0 || len(d.Changed) != 0 || len(d.Removed) != 0 {
		t.Fatalf("expected all-added for missing before, got %+v", d)
	}
}

func TestDiff_MissingAfterIsAllRemoved(t *testing.T) {
	before := map[string]any{"a": 1, "b": 2}
	d, err := Diff(before, nil)
	if err != nil {
		t.Fatalf("Diff(before, nil): %v", err)
	}
	if len(d.Removed) != 2 || len(d.Carried) != 0 || len(d.Changed) != 0 || len(d.Added) != 0 {
		t.Fatalf("expected all-removed for missing after, got %+v", d)
	}
}

func TestDiff_SortedOutput(t *testing.T) {
	before := map[string]any{}
	after := map[string]any{
		"zebra": 1, "apple": 2, "mango": 3,
	}
	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got := make([]string, len(d.Added))
	for i, e := range d.Added {
		got[i] = e.Key
	}
	want := []string{"apple", "mango", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Added keys = %v, want sorted %v", got, want)
	}
}

func TestDiff_SortedOutputAllBuckets(t *testing.T) {
	// Multiple entries per bucket to exercise the sort comparator on every
	// slice, not just the trivial single-element case.
	before := map[string]any{
		"zebra-carry": 1, "apple-carry": 1,
		"zebra-change": 1, "apple-change": 1,
		"zebra-remove": 1, "apple-remove": 1,
	}
	after := map[string]any{
		"zebra-carry": 1, "apple-carry": 1,
		"zebra-change": 2, "apple-change": 2,
		"zebra-add": 1, "apple-add": 1,
	}
	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	carriedKeys := make([]string, len(d.Carried))
	for i, e := range d.Carried {
		carriedKeys[i] = e.Key
	}
	if !reflect.DeepEqual(carriedKeys, []string{"apple-carry", "zebra-carry"}) {
		t.Fatalf("Carried keys not sorted: %v", carriedKeys)
	}

	changedKeys := make([]string, len(d.Changed))
	for i, e := range d.Changed {
		changedKeys[i] = e.Key
	}
	if !reflect.DeepEqual(changedKeys, []string{"apple-change", "zebra-change"}) {
		t.Fatalf("Changed keys not sorted: %v", changedKeys)
	}
}

func TestDiff_KeyOrderAndWhitespaceDoNotCauseFalseChange(t *testing.T) {
	before := map[string]any{
		"k": map[string]any{"a": 1, "b": 2},
	}
	after := map[string]any{
		"k": map[string]any{"b": 2, "a": 1}, // same content, different Go map insertion irrelevant, but also verify raw JSON whitespace variance
	}
	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(d.Changed) != 0 || len(d.Carried) != 1 {
		t.Fatalf("expected carried (not changed) for reordered-but-equal doc, got %+v", d)
	}

	before2 := map[string]any{"k": []byte(`{"a":1,"b":2}`)}
	after2 := map[string]any{"k": []byte("{\n  \"b\" : 2,\n  \"a\" : 1\n}")}
	d2, err := Diff(before2, after2)
	if err != nil {
		t.Fatalf("Diff (raw json whitespace): %v", err)
	}
	if len(d2.Changed) != 0 || len(d2.Carried) != 1 {
		t.Fatalf("expected carried for whitespace-different-but-equal raw JSON, got %+v", d2)
	}
}

func TestDiff_DuplicateKeyInDocumentErrors(t *testing.T) {
	before := map[string]any{"k": []byte(`{"a":1,"a":2}`)}
	_, err := Diff(before, map[string]any{})
	if err == nil {
		t.Fatal("expected error for duplicate-key document in before, got nil")
	}

	after := map[string]any{"k": []byte(`{"a":1,"a":2}`)}
	_, err = Diff(map[string]any{}, after)
	if err == nil {
		t.Fatal("expected error for duplicate-key document in after, got nil")
	}
}

func TestDiff_ErrorNamesOffendingKey(t *testing.T) {
	before := map[string]any{"bad-key": []byte(`{"a":1,"a":2}`)}
	_, err := Diff(before, map[string]any{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error should identify which key's document failed, per doc comment.
	if got := err.Error(); !strings.Contains(got, "bad-key") {
		t.Fatalf("error %q does not name offending key 'bad-key'", got)
	}
}
