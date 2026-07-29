package characterize

import (
	"fmt"
	"regexp"
	"strings"
)

var nonIDChar = regexp.MustCompile(`[^a-z0-9-]+`)

// slugify collapses s into the lowercase, hyphenated token this package's ids are built from.
func slugify(s string) string {
	s = nonIDChar.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "x"
	}
	return s
}

// mintID builds a dotted-lowercase manifest id (schema $defs/identifier): pluginSlug names the
// characterized plugin, kind names the entry's role ("surface", "weak-spot", "gap"), and n is
// its 1-based position among entries of that kind in this manifest. Minting ids in this package
// rather than trusting the characterizing agent to invent schema-compliant ones itself means a
// malformed or colliding id can never reach the manifest.
func mintID(pluginSlug, kind string, n int) string {
	return fmt.Sprintf("%s.%s.%d", slugify(pluginSlug), kind, n)
}
