package clikit

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/gowebpki/jcs"
)

// MarshalCanonical renders r as the canonical bytes the contract pins for
// stdout: RFC 8785 (JCS) key ordering, numbers, strings and encoding -
// adopted from logkit's own canonical_json rule rather than a second
// canonicalizer, since a divergence between the two would be a defect in
// whichever one restates it. r is validated first, so an invalid record is
// never serialized.
func (r *Result) MarshalCanonical() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("clikit: encode result: %w", err)
	}
	canon, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("clikit: canonicalize result: %w", err)
	}
	return canon, nil
}

// Emit writes r to w as the CLI's one result record: canonical JSON,
// LF-terminated, in a single write. It is the last thing a clikit CLI does
// before exiting with r.ExitCode.
func Emit(w io.Writer, r *Result) error {
	canon, err := r.MarshalCanonical()
	if err != nil {
		return err
	}
	canon = append(canon, '\n')
	_, err = w.Write(canon)
	return err
}
