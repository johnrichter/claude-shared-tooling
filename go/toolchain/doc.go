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
package toolchain
