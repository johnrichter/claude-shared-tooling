module github.com/johnrichter/claude-shared-tooling/go/git

go 1.26

require github.com/johnrichter/claude-shared-tooling/go/sysops v0.0.0

// sysops is a same-repo sibling module (ai-shared-lib/go/sysops), not yet independently
// tagged -- this placeholder version + local replace is a monorepo-development stand-in the
// release transaction resolves by cutting a real sysops tag and pointing this require at it. A
// `replace` directive is only honored in the MAIN module's own go.mod, so until then, an external
// `go get` of a NEW git tag containing this dependency cannot resolve sysops on its own.
replace github.com/johnrichter/claude-shared-tooling/go/sysops => ../sysops
