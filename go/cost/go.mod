module github.com/johnrichter/claude-shared-tooling/go/cost

go 1.26

require (
	github.com/johnrichter/claude-shared-tooling/go/roster v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/transcript v0.0.0
	modernc.org/sqlite v1.54.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.46.0 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// roster and transcript are same-repo sibling modules (ai-shared-lib/go/roster,
// ai-shared-lib/go/transcript), not yet independently tagged -- this placeholder version +
// local replace is a monorepo-development stand-in a future release transaction resolves by
// cutting real tags and pointing these requires at them. A `replace` directive is only
// honored in the MAIN module's own go.mod, so until then an external `go get` of a new cost
// tag containing these dependencies cannot resolve them on its own.
replace github.com/johnrichter/claude-shared-tooling/go/roster => ../roster

replace github.com/johnrichter/claude-shared-tooling/go/transcript => ../transcript
