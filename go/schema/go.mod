module github.com/johnrichter/claude-shared-tooling/go/schema

go 1.26

require (
	github.com/johnrichter/claude-shared-tooling/go/clikit v0.0.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
)

require (
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/johnrichter/claude-shared-tooling/go/logkit v0.0.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)

// clikit and logkit are same-repo sibling modules (ai-shared-lib/go/clikit, .../go/logkit), not
// yet independently tagged -- these placeholder versions + local replaces are a monorepo-
// development stand-in a future release transaction resolves by cutting real tags and pointing
// these requires at them.
replace (
	github.com/johnrichter/claude-shared-tooling/go/clikit => ../clikit
	github.com/johnrichter/claude-shared-tooling/go/logkit => ../logkit
)
