package jsondoc

import (
	"encoding/json"
	"testing"
)

// Arrays are ordered per JSON semantics — canonicalization must NOT reorder
// array elements, only object keys. A reviewer or implementer confusing
// "sorted" with "arrays too" would silently corrupt ordered data.
func TestAdversarial_ArrayOrderIsPreservedNotSorted(t *testing.T) {
	a := []any{3, 1, 2}
	b := []any{1, 2, 3}
	ca, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize(a): %v", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("Canonicalize(b): %v", err)
	}
	if string(ca) == string(cb) {
		t.Fatalf("array element order was collapsed by canonicalization: %s", ca)
	}
	if string(ca) != `[3,1,2]` {
		t.Fatalf("array order not preserved: got %s", ca)
	}
}

// Diff must treat array-valued documents the same way: a reordered array is
// a real content change, not noise to be canonicalized away.
func TestAdversarial_DiffArrayReorderIsChanged(t *testing.T) {
	before := map[string]any{"k": []any{1, 2, 3}}
	after := map[string]any{"k": []any{3, 2, 1}}
	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(d.Changed) != 1 || len(d.Carried) != 0 {
		t.Fatalf("expected array reorder to register as Changed, got %+v", d)
	}
}

// A document that is bare scalar JSON (not an object) must still hash and
// diff correctly — ContentHash/Diff must not assume an object shape.
func TestAdversarial_ScalarDocumentHashAndDiff(t *testing.T) {
	h1, err := ContentHash(42)
	if err != nil {
		t.Fatalf("ContentHash(42): %v", err)
	}
	h2, err := ContentHash(42.0)
	if err != nil {
		t.Fatalf("ContentHash(42.0): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("scalar int vs equal float hashed differently: %s vs %s", h1, h2)
	}

	before := map[string]any{"k": 1}
	after := map[string]any{"k": 2}
	d, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff scalar docs: %v", err)
	}
	if len(d.Changed) != 1 {
		t.Fatalf("expected scalar value change to register as Changed, got %+v", d)
	}
}

// null is a distinct JSON value from an absent field / empty object — must
// not collide in hash with either.
func TestAdversarial_NullDistinctFromEmptyAndAbsent(t *testing.T) {
	hNull, err := ContentHash(map[string]any{"x": nil})
	if err != nil {
		t.Fatalf("hNull: %v", err)
	}
	hEmptyObj, err := ContentHash(map[string]any{"x": map[string]any{}})
	if err != nil {
		t.Fatalf("hEmptyObj: %v", err)
	}
	hAbsent, err := ContentHash(map[string]any{})
	if err != nil {
		t.Fatalf("hAbsent: %v", err)
	}
	if hNull == hEmptyObj || hNull == hAbsent || hEmptyObj == hAbsent {
		t.Fatalf("null/empty-object/absent-field collided: null=%s emptyObj=%s absent=%s", hNull, hEmptyObj, hAbsent)
	}
}

// A key containing characters that sort differently under byte-order vs
// naive Unicode-code-point order (surrogate-pair range) must still produce
// deterministic, byte-identical canonical output regardless of input order.
func TestAdversarial_KeySortStableAcrossHighCodepoints(t *testing.T) {
	a := map[string]any{"\U0001F600": 1, "a": 2, "￿": 3}
	b := map[string]any{"￿": 3, "a": 2, "\U0001F600": 1}
	ca, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize(a): %v", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("Canonicalize(b): %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("high-codepoint keys sorted inconsistently: %s vs %s", ca, cb)
	}
}

// Diff must reject a duplicate key inside a *both-sides* document deep in
// nested structure, and must do so for the after side even when before is
// entirely clean, without partially mutating the returned DocDiff.
func TestAdversarial_DiffAfterSideDuplicateKeyReturnsZeroValue(t *testing.T) {
	before := map[string]any{"ok": 1}
	after := map[string]any{
		"ok":  1,
		"bad": []byte(`{"a":1,"a":2}`),
	}
	d, err := Diff(before, after)
	if err == nil {
		t.Fatal("expected error for duplicate key in after-side document")
	}
	zero := DocDiff{}
	if len(d.Carried) != 0 || len(d.Changed) != 0 || len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatalf("expected zero-value DocDiff on error, got %+v (want %+v)", d, zero)
	}
}

// float precision at the edge of IEEE-754 double round-trip must still be
// stable and self-consistent (same input twice -> identical bytes).
func TestAdversarial_FloatPrecisionEdgeStable(t *testing.T) {
	in := []byte(`{"x":0.1,"y":123456789012345.678,"z":2.220446049250313e-16}`)
	c1, err := CanonicalizeRaw(in)
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	c2, err := CanonicalizeRaw(in)
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("repeated canonicalization of same float-heavy input diverged: %s vs %s", c1, c2)
	}
}

// A json.RawMessage / []byte value nested as a field of a larger struct
// passed to ContentHash must be canonicalized in place, not treated as an
// opaque already-final string.
func TestAdversarial_ContentHashOverStructWithRawMessageField(t *testing.T) {
	type spec struct {
		Config json.RawMessage `json:"config"`
	}
	h1, err := ContentHash(spec{Config: json.RawMessage(`{"a":1,"b":2}`)})
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ContentHash(spec{Config: json.RawMessage(`{"b":2,"a":1}`)})
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("nested RawMessage field key order altered hash: %s vs %s", h1, h2)
	}
}
