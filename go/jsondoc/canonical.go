package jsondoc

import (
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

// Canonicalize renders v as RFC 8785 JSON Canonicalization Scheme (JCS)
// bytes: object keys sorted at every depth, numbers in ECMA-262
// shortest-round-trip form, minimal string escaping, and no incidental
// whitespace. Two documents that are the same JSON value — regardless of key
// order, spacing, or how a number was originally written — canonicalize to
// byte-identical output, which is what makes them safe to hash or compare
// with Canonicalize's output as the input.
//
// []byte and json.RawMessage are treated as already-encoded JSON text and
// passed straight to CanonicalizeRaw; every other value is first encoded
// with encoding/json. This means a []byte is canonicalized as the JSON it
// contains, not base64-encoded the way a plain json.Marshal(v) would.
//
// An error is returned if v cannot be marshaled to JSON, if it (or its raw
// bytes) is not valid JSON, or if a JSON object in it repeats a key at the
// same level — duplicate keys are rejected rather than silently resolved to
// one of the values.
func Canonicalize(v any) ([]byte, error) {
	switch raw := v.(type) {
	case json.RawMessage:
		return CanonicalizeRaw(raw)
	case []byte:
		return CanonicalizeRaw(raw)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jsondoc: marshal: %w", err)
	}
	return CanonicalizeRaw(encoded)
}

// CanonicalizeRaw runs already-encoded JSON bytes through the RFC 8785
// canonicalizer, independent of how they were produced. Nil or empty input
// is invalid JSON and returns an error — callers representing "no document"
// should use an explicit empty value (e.g. []byte("{}") or []byte("null"))
// rather than nil.
func CanonicalizeRaw(raw []byte) ([]byte, error) {
	canon, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("jsondoc: canonicalize: %w", err)
	}
	return canon, nil
}
