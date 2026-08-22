package toolchain

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Route is how an Adapter performs one check: by spawning an external tool,
// or by analyzing inside the calling process. An adapter may answer
// differently per check — routing, like Tool, is a per-check property.
type Route string

const (
	// RouteSubprocess spawns the adapter's Command argv and hands what it
	// wrote to Parse. This is the ordinary route: every check whose tool
	// ships as a binary takes it.
	RouteSubprocess Route = "subprocess"
	// RouteInProcess calls RunInProcess and takes its diagnostics directly.
	// It exists for a check whose analysis is a library rather than a
	// binary, so there is nothing to spawn and nothing to parse.
	RouteInProcess Route = "in_process"
)

// valid reports whether r is one of the declared routes. Run rejects
// anything else rather than guess a route on a caller's behalf.
func (r Route) valid() bool {
	return r == RouteSubprocess || r == RouteInProcess
}

// Adapter is a per-language tool integration: it routes a Check, names what
// runs it, and turns that tool's own findings into the normalized Diagnostic
// shape. Run owns everything language-agnostic — execution, capping,
// caching, status classification, logging; an Adapter owns only what is
// irreducibly tool-specific.
//
// Language, Route and Tool are answered on every route. Command and Parse
// serve RouteSubprocess only, and RunInProcess serves RouteInProcess only —
// an adapter that never uses one of those three still implements it, and
// should report ErrUnsupportedCheck from it.
type Adapter interface {
	// Language returns the adapter's identity, e.g. "rust" — the key
	// Target and the registry key on.
	Language() string
	// Route reports how check runs. Run reads this before it runs anything,
	// and dispatches on the answer.
	Route(check Check) Route
	// Tool returns the name of what performs check, carried verbatim onto
	// RunResult.Tool. On RouteSubprocess that is the executable, e.g.
	// "cargo". On RouteInProcess nothing is spawned, so it is instead the
	// analysis's own fixed name — the label a reader of the result sees in
	// place of a binary. Most adapters answer one name for every check they
	// support, but the interface takes check so an adapter fronting more
	// than one tool (e.g. one language's formatter vs. its compiler), or
	// mixing routes across checks, can answer accurately per check.
	Tool(check Check) string
	// Command returns the argv (excluding the working directory, which Run
	// supplies separately as the process's cwd) for check, or an error if
	// this adapter has no equivalent for it. Only RouteSubprocess reaches
	// it.
	Command(check Check) ([]string, error)
	// Parse turns the tool's raw stdout/stderr from one invocation into
	// normalized diagnostics. exitCode is the tool's own exit status. Parse
	// never runs the tool itself — Run owns the one execution path every
	// subprocess-routed adapter shares. Only RouteSubprocess reaches it.
	Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error)
	// RunInProcess performs target's check inside this process and returns
	// its normalized diagnostics. Only RouteInProcess reaches it; Run
	// bounds ctx by Options.Timeout exactly as it bounds a spawned tool, and
	// an implementation is expected to honor cancellation. A returned error
	// means the analysis could not be performed at all — a problem it found
	// in the code is a Diagnostic, never an error.
	RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Adapter{}
)

// Register makes adapter available to Run under its own Language() key,
// replacing whatever was previously registered for that language. Built-in
// adapters (cargo.go) call this from an init function, so importing package
// toolchain alone is enough to make them available.
func Register(adapter Adapter) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[adapter.Language()] = adapter
}

// lookup returns the adapter registered for language, and whether one was
// found.
func lookup(language string) (Adapter, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a, ok := registry[language]
	return a, ok
}

// ErrUnsupportedCheck is the sentinel wrapped into the error an adapter
// returns for a check it has no equivalent for, and for a member the check's
// route never reaches. Callers match it with errors.Is rather than parsing
// the error's text.
var ErrUnsupportedCheck = errors.New("toolchain: check not supported by adapter")

// errUnsupportedCheck builds that error for a check tool has no equivalent
// for, wrapping ErrUnsupportedCheck so callers can match it with errors.Is.
func errUnsupportedCheck(tool string, check Check) error {
	return fmt.Errorf("toolchain: %s has no equivalent for check %q: %w", tool, check, ErrUnsupportedCheck)
}
