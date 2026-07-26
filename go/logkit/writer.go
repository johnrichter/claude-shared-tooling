package logkit

import (
	"bytes"
	"io"
)

// canonicalWriter sits between zerolog and the real sink. zerolog produces a
// valid JSON object per event but not canonical bytes (its own key order is
// insertion order, not sorted; its number formatting is its own). This writer
// re-canonicalizes each line through RFC 8785 before the real write, so the
// wire bytes are canonical regardless of how zerolog assembled them - zerolog
// stays exactly what the contract says it is: a byte writer.
type canonicalWriter struct {
	real io.Writer
}

func (w *canonicalWriter) Write(p []byte) (int, error) {
	line := bytes.TrimRight(p, "\n")
	canon, err := canonicalizeRaw(line)
	if err != nil {
		return 0, err
	}
	canon = append(canon, '\n')
	if _, err := w.real.Write(canon); err != nil {
		return 0, err
	}
	// Report the input length so the caller (zerolog) sees a full write.
	return len(p), nil
}
