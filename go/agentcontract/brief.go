package agentcontract

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontmatterFence = "---"

// ParseError reports that a candidate brief file could not be read as a brief at all — missing
// or malformed frontmatter, or no declared name. It is distinct from a lint Finding: a Finding
// is a brief that parsed fine but violates a contract property, while ParseError means there
// was nothing to check.
type ParseError struct {
	Path   string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("agentcontract: %s: %s", e.Path, e.Reason)
}

// ParseBrief parses one agent brief file's bytes: a YAML frontmatter block fenced by "---"
// lines, followed by a Markdown body.
func ParseBrief(path string, data []byte) (*Brief, error) {
	fm, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, &ParseError{Path: path, Reason: err.Error()}
	}

	var parsed Frontmatter
	if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
		return nil, &ParseError{Path: path, Reason: fmt.Sprintf("frontmatter is not valid YAML: %v", err)}
	}
	if strings.TrimSpace(parsed.Name) == "" {
		return nil, &ParseError{Path: path, Reason: "frontmatter has no name"}
	}

	return &Brief{
		Path:        path,
		Dir:         filepath.Dir(path),
		Frontmatter: parsed,
		Body:        body,
	}, nil
}

// splitFrontmatter splits a brief file's raw content into its frontmatter YAML and its body.
// The file must open with a "---" fence line, and the frontmatter block must be closed by a
// second "---" fence line.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return "", "", fmt.Errorf("does not open with a %q frontmatter fence", frontmatterFence)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterFence {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", fmt.Errorf("frontmatter opened but never closed with a second %q fence", frontmatterFence)
}
