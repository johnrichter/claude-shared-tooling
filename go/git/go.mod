module github.com/johnrichter/claude-shared-tooling/go/git

go 1.26

require github.com/johnrichter/claude-shared-tooling/go/sysops v0.1.0

// sysops is a same-repo sibling module (ai-shared-lib/go/sysops), independently tagged at
// go/sysops/v0.1.0. The require above resolves that tag for external consumers on its own. The
// replace below is kept only as a monorepo-development convenience so local sysops edits are
// picked up without re-tagging; a `replace` directive is only honored in the MAIN module's own
// go.mod, so it has no effect on external consumers of this module.
replace github.com/johnrichter/claude-shared-tooling/go/sysops => ../sysops
