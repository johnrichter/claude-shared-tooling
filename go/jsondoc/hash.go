package jsondoc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ContentHash returns the lowercase hex sha256 digest of fields' canonical
// JSON form. fields is the caller's own choice of which parts of a larger
// document are spec-bearing — e.g. a struct or map holding only the fields
// that should force a rebuild when they change, with anything else (a
// priority tier, a display label, provenance metadata) left out. Because
// only what's included in fields is ever encoded, a change to an excluded
// field cannot alter the hash: retuning a tier without touching the spec
// fields yields the identical hash, so callers can tell "the spec changed"
// apart from "only a scheduling knob changed".
//
// fields is canonicalized (see Canonicalize) before hashing, so key order,
// whitespace, and float formatting differences never produce a different
// hash for an otherwise-identical value.
func ContentHash(fields any) (string, error) {
	canon, err := Canonicalize(fields)
	if err != nil {
		return "", fmt.Errorf("jsondoc: content hash: %w", err)
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}
