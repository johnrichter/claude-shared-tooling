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

// MatrixEntry is one pair in the dispatch table: a language and a check (a
// test pair also carries its TestKind), plus what a caller needs to run it —
// the tools the check invokes, the config those tools read, and whether an
// adapter implements the pair yet. It carries no execution behavior; Run and
// each Adapter own that. An entry is data the binary's command surface, the
// language tracks and the routing table all read from this one source.
type MatrixEntry struct {
	// Language is the pair's language, one of the LanguageGo/Rust/Python/Shell
	// set.
	Language string
	// Check is the pair's check kind.
	Check Check
	// Test is the CheckTest subcommand; empty for every non-test check.
	Test TestKind
	// Tools lists every tool the check invokes, in run order. It holds more
	// than one where a check needs more than one tool, each doing a task the
	// others do not (OD46) — Go lint names golangci-lint and goimports, Go
	// security names gosec and govulncheck. It never holds a library a
	// language resolves through its own dependency file.
	Tools []string
	// Config is the base name of the config file the pair's tools read,
	// resolved from the language-tools tree rather than a caller's (OD47).
	// Empty when no tool in the pair reads a config.
	Config string
	// Implemented reports whether an adapter performs this pair today. A
	// declared-but-unimplemented pair resolves (ResolveCheck) to the
	// unsupported diagnostic (EXIT 80), never a silent pass; a later task
	// flips it true as it lands the adapter.
	Implemented bool
}

// PairID is the pair's canonical identifier: the language, the check, and the
// test subcommand when the check is CheckTest — "go build", "rust test
// benchmark", "shell lint". It is the key the count, resolution and parity
// checks compare on, and the operation name the routing table uses.
func (e MatrixEntry) PairID() string {
	if e.Check == CheckTest {
		return e.Language + " " + string(e.Check) + " " + string(e.Test)
	}
	return e.Language + " " + string(e.Check)
}

// matrixSpec is MATRIX: section 4.7's language-by-check grid, the single
// source the committed dispatch table (committedMatrix) is regenerated from
// and checked against (RegenerateMatrix, VerifyMatrixParity). Each row lists
// the checks the standard marks a language "yes" for; CheckTest expands into
// the subcommands testKindsByLanguage names. Shell takes no build (a script
// neither compiles nor packages) and no vet (the standard records type
// checking as not applicable for it).
var matrixSpec = map[string][]Check{
	LanguageGo:     {CheckBuild, CheckFormat, CheckLint, CheckVet, CheckSecurity, CheckTest},
	LanguageRust:   {CheckBuild, CheckFormat, CheckLint, CheckVet, CheckSecurity, CheckTest},
	LanguagePython: {CheckBuild, CheckFormat, CheckLint, CheckVet, CheckSecurity, CheckTest},
	LanguageShell:  {CheckFormat, CheckLint, CheckSecurity, CheckTest},
}

// testKindsByLanguage expands CheckTest into its pairs per section 4.7's test
// rows: every language takes unit and e2e, and Rust alone adds benchmark
// (the standard names a benchmark tool for no other in-scope language).
var testKindsByLanguage = map[string][]TestKind{
	LanguageGo:     {TestUnit, TestE2E},
	LanguageRust:   {TestUnit, TestE2E, TestBenchmark},
	LanguagePython: {TestUnit, TestE2E},
	LanguageShell:  {TestUnit, TestE2E},
}

