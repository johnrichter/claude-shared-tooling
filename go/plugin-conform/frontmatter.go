package plugin_conform

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontmatterFence = "---"

// FrontmatterParseError names a file whose leading YAML frontmatter block opened but failed to
// parse -- distinct from "no frontmatter block at all", which parseFrontmatter reports via its
// own ok return rather than as an error, since a file with nothing to parse is not a parse
// failure.
type FrontmatterParseError struct {
	Path   string
	Reason string
}

func (e *FrontmatterParseError) Error() string {
	return fmt.Sprintf("plugin-conform: %s: %s", e.Path, e.Reason)
}

// splitFrontmatter splits content into its leading "---"-fenced block and the text that follows.
// A file that does not open with the fence has no frontmatter block at all, reported via
// ok == false: that is a legitimate, uncommon shape (an unconditional rule, or a component this
// check does not require frontmatter from), never a parse failure by itself.
func splitFrontmatter(content string) (frontmatter string, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterFence {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
}

// parseFrontmatter splits path's content and decodes its frontmatter block into out (a pointer
// to a caller-declared struct). found is false, with a nil error, when the file carries no
// frontmatter block at all. A block that opens but fails to decode as YAML is a
// FrontmatterParseError, never silently skipped.
func parseFrontmatter(path string, content []byte, out any) (found bool, err error) {
	fm, ok := splitFrontmatter(string(content))
	if !ok {
		return false, nil
	}
	if err := yaml.Unmarshal([]byte(fm), out); err != nil {
		return true, &FrontmatterParseError{Path: path, Reason: fmt.Sprintf("frontmatter is not valid YAML: %v", err)}
	}
	return true, nil
}
