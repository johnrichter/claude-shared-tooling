package toolchain

import (
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// SchemaVersion is the RunResult contract's MAJOR revision, carried on every
// result so a captured record is self-describing without its process
// context. Adding, removing, or renaming a field is a MAJOR change.
const SchemaVersion = 1

// MaxDiagnostics bounds how many diagnostics a RunResult carries verbatim.
// A run that produces more has the excess counted into Overflow and written
// in full to the file LogRef names — never dropped, only deferred out of
// the capped verdict a caller reads by default.
const MaxDiagnostics = 20

// DefaultTimeout bounds a tool invocation when Options.Timeout is left at
// zero. A build/test/lint run that never terminates on its own is not an
// acceptable failure mode for an unattended caller.
const DefaultTimeout = 5 * time.Minute

// Severity is a diagnostic's closed severity class.
type Severity string

// The two severities a normalized Diagnostic carries. A tool's own finer
// grades (e.g. rustc's "note" and "help") are informational children of an
// error or warning, not separately counted diagnostics.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one normalized finding a language tool reported about the
// code it ran against — a compile error, a lint warning, a failed test.
// Every Adapter maps its own tool's native diagnostic shape onto this one
// so a caller never branches on which tool produced it.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code,omitempty"`
	Message  string   `json:"message"`
	File     string   `json:"file,omitempty"`
	Line     int      `json:"line,omitempty"`
}

// Counts is the full tally of diagnostics a run produced, taken before
// capping — so a caller always knows the true volume even when Diagnostics
// holds only the first MaxDiagnostics.
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// Impact reports why a run did, or didn't, actually invoke the underlying
// tool.
type Impact string

const (
	// ImpactExecuted means the tool ran: caching was off, or the target's
	// content hash had no matching cache entry.
	ImpactExecuted Impact = "executed"
	// ImpactSkippedNoChange means the tool did not run: the target's
	// content hash matched the last recorded run's, so that run's verdict
	// was replayed as-is. This is the cache's Level-0 tier — the cheapest
	// possible impact analysis, a whole-target content hash with no
	// dependency-graph reasoning behind it.
	ImpactSkippedNoChange Impact = "skipped_no_change"
)

// RunResult is one language-tool invocation's outcome in its normalized,
// frozen form. Every Adapter (cargo today) produces the same shape, so a
// caller composing runs across languages never branches on which tool ran.
// Diagnostics is capped at MaxDiagnostics; anything past the cap is counted
// in Overflow, and the run's complete diagnostic list and raw tool output
// are written to the file LogRef names — RunResult only ever carries the
// capped verdict, never the unbounded detail behind it.
//
// Status is one of clikit's closed classes rather than a toolchain-specific
// enum: a run that produced no error or must-fail warning is
// clikit.StatusSuccess, everything else clikit.StatusGateNegative. A tool
// that could not even be invoked (missing binary, timeout) is a Go error
// from Run, not a RunResult — diagnostics are the one carrier for a code
// problem the tool actually ran and reported.
type RunResult struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	Tool          string        `json:"tool"`
	Language      string        `json:"language"`
	Command       []string      `json:"command"`
	Status        clikit.Status `json:"status"`
	Counts        Counts        `json:"counts"`
	Diagnostics   []Diagnostic  `json:"diagnostics"`
	Overflow      int           `json:"overflow"`
	LogRef        string        `json:"log_ref"`
	Impact        Impact        `json:"impact"`
	DurationMS    int64         `json:"duration_ms"`
}

// Check is a language-tool run's kind: what the tool was asked to verify.
type Check string

// The three checks every Adapter is expected to support, though Command may
// reject one a particular language tool has no equivalent for.
const (
	CheckBuild Check = "build"
	CheckTest  Check = "test"
	CheckLint  Check = "lint"
)

// Target names one thing Run can check: a language, a check kind, and the
// directory the tool runs against (a crate/module/package root).
type Target struct {
	Language string
	Check    Check
	Dir      string
	// Args carries a build profile and a target triple (e.g. "--release",
	// "x86_64-unknown-linux-gnu") — nothing else. Run appends Args verbatim
	// to the end of the adapter's argv, and runID folds it into the run
	// identity so two targets differing only in Args get distinct cache
	// entries and log files.
	Args []string
}

// Options configures a Run call. The zero value already carries the
// must-fail-on-warning default and disables caching — a caller opts in to
// relax either, never opts in to tighten them.
type Options struct {
	// AllowWarnings, if true, lets a warning-only run classify as success.
	// False (the zero value) makes a warning fail the run exactly like an
	// error — the must-fail-on-warning default.
	AllowWarnings bool
	// LogDir is where each run's uncapped detail is written. Required: Run
	// rejects a call with LogDir empty rather than silently drop the detail
	// a capped RunResult can't carry.
	LogDir string
	// CacheDir, if set, enables the content-hash impact-skip cache: a run
	// whose target content hash matches its last recorded run there is
	// skipped and that run's verdict replayed. Empty (the zero value)
	// disables caching — every call executes the tool.
	CacheDir string
	// Timeout bounds the underlying tool invocation. Zero uses
	// DefaultTimeout.
	Timeout time.Duration
}
