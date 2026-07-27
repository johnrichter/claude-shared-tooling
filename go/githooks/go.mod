module github.com/johnrichter/claude-shared-tooling/go/githooks

go 1.26

require (
	github.com/johnrichter/claude-shared-tooling/go/clikit v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/fsx v0.0.0
)

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/johnrichter/claude-shared-tooling/go/logkit v0.0.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.29.0 // indirect
)

// fsx, clikit and logkit are same-repo sibling modules (ai-shared-lib/go/fsx,
// ai-shared-lib/go/clikit, ai-shared-lib/go/logkit), not yet independently
// tagged -- this placeholder version + local replace is a monorepo-development
// stand-in a future release transaction resolves by cutting real tags and
// pointing these requires at them. A `replace` directive is only honored in
// the MAIN module's own go.mod, so until then an external `go get` of a new
// githooks tag containing these dependencies cannot resolve them on its own.
replace github.com/johnrichter/claude-shared-tooling/go/fsx => ../fsx

replace github.com/johnrichter/claude-shared-tooling/go/clikit => ../clikit

replace github.com/johnrichter/claude-shared-tooling/go/logkit => ../logkit
