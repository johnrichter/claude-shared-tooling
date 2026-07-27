module github.com/johnrichter/claude-shared-tooling/go/ledger

go 1.26

require github.com/johnrichter/claude-shared-tooling/go/docmirror v0.0.0

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/johnrichter/claude-shared-tooling/go/fsx v0.0.0 // indirect
	github.com/johnrichter/claude-shared-tooling/go/jsondoc v0.0.0 // indirect
)

// docmirror, fsx, and jsondoc are same-repo sibling modules (ai-shared-lib/go/docmirror,
// ai-shared-lib/go/fsx, ai-shared-lib/go/jsondoc), not yet independently tagged -- this
// placeholder version + local replace is a monorepo-development stand-in a future release
// transaction resolves by cutting real tags and pointing these requires at them. A `replace`
// directive is only honored in the MAIN module's own go.mod, so until then an external `go
// get` of a new ledger tag cannot resolve these on its own.
replace github.com/johnrichter/claude-shared-tooling/go/docmirror => ../docmirror

replace github.com/johnrichter/claude-shared-tooling/go/fsx => ../fsx

replace github.com/johnrichter/claude-shared-tooling/go/jsondoc => ../jsondoc
