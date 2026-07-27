package githooks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// secretPattern is one high-confidence plaintext-secret signature.
type secretPattern struct {
	re    *regexp.Regexp
	label string
}

// secretPatterns are the closed, high-confidence signatures ScanSecrets looks
// for: private-key blocks and cloud/VCS/chat-host token shapes distinctive
// enough that a match is never mistaken for ordinary source text.
var secretPatterns = []secretPattern{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "private_key_block"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "aws_access_key_id"},
	{regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}`), "slack_token"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), "github_token"},
}

// ScanSecrets walks root and reports every file whose text matches one of the
// closed set of high-confidence secret signatures - private-key blocks, AWS
// access-key ids, Slack and GitHub tokens. skipRules is the injected
// fsx.ClassifyPath ruleset naming directories/paths never scanned (VCS
// internals, build artifacts, vendored trees); a binary-asset extension is
// always skipped regardless of skipRules. A file that fails to decode as
// UTF-8 is treated as unreadable-as-text, not scanned, and never reported -
// there is nothing to leak in text form.
func ScanSecrets(root string, skipRules []fsx.Rule) ([]Finding, error) {
	var findings []Finding
	err := walkScannable(root, skipRules, func(rel, abs string) error {
		if binarySuffixes[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil // unreadable — nothing to leak in text form
		}
		if !isValidUTF8(data) {
			return nil
		}
		text := string(data)
		for _, p := range secretPatterns {
			if p.re.MatchString(text) {
				findings = append(findings, Finding{Path: rel, Rule: p.label, Detail: "possible " + strings.ReplaceAll(p.label, "_", " ")})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}
