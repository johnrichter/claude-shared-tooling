module github.com/johnrichter/claude-shared-tooling/go/bandcheck

go 1.26

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/johnrichter/claude-shared-tooling/go/gate v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/roster v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/sysops v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/transcript v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

// gate, roster, sysops and transcript are same-repo sibling modules
// (ai-shared-lib/go/gate, ai-shared-lib/go/roster, ai-shared-lib/go/sysops,
// ai-shared-lib/go/transcript), not yet independently tagged -- this placeholder version + local
// replace is a monorepo-development stand-in a future release transaction resolves by cutting
// real tags and pointing these requires at them.
replace github.com/johnrichter/claude-shared-tooling/go/gate => ../gate

replace github.com/johnrichter/claude-shared-tooling/go/roster => ../roster

replace github.com/johnrichter/claude-shared-tooling/go/sysops => ../sysops

replace github.com/johnrichter/claude-shared-tooling/go/transcript => ../transcript
