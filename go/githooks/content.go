package githooks

import "unicode/utf8"

// isValidUTF8 reports whether b decodes cleanly as UTF-8 end to end.
func isValidUTF8(b []byte) bool {
	return utf8.Valid(b)
}

// sniffBytes is how many leading bytes isBinaryPrefix inspects - matching
// git's own is-binary heuristic (core.bigFileThreshold aside), which looks at
// a fixed-size head rather than the whole file.
const sniffBytes = 8000

// isBinaryPrefix reports whether prefix (the file's leading sniffBytes) looks
// binary: a NUL byte, or content that fails UTF-8 decoding. A text file that
// happens to contain non-ASCII bytes still decodes as valid UTF-8, so this
// stays low on false positives for real prose/code.
func isBinaryPrefix(prefix []byte) bool {
	for _, b := range prefix {
		if b == 0 {
			return true
		}
	}
	return !utf8.Valid(prefix)
}