// committedMatrix is the dispatch table: the frozen enumeration of all
// twenty-seven pairs section 4.7 names, each with its tool list, its config
// file and its implementation status. It is asserted equal to matrixSpec
// (MATRIX) by VerifyMatrixParity, so a pair dropped here or invented here
// fails the parity test. Config base names resolve from the language-tools
// tree (OD47): .golangci.yml, clippy.toml, ruff.toml, .shellcheckrc.
// Implemented marks the thirteen pairs the adapters perform today (F15, Go
// five, Rust four, Python four); the other fourteen resolve to EXIT 80 until
// their adapter lands.
var committedMatrix = []MatrixEntry{
	// Go — seven pairs.
	{Language: LanguageGo, Check: CheckBuild, Tools: []string{"go build"}, Implemented: true},
	{Language: LanguageGo, Check: CheckFormat, Tools: []string{"gofmt", "goimports"}, Implemented: true},
	{Language: LanguageGo, Check: CheckLint, Tools: []string{"golangci-lint", "goimports"}, Config: ".golangci.yml", Implemented: true},
	{Language: LanguageGo, Check: CheckVet, Tools: []string{"go vet", "staticcheck"}, Implemented: true},
	{Language: LanguageGo, Check: CheckSecurity, Tools: []string{"gosec", "govulncheck"}},
	{Language: LanguageGo, Check: CheckTest, Test: TestUnit, Tools: []string{"go test", "gotestsum"}, Implemented: true},
	{Language: LanguageGo, Check: CheckTest, Test: TestE2E, Tools: []string{"go test"}},

	// Rust — eight pairs.
	{Language: LanguageRust, Check: CheckBuild, Tools: []string{"cargo build"}, Implemented: true},
	{Language: LanguageRust, Check: CheckFormat, Tools: []string{"cargo fmt"}, Implemented: true},
	{Language: LanguageRust, Check: CheckLint, Tools: []string{"cargo clippy"}, Config: "clippy.toml", Implemented: true},
	{Language: LanguageRust, Check: CheckVet, Tools: []string{"cargo check"}},
	{Language: LanguageRust, Check: CheckSecurity, Tools: []string{"cargo audit", "cargo deny"}},
	{Language: LanguageRust, Check: CheckTest, Test: TestUnit, Tools: []string{"cargo nextest", "cargo llvm-cov"}, Implemented: true},
	{Language: LanguageRust, Check: CheckTest, Test: TestE2E, Tools: []string{"cargo nextest"}},
	{Language: LanguageRust, Check: CheckTest, Test: TestBenchmark, Tools: []string{"cargo bench"}},

	// Python — seven pairs.
	{Language: LanguagePython, Check: CheckBuild, Tools: []string{"uv build"}, Implemented: true},
	{Language: LanguagePython, Check: CheckFormat, Tools: []string{"ruff format"}, Config: "ruff.toml", Implemented: true},
	{Language: LanguagePython, Check: CheckLint, Tools: []string{"ruff check"}, Config: "ruff.toml", Implemented: true},
	{Language: LanguagePython, Check: CheckVet, Tools: []string{"mypy"}},
	{Language: LanguagePython, Check: CheckSecurity, Tools: []string{"bandit"}},
	{Language: LanguagePython, Check: CheckTest, Test: TestUnit, Tools: []string{"pytest"}, Implemented: true},
	{Language: LanguagePython, Check: CheckTest, Test: TestE2E, Tools: []string{"pytest", "playwright"}},

	// Shell — five pairs. No build, no vet.
	{Language: LanguageShell, Check: CheckFormat, Tools: []string{"shfmt"}},
	{Language: LanguageShell, Check: CheckLint, Tools: []string{"shellcheck", "checkbashisms"}, Config: ".shellcheckrc"},
	{Language: LanguageShell, Check: CheckSecurity, Tools: []string{"semgrep"}},
	{Language: LanguageShell, Check: CheckTest, Test: TestUnit, Tools: []string{"bats", "kcov"}},
	{Language: LanguageShell, Check: CheckTest, Test: TestE2E, Tools: []string{"bats"}},
}

// Matrix returns a copy of the committed dispatch table — all twenty-seven
// pairs section 4.7 names, in a fixed order. It is a copy so a caller can
// never mutate the one source every language track and the command surface
// read from.
func Matrix() []MatrixEntry {
	out := make([]MatrixEntry, len(committedMatrix))
	for i, e := range committedMatrix {
		out[i] = e
		out[i].Tools = append([]string(nil), e.Tools...)
	}
	return out
}

// LookupPair returns the dispatch-table entry for (language, check, test),
// and whether the Matrix declares that pair. test must be empty for a
// non-test check and one of the TestKind values for CheckTest; any other
// combination is not a declared pair and returns ok=false. Membership here
// says only that the pair is in the table — ResolveCheck decides whether an
// adapter implements it.
func LookupPair(language string, check Check, test TestKind) (MatrixEntry, bool) {
	for _, e := range committedMatrix {
		if e.Language == language && e.Check == check && e.Test == test {
			e.Tools = append([]string(nil), e.Tools...)
			return e, true
		}
	}
	return MatrixEntry{}, false
}
