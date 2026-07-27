package jsondoc

import (
	"encoding/json"
	"testing"
)

func TestCanonicalize_KeyOrderInvariant(t *testing.T) {
	a := map[string]any{"b": 1, "a": 2, "c": 3}
	b := map[string]any{"c": 3, "b": 1, "a": 2}

	ca, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize(a): %v", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("Canonicalize(b): %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("key order changed canonical form: %s vs %s", ca, cb)
	}
	if string(ca) != `{"a":2,"b":1,"c":3}` {
		t.Fatalf("unexpected canonical form: %s", ca)
	}
}

func TestCanonicalize_WhitespaceInvariant(t *testing.T) {
	spaced := []byte(`{
		"a" : 1 ,
		"b" : 2
	}`)
	tight := []byte(`{"a":1,"b":2}`)

	c1, err := CanonicalizeRaw(spaced)
	if err != nil {
		t.Fatalf("CanonicalizeRaw(spaced): %v", err)
	}
	c2, err := CanonicalizeRaw(tight)
	if err != nil {
		t.Fatalf("CanonicalizeRaw(tight): %v", err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("whitespace changed canonical form: %s vs %s", c1, c2)
	}
}

func TestCanonicalize_NestedKeyOrder(t *testing.T) {
	a := map[string]any{
		"outer": map[string]any{"z": 1, "y": 2},
		"list":  []any{map[string]any{"n": 1, "m": 2}},
	}
	b := map[string]any{
		"list":  []any{map[string]any{"m": 2, "n": 1}},
		"outer": map[string]any{"y": 2, "z": 1},
	}
	ca, err := Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize(a): %v", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		t.Fatalf("Canonicalize(b): %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("nested key order changed canonical form: %s vs %s", ca, cb)
	}
}

func TestCanonicalize_FloatFormatting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"integer-valued float", `{"x":1.0}`, `{"x":1}`},
		{"trailing zeros", `{"x":1.500}`, `{"x":1.5}`},
		{"negative zero", `{"x":-0}`, `{"x":0}`},
		{"large exponent", `{"x":1e21}`, `{"x":1e+21}`},
		{"small exponent", `{"x":1e-7}`, `{"x":1e-7}`},
		{"exact int no exponent", `{"x":100}`, `{"x":100}`},
		{"negative fraction", `{"x":-1.25}`, `{"x":-1.25}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalizeRaw([]byte(tc.in))
			if err != nil {
				t.Fatalf("CanonicalizeRaw(%s): %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("CanonicalizeRaw(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalize_FloatFormattingDeterministicAcrossOrigin(t *testing.T) {
	// Same numeric value, written two different ways in the source text,
	// must canonicalize identically.
	c1, err := CanonicalizeRaw([]byte(`{"x":1.50000}`))
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	c2, err := CanonicalizeRaw([]byte(`{"x":1.5e0}`))
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("differently-written equal floats diverged: %s vs %s", c1, c2)
	}
}

func TestCanonicalize_RawMessageAndBytesPathsAgree(t *testing.T) {
	src := []byte(`{"b":2,"a":1}`)

	viaBytes, err := Canonicalize(src)
	if err != nil {
		t.Fatalf("Canonicalize([]byte): %v", err)
	}
	viaRaw, err := Canonicalize(json.RawMessage(src))
	if err != nil {
		t.Fatalf("Canonicalize(json.RawMessage): %v", err)
	}
	viaStruct, err := Canonicalize(map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("Canonicalize(map): %v", err)
	}
	if string(viaBytes) != string(viaRaw) || string(viaRaw) != string(viaStruct) {
		t.Fatalf("input forms diverged: bytes=%s raw=%s struct=%s", viaBytes, viaRaw, viaStruct)
	}
}

func TestCanonicalize_DuplicateKeyRejected(t *testing.T) {
	_, err := CanonicalizeRaw([]byte(`{"a":1,"a":2}`))
	if err == nil {
		t.Fatal("expected error for duplicate key, got nil")
	}
}

func TestCanonicalize_DuplicateKeyNested(t *testing.T) {
	_, err := CanonicalizeRaw([]byte(`{"outer":{"a":1,"a":2}}`))
	if err == nil {
		t.Fatal("expected error for nested duplicate key, got nil")
	}
}

func TestCanonicalize_InvalidJSON(t *testing.T) {
	_, err := CanonicalizeRaw([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestCanonicalizeRaw_NilAndEmptyRejected(t *testing.T) {
	if _, err := CanonicalizeRaw(nil); err == nil {
		t.Fatal("expected error for nil input, got nil")
	}
	if _, err := CanonicalizeRaw([]byte{}); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestCanonicalize_UnmarshalableValue(t *testing.T) {
	// A channel cannot be marshaled to JSON.
	_, err := Canonicalize(map[string]any{"x": make(chan int)})
	if err == nil {
		t.Fatal("expected error for unmarshalable value, got nil")
	}
}

func TestCanonicalize_EmptyObjectAndArray(t *testing.T) {
	got, err := CanonicalizeRaw([]byte(`{}`))
	if err != nil || string(got) != `{}` {
		t.Fatalf("empty object: got=%s err=%v", got, err)
	}
	got, err = CanonicalizeRaw([]byte(`[]`))
	if err != nil || string(got) != `[]` {
		t.Fatalf("empty array: got=%s err=%v", got, err)
	}
}

func TestCanonicalize_UnicodeEscaping(t *testing.T) {
	// Same string, one written as a literal UTF-8 byte sequence and one as
	// a \u00e9 escape, must canonicalize identically per RFC 8785.
	literal, err := CanonicalizeRaw([]byte("{\"x\":\"café\"}"))
	if err != nil {
		t.Fatalf("literal: %v", err)
	}
	escaped, err := CanonicalizeRaw([]byte(`{"x":"caf\u00e9"}`))
	if err != nil {
		t.Fatalf("escaped: %v", err)
	}
	if string(literal) != string(escaped) {
		t.Fatalf("unicode escape vs literal diverged: %s vs %s", literal, escaped)
	}
}
