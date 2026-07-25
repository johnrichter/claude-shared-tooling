module github.com/johnrichter/claude-shared-tooling/go/build-helpers

go 1.26

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/johnrichter/claude-shared-tooling/go/roster v0.0.0
)

// roster is a same-repo sibling module (ai-shared-lib/go/roster), not yet independently tagged --
// this placeholder version + local replace is a monorepo-development stand-in the M0 release
// transaction (M0.P7.T3) resolves by cutting a real roster tag and pointing this require at it. A
// `replace` directive is only honored in the MAIN module's own go.mod, so until then, an external
// `go get` of a NEW build-helpers tag containing this dependency cannot resolve roster on its own
// (gomod_resolution_smoke_test.go's TestBuildResolvesFromModulePath documents and tracks this).
replace github.com/johnrichter/claude-shared-tooling/go/roster => ../roster
