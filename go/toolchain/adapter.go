package toolchain

import (
	"fmt"
	"sync"
)

// Adapter is a per-language tool integration: it builds the argv for a
// Check and parses that tool's own diagnostic output into the normalized
// Diagnostic shape. Run owns everything language-agnostic — execution,
// capping, caching, status classification; an Adapter owns only what is
// irreducibly tool-specific.
type Adapter interface {
	// Language returns the adapter's identity, e.g. "rust" — the key
	// Target and the registry key on.
	Language() string
	// Tool returns the underlying executable name for check, e.g. "cargo" —
	// carried verbatim onto RunResult.Tool. Most adapters run one executable
	// for every check they support, but the interface takes check so an
	// adapter fronting more than one tool (e.g. one language's formatter vs.
	// its compiler) can answer accurately per check.
	Tool(check Check) string
	// Command returns the argv (excluding the working directory, which Run
	// supplies separately as the process's cwd) for check, or an error if
	// this adapter has no equivalent for it.
	Command(check Check) ([]string, error)
	// Parse turns the tool's raw stdout/stderr from one invocation into
	// normalized diagnostics. exitCode is the tool's own exit status. Parse
	// never runs the tool itself — Run owns the one execution path every
	// adapter shares.
	Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error)
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

// errUnsupportedCheck is the error Command returns for a check an adapter
// has no equivalent for.
func errUnsupportedCheck(tool string, check Check) error {
	return fmt.Errorf("toolchain: %s has no equivalent for check %q", tool, check)
}
