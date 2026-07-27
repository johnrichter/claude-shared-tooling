package githooks

import "github.com/johnrichter/claude-shared-tooling/go/fsx"

// Finding is one scanner hit: a path that violates a guardrail, plus a stable
// rule id and a human-readable detail.
type Finding struct {
	Path   string // slash-separated, relative to the scanned root
	Rule   string // stable rule id, e.g. "aws_access_key_id", "raw_binary"
	Detail string // one-line, human-readable description of the hit
}

// DefaultSkipRules is a reasonable starting ruleset for the skipRules
// parameter every scan in this package takes: VCS internals, build/venv/
// cache artifacts, and vendored dependency trees. It is a default, not a
// built-in - a caller passes its own ruleset (or this one, or this one
// extended) to fsx.ClassifyPath's injected-ruleset model.
var DefaultSkipRules = []fsx.Rule{
	{Pattern: "**/.git/**", Class: SkipClass},
	{Pattern: "**/.git-worktrees/**", Class: SkipClass},
	{Pattern: "**/.venv/**", Class: SkipClass},
	{Pattern: "**/venv/**", Class: SkipClass},
	{Pattern: "**/__pycache__/**", Class: SkipClass},
	{Pattern: "**/.pytest_cache/**", Class: SkipClass},
	{Pattern: "**/.mypy_cache/**", Class: SkipClass},
	{Pattern: "**/.ruff_cache/**", Class: SkipClass},
	{Pattern: "**/.cache/**", Class: SkipClass},
	{Pattern: "**/node_modules/**", Class: SkipClass},
	{Pattern: "**/dist/**", Class: SkipClass},
	{Pattern: "**/build/**", Class: SkipClass},
	{Pattern: "**/target/**", Class: SkipClass},
	{Pattern: "**/*.egg-info/**", Class: SkipClass},
}

// binarySuffixes are asset extensions never text-scanned by ScanSecrets or
// ScanPrivacy - content that cannot carry a plaintext secret or a frontmatter
// marker as readable text.
var binarySuffixes = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".pdf": true,
	".zip": true, ".gz": true, ".whl": true, ".pyc": true, ".ico": true,
	".woff": true, ".woff2": true,
}
