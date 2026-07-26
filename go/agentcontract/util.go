package agentcontract

import (
	"strings"
	"unicode"
)

// trimmedEmpty reports whether s is empty once leading/trailing whitespace is removed.
func trimmedEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// normalize lowercases s and collapses all whitespace runs to a single space, so two
// statements differing only in casing or incidental whitespace compare equal.
func normalize(s string) string {
	fields := strings.FieldsFunc(strings.ToLower(s), unicode.IsSpace)
	return strings.Join(fields, " ")
}

// containsAll reports whether every needle in needles occurs in haystack (already normalized).
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}

// containsAny reports whether at least one needle occurs in haystack (already normalized).
func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
