// Package toolchain runs a language's build/test/lint tool and normalizes
// its outcome into one frozen result shape (RunResult) regardless of which
// tool produced it. Run owns everything language-agnostic: dispatching the
// check on the route its Adapter reports, capping diagnostics, classifying
// pass/fail, writing the uncapped detail to a log file, and skipping a
// re-run whose target hasn't changed since the last one (the content-hash
// cache in cache.go). An Adapter owns only what is irreducibly
// tool-specific: how a check runs, the argv for it when that means spawning
// a tool (via sysops), how to parse that tool's output, and the analysis
// itself when the check instead runs in-process.
//
// The default policy treats a warning exactly like an error: Options'
// zero value already enforces this, so a caller must opt in to relax it
// rather than opt in to tighten it.
//
// # The dispatch table
//
// The package also declares the fleet's check-set contract once, as the
// single source every track and the binary's command surface read from. The
// check set is CheckBuild, CheckFormat, CheckLint, CheckVet, CheckSecurity and
// CheckTest, the last carrying a TestKind subcommand layer (TestUnit, TestE2E,
// TestBenchmark); the track set is the four languages Go, Rust, Python and
// shell, plus the workflow track OD71 carves out. Matrix returns the committed
// dispatch table — twenty-eight (language, check) pairs: section 4.7's
// twenty-seven language pairs plus WORKFLOW-PAIR, the workflow track's single
// `workflow lint` check — each pair recording the tools it invokes (more than
// one where a check needs more than one, per OD46), the config file its tools
// read from the language-tools tree (per OD47), and whether an adapter
// implements it yet. ResolveCheck turns a request into either a declared,
// implemented entry or the unsupported diagnostic (DiagUnsupportedCheck, EXIT
// 80), so a pair that would not run fails closed rather than passing silently.
// VerifyMatrixParity asserts the committed table has not drifted from
// MATRIX-plus-WORKFLOW-PAIR in either direction.
//
// The workflow track sits outside the section 4.7 language matrix, on the
// ground OD71 states: a GitHub Actions workflow file is not a language this
// fleet ships, so OD48's standard names no category for it. Its adapter
// (workflow.go) runs actionlint's own default rule set over an explicitly
// enumerated path list and carries exactly two fleet rules — inheritance and
// template-version currency — and no third, per OD71's ceiling.
package toolchain
