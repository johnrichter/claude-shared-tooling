package logkit

import (
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

// canonicalize renders v (a Record, built with encoding/json's ordinary
// rules) as RFC 8785 JSON Canonicalization Scheme bytes: keys sorted at every
// depth, ECMA-262 shortest-round-trip numbers, minimal escaping, no
// whitespace. This is what makes Go, Rust and Python emit identical bytes for
// the same record; an implementation reuses one canonicalizer rather than
// maintaining a logging-specific serializer.
func canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("logkit: encode record: %w", err)
	}
	return canonicalizeRaw(raw)
}

// canonicalizeRaw runs already-valid JSON bytes through the JCS
// canonicalizer, independent of how they were produced.
func canonicalizeRaw(raw []byte) ([]byte, error) {
	canon, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("logkit: canonicalize: %w", err)
	}
	return canon, nil
}
