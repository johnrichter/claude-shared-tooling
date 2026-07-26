module github.com/johnrichter/claude-shared-tooling/go/state

go 1.26

require github.com/johnrichter/claude-shared-tooling/go/fsx v0.0.0

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
)

// fsx is a same-repo sibling module (ai-shared-lib/go/fsx), not yet independently tagged --
// this placeholder version + local replace is a monorepo-development stand-in the M0 release
// transaction resolves by cutting a real fsx tag and pointing this require at it.
replace github.com/johnrichter/claude-shared-tooling/go/fsx => ../fsx
