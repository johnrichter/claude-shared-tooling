// Package toolchain runs a language's build/test/lint tool and normalizes
// its outcome into one frozen result shape (RunResult) regardless of which
// tool produced it. Run owns everything language-agnostic: spawning the
// tool (via sysops), capping diagnostics, classifying pass/fail, writing
// the uncapped detail to a log file, and skipping a re-run whose target
// hasn't changed since the last one (the content-hash cache in cache.go).
// An Adapter owns only what is irreducibly tool-specific: the argv for a
// given check, and how to parse that tool's own diagnostic output.
//
// The default policy treats a warning exactly like an error: Options'
// zero value already enforces this, so a caller must opt in to relax it
// rather than opt in to tighten it.
package toolchain
