package plugin_conform

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// maxDiagnosticLine is the longest single-line message a clikit diagnostic accepts.
const maxDiagnosticLine = 4096

// oneLine collapses a multi-line message into the single bounded line every clikit diagnostic
// requires, so a wrapped error's or a file path's own formatting never invalidates the
// diagnostic carrying it.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if s == "" {
		return "unspecified"
	}
	if len(s) > maxDiagnosticLine {
		return s[:maxDiagnosticLine-3] + "..."
	}
	return s
}

// expandGlobs walks root and returns every regular file matching any of globs, as sorted
// root-relative slash paths, deduplicated across overlapping patterns. A pattern with no real
// match on disk contributes nothing and is never itself an error -- a caller that requires at
// least one match (CheckRuleGlobs) treats a zero-length result as its own finding; a caller that
// merely enumerates candidates (CheckComponentFrontmatter) has nothing to check either way.
func expandGlobs(root string, globs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		err := doublestar.GlobWalk(os.DirFS(root), g, func(path string, d os.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// expandTilde resolves a leading "~" against the current user's home directory, the same
// shorthand Claude Code's own settings.json additionalDirectories entries use, and cleans the
// result so a trailing slash or a redundant "." never causes a spurious mismatch.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return filepath.Clean(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(home, strings.TrimPrefix(path, "~")))
}
