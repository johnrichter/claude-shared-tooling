module github.com/johnrichter/claude-shared-tooling/plugin-foundation

go 1.26

require (
	github.com/johnrichter/claude-shared-tooling/go/adoption v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/transcript v0.0.0
)

require (
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/johnrichter/claude-shared-tooling/go/clikit v0.0.0 // indirect
	github.com/johnrichter/claude-shared-tooling/go/gate v0.0.0 // indirect
	github.com/johnrichter/claude-shared-tooling/go/logkit v0.0.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.29.0 // indirect
)

// adoption, clikit, gate, transcript and logkit are same-repo sibling modules
// (ai-shared-lib/go/adoption, ai-shared-lib/go/clikit, ai-shared-lib/go/gate,
// ai-shared-lib/go/transcript, ai-shared-lib/go/logkit), not yet independently tagged -- this
// placeholder version + local replace is a monorepo-development stand-in a future release
// transaction resolves by cutting real tags and pointing these requires at them. A `replace`
// directive is only honored in the MAIN module's own go.mod, so this module restates adoption's
// own sibling replaces rather than inheriting them.
replace github.com/johnrichter/claude-shared-tooling/go/adoption => ../go/adoption

replace github.com/johnrichter/claude-shared-tooling/go/clikit => ../go/clikit

replace github.com/johnrichter/claude-shared-tooling/go/gate => ../go/gate

replace github.com/johnrichter/claude-shared-tooling/go/transcript => ../go/transcript

replace github.com/johnrichter/claude-shared-tooling/go/logkit => ../go/logkit
