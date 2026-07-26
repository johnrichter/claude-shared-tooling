module github.com/johnrichter/claude-shared-tooling/go/clikit

go 1.26

require (
	github.com/gowebpki/jcs v1.0.1
	github.com/johnrichter/claude-shared-tooling/go/logkit v0.0.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.29.0 // indirect
)

// logkit is a same-repo sibling module (ai-shared-lib/go/logkit), not yet independently tagged --
// this placeholder version + local replace is a monorepo-development stand-in a future release
// transaction resolves by cutting a real logkit tag and pointing this require at it. A `replace`
// directive is only honored in the MAIN module's own go.mod, so until then an external `go get` of
// a new clikit tag containing this dependency cannot resolve logkit on its own.
replace github.com/johnrichter/claude-shared-tooling/go/logkit => ../logkit
