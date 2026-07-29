module github.com/johnrichter/claude-shared-tooling/go/plugin-behavioral

go 1.26

require (
	github.com/johnrichter/claude-shared-tooling/go/adoption v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/sysops v0.0.0
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

// adoption, sysops and transcript are same-repo sibling modules (ai-shared-lib/go/adoption,
// .../go/sysops, .../go/transcript), not yet independently tagged -- these placeholder versions +
// local replaces are a monorepo-development stand-in a future release transaction resolves by
// cutting real tags and pointing these requires at them. A `replace` directive is only honored in
// the MAIN module's own go.mod, so the full transitive closure is replaced here, including
// modules this package never imports directly (clikit, gate, logkit -- adoption's own deps).
replace (
	github.com/johnrichter/claude-shared-tooling/go/adoption => ../adoption
	github.com/johnrichter/claude-shared-tooling/go/clikit => ../clikit
	github.com/johnrichter/claude-shared-tooling/go/gate => ../gate
	github.com/johnrichter/claude-shared-tooling/go/logkit => ../logkit
	github.com/johnrichter/claude-shared-tooling/go/sysops => ../sysops
	github.com/johnrichter/claude-shared-tooling/go/transcript => ../transcript
)
