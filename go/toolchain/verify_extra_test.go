package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// TestVerifyRunResultJSONFieldsFrozen checks RunResult's on-wire JSON carries
// exactly the documented field set (plus schema_version, the versioning
// escape hatch every frozen contract needs) — nothing renamed, nothing
// dropped, nothing extra sneaked in.
func TestVerifyRunResultJSONFieldsFrozen(t *testing.T) {
	rr := RunResult{
		SchemaVersion: SchemaVersion,
		ID:            "abc",
		Tool:          "cargo",
		Language:      "rust",
		Command:       []string{"build"},
		Counts:        Counts{Errors: 1},
		Diagnostics:   []Diagnostic{{Severity: SeverityError, Message: "m"}},
		Overflow:      2,
		LogRef:        "/tmp/x.json",
		Impact:        ImpactExecuted,
		DurationMS:    5,
	}
	raw, err := json.Marshal(rr)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := map[string]bool{
		"schema_version": true, "id": true, "tool": true, "language": true,
		"command": true, "status": true, "counts": true, "diagnostics": true,
		"overflow": true, "log_ref": true, "impact": true, "duration_ms": true,
	}
	if len(m) != len(want) {
		t.Fatalf("RunResult JSON has %d top-level fields, want %d: got keys %v", len(m), len(want), keysOf(m))
	}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("RunResult JSON missing frozen field %q; got keys %v", k, keysOf(m))
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestVerifyCheckDeclaresFormatAndVetBesideBuildTestLint checks types.go's
// Check constants include format and vet alongside build/test/lint, each
// with the exact lowercase string value the wire contract and adapters key
// on — not merely that the identifiers compile, but that their values are
// the documented ones.
func TestVerifyCheckDeclaresFormatAndVetBesideBuildTestLint(t *testing.T) {
	want := map[Check]string{
		CheckBuild:  "build",
		CheckTest:   "test",
		CheckLint:   "lint",
		CheckFormat: "format",
		CheckVet:    "vet",
	}
	for check, wantVal := range want {
		if string(check) != wantVal {
			t.Fatalf("Check constant = %q, want %q", string(check), wantVal)
		}
	}
}

// TestVerifyDiagnosticsCapIsExactlyTwenty pins MaxDiagnostics itself, since
// the cap value is part of the frozen contract (acceptance criterion #1
// names 20 explicitly, not "MaxDiagnostics whatever it happens to be").
func TestVerifyDiagnosticsCapIsExactlyTwenty(t *testing.T) {
	if MaxDiagnostics != 20 {
		t.Fatalf("MaxDiagnostics = %d, want 20 per the frozen schema", MaxDiagnostics)
	}
}

// sleepAdapter is a fake Adapter whose Command runs a real subprocess that
// outlives any short timeout, so Run's timeout path can be exercised without
// depending on cargo ever actually being slow.
type sleepAdapter struct{}

func (sleepAdapter) Language() string        { return "slowlang" }
func (sleepAdapter) Route(check Check) Route { return RouteSubprocess }
func (sleepAdapter) Tool(check Check) string { return "sleep" }
func (sleepAdapter) Command(check Check) ([]string, error) {
	return []string{"5"}, nil
}
func (sleepAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	return nil, nil
}
func (sleepAdapter) RunInProcess(context.Context, Target) ([]Diagnostic, error) {
	return nil, errUnsupportedCheck("sleep", CheckBuild)
}

// multiToolAdapter is a fake Adapter fronting two distinct executables for
// two distinct checks, so Tool's per-check signature can be exercised
// without depending on cargoAdapter (which answers the same executable for
// every check it supports).
type multiToolAdapter struct{}

func (multiToolAdapter) Language() string        { return "multitool" }
func (multiToolAdapter) Route(check Check) Route { return RouteSubprocess }
func (multiToolAdapter) Tool(check Check) string {
	if check == CheckLint {
		return "linter-bin"
	}
	return "builder-bin"
}
func (multiToolAdapter) Command(check Check) ([]string, error) {
	if check == CheckLint {
		return []string{"lint", "--check"}, nil
	}
	return []string{"build"}, nil
}
func (multiToolAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	return nil, nil
}
func (multiToolAdapter) RunInProcess(_ context.Context, target Target) ([]Diagnostic, error) {
	return nil, errUnsupportedCheck("multitool", target.Check)
}

// TestVerifyToolVariesByCheck checks that Tool(check) is check-dependent:
// an adapter fronting more than one executable answers each check with its
// own tool, and Command's argv for that check stays consistent with the
// executable Tool names for it (never a "linter-bin" Tool paired with a
// "build" argv or vice versa).
func TestVerifyToolVariesByCheck(t *testing.T) {
	a := multiToolAdapter{}
	buildTool := a.Tool(CheckBuild)
	lintTool := a.Tool(CheckLint)
	if buildTool == lintTool {
		t.Fatalf("Tool(CheckBuild) == Tool(CheckLint) == %q, want two different executables", buildTool)
	}

	for _, check := range []Check{CheckBuild, CheckLint} {
		tool := a.Tool(check)
		argv, err := a.Command(check)
		if err != nil {
			t.Fatalf("Command(%s): %v", check, err)
		}
		if tool == "builder-bin" && (len(argv) == 0 || argv[0] != "build") {
			t.Fatalf("Tool(%s) = %q but Command(%s) = %v: executable and argv disagree", check, tool, check, argv)
		}
		if tool == "linter-bin" && (len(argv) == 0 || argv[0] != "lint") {
			t.Fatalf("Tool(%s) = %q but Command(%s) = %v: executable and argv disagree", check, tool, check, argv)
		}
	}
}

// TestVerifyTimeoutIsErrorNotRunResult checks a tool invocation that exceeds
// Options.Timeout surfaces as a Go error from Run, never as a RunResult —
// per the documented contract, only a tool that actually ran and reported
// gets to speak through a Diagnostic.
func TestVerifyTimeoutIsErrorNotRunResult(t *testing.T) {
	Register(sleepAdapter{})
	res, err := Run(context.Background(), Target{Language: "slowlang", Check: CheckBuild, Dir: t.TempDir()},
		Options{LogDir: t.TempDir(), Timeout: 100 * time.Millisecond})
	if err == nil {
		t.Fatalf("expected a timeout error, got RunResult %+v", res)
	}
	if res != nil {
		t.Fatalf("expected nil RunResult on timeout, got %+v", res)
	}
}

// TestVerifyConcurrentCacheWritesForDifferentTargetsBothPersist checks
// saveCache's state.WithLock serialization: two goroutines racing to record
// distinct target IDs against the same cache document must both survive,
// not have one clobber the other's read-modify-write.
func TestVerifyConcurrentCacheWritesForDifferentTargetsBothPersist(t *testing.T) {
	cacheDir := t.TempDir()
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("target-%d", i)
			errs[i] = saveCache(cacheDir, id, "hash-"+id, false, RunResult{ID: id})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("saveCache[%d]: %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("target-%d", i)
		cached, hit, err := lookupCache(cacheDir, id, "hash-"+id, false)
		if err != nil {
			t.Fatalf("lookupCache(%s): %v", id, err)
		}
		if !hit {
			t.Fatalf("lookupCache(%s): miss, want hit — a concurrent write was lost", id)
		}
		if cached.ID != id {
			t.Fatalf("lookupCache(%s): got ID %q, want %q", id, cached.ID, id)
		}
	}
}

// TestVerifyCargoArgvTableLockedPerCheck pins cargoAdapter.Command's argv
// for every check it supports: --locked on build, test and lint (so a stale
// Cargo.lock fails the run instead of silently re-resolving), and its
// absence on format — cargo fmt forwards unrecognized flags straight to
// rustfmt, which has no --locked and would fail the check on that flag alone
// rather than on anything about the code's formatting.
func TestVerifyCargoArgvTableLockedPerCheck(t *testing.T) {
	locked := map[Check]bool{
		CheckBuild:  true,
		CheckTest:   true,
		CheckLint:   true,
		CheckFormat: false,
	}
	for check, wantLocked := range locked {
		argv, err := cargoAdapter{}.Command(check)
		if err != nil {
			t.Fatalf("Command(%s): %v", check, err)
		}
		gotLocked := false
		for _, arg := range argv {
			if arg == "--locked" {
				gotLocked = true
			}
		}
		if gotLocked != wantLocked {
			t.Fatalf("Command(%s) = %v, --locked present = %v, want %v", check, argv, gotLocked, wantLocked)
		}
	}
}

// TestVerifyCargoFormatArgvIsFmtCheck pins the format check's argv exactly:
// `fmt --check`, cargo fmt's own dry-run flag, never `fmt` alone (which
// rewrites files) and never a flag order rustfmt would reject.
func TestVerifyCargoFormatArgvIsFmtCheck(t *testing.T) {
	argv, err := cargoAdapter{}.Command(CheckFormat)
	if err != nil {
		t.Fatalf("Command(CheckFormat): %v", err)
	}
	want := []string{"fmt", "--check"}
	if len(argv) != len(want) || argv[0] != want[0] || argv[1] != want[1] {
		t.Fatalf("Command(CheckFormat) = %v, want %v", argv, want)
	}
}

// TestVerifyClippyLintReportsWarningAsMustFail checks the cargo Adapter's
// lint Check (clippy) round-trips through Run exactly like build/test: a
// clippy-flagged lint is a Diagnostic that fails the run by default.
func TestVerifyClippyLintReportsWarningAsMustFail(t *testing.T) {
	// A clippy-only lint (needless_return) that plain rustc does not itself
	// warn on, so this exercises clippy's own diagnostic stream, not rustc's.
	const clippyLib = "pub fn f() -> i32 {\n    return 1;\n}\n"
	dir := writeCrate(t, "cratecliplint", clippyLib)
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckLint, Dir: dir}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tool != "cargo" {
		t.Fatalf("Tool = %q, want cargo", res.Tool)
	}
	if res.Counts.Warnings == 0 {
		t.Fatalf("Counts.Warnings = 0, want clippy to flag needless_return")
	}
	if res.Status == "" {
		t.Fatalf("Status is empty")
	}
}

// TestVerifyRerunOverwritesSameIDLogFileRatherThanAccumulating checks that
// re-running the identical target (same language/check/dir) reuses its
// deterministic ID as the log file name, so repeated runs against the same
// target don't leak one log file per invocation.
func TestVerifyRerunOverwritesSameIDLogFileRatherThanAccumulating(t *testing.T) {
	dir := writeCrate(t, "craterelogreuse", cleanLib)
	logDir := t.TempDir()
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}

	first, err := Run(context.Background(), target, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := Run(context.Background(), target, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if first.LogRef != second.LogRef {
		t.Fatalf("LogRef changed across identical reruns: %q vs %q", first.LogRef, second.LogRef)
	}
	if first.ID != second.ID {
		t.Fatalf("ID changed across identical reruns: %q vs %q", first.ID, second.ID)
	}
}

// TestVerifyRunIDNilArgsVsExplicitEmptyArgsDiffer documents runID's actual,
// deliberate behavior at the nil/empty boundary: Target.Args left nil
// (zero value) and Target.Args set to []string{} (explicitly no args)
// marshal to distinct JSON ("null" vs "[]") and so hash to distinct IDs.
// A caller that means "no args" must pick one representation consistently
// per target — silently switching between them changes the run identity
// and therefore the cache key and log file name.
func TestVerifyRunIDNilArgsVsExplicitEmptyArgsDiffer(t *testing.T) {
	nilArgs := Target{Language: "rust", Check: CheckBuild, Dir: "/tmp/x"}
	emptyArgs := Target{Language: "rust", Check: CheckBuild, Dir: "/tmp/x", Args: []string{}}

	nilID, err := runID(nilArgs)
	if err != nil {
		t.Fatalf("runID(nil Args): %v", err)
	}
	emptyID, err := runID(emptyArgs)
	if err != nil {
		t.Fatalf("runID(empty Args): %v", err)
	}
	if nilID == emptyID {
		t.Fatalf("runID nil-Args == explicit-empty-Args (%q); documented behavior expects these to differ ([]string(nil) marshals to null, []string{} marshals to []) — if this now matches, update the Args doc comment to state nil and empty are equivalent", nilID)
	}
}

// TestVerifyRunIDStableAcrossArgsElementOrder checks runID is sensitive to
// Args order: Run appends Args verbatim to argv (order affects the actual
// command line, e.g. flag-then-value pairs), so two targets whose Args
// hold the same elements in a different order must not collide on one
// cache entry / log file — a stale replay of the wrong ordering would be a
// silent correctness bug the tool never sees exercised.
func TestVerifyRunIDStableAcrossArgsElementOrder(t *testing.T) {
	a := Target{Language: "rust", Check: CheckBuild, Dir: "/tmp/x", Args: []string{"--target", "x86_64-unknown-linux-gnu"}}
	b := Target{Language: "rust", Check: CheckBuild, Dir: "/tmp/x", Args: []string{"x86_64-unknown-linux-gnu", "--target"}}

	idA, err := runID(a)
	if err != nil {
		t.Fatalf("runID(a): %v", err)
	}
	idB, err := runID(b)
	if err != nil {
		t.Fatalf("runID(b): %v", err)
	}
	if idA == idB {
		t.Fatalf("runID identical for Args in different order (%q); a reordering that changes the executed command line must not collide on one cache entry", idA)
	}
}

// TestVerifyConcurrentCacheWritesForArgsDifferingTargetsBothPersist mirrors
// TestVerifyConcurrentCacheWritesForDifferentTargetsBothPersist but drives
// the IDs through runID on targets that differ only in Args, so it also
// proves the Args-derived ID is what actually keys concurrent cache
// entries end to end — not just that runID returns distinct strings, but
// that saveCache/lookupCache correctly separate them under concurrent
// writers.
func TestVerifyConcurrentCacheWritesForArgsDifferingTargetsBothPersist(t *testing.T) {
	cacheDir := t.TempDir()
	const n = 20
	targets := make([]Target, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		targets[i] = Target{Language: "rust", Check: CheckBuild, Dir: "/tmp/x", Args: []string{fmt.Sprintf("--target-%d", i)}}
		id, err := runID(targets[i])
		if err != nil {
			t.Fatalf("runID[%d]: %v", i, err)
		}
		ids[i] = id
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			t.Fatalf("runID collision at index %d: %q already produced by an earlier Args-differing target", i, id)
		}
		seen[id] = true
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = saveCache(cacheDir, ids[i], "hash-shared-dir", false, RunResult{ID: ids[i]})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("saveCache[%d]: %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		cached, hit, err := lookupCache(cacheDir, ids[i], "hash-shared-dir", false)
		if err != nil {
			t.Fatalf("lookupCache(%s): %v", ids[i], err)
		}
		if !hit {
			t.Fatalf("lookupCache(%s): miss, want hit — a concurrent write for an Args-differing target was lost", ids[i])
		}
		if cached.ID != ids[i] {
			t.Fatalf("lookupCache(%s): got ID %q, want %q — Args-derived IDs collided in the cache document", ids[i], cached.ID, ids[i])
		}
	}
}

// routedAdapter is a fake Adapter split across both routes: CheckLint runs
// in-process under the fixed route name "go-analysis", every other check
// spawns /usr/bin/true. Both routes answer from the same diags slice, so a
// test can hold the findings constant and compare only what Run does around
// them. commandCalls and parseCalls record the subprocess-only members, so a
// test can assert the in-process route reached neither.
type routedAdapter struct {
	language string
	diags    []Diagnostic
	failWith error

	mu           sync.Mutex
	commandCalls int
	parseCalls   int
}

func (a *routedAdapter) Language() string { return a.language }

func (a *routedAdapter) Route(check Check) Route {
	if check == CheckLint {
		return RouteInProcess
	}
	return RouteSubprocess
}

func (a *routedAdapter) Tool(check Check) string {
	if check == CheckLint {
		return "go-analysis"
	}
	return "true"
}

func (a *routedAdapter) Command(check Check) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commandCalls++
	return []string{"--check"}, nil
}

func (a *routedAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.parseCalls++
	return a.diags, nil
}

func (a *routedAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	if a.failWith != nil {
		return nil, a.failWith
	}
	return a.diags, nil
}

func (a *routedAdapter) calls() (command, parse int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commandCalls, a.parseCalls
}

// errorDiagnostics builds n distinct error diagnostics, enough to drive the
// MaxDiagnostics cap when n exceeds it.
func errorDiagnostics(n int) []Diagnostic {
	diags := make([]Diagnostic, n)
	for i := range diags {
		diags[i] = Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("finding %d", i)}
	}
	return diags
}

// registerRouted registers a routedAdapter under a language unique to the
// calling test, since the registry is process-global.
func registerRouted(t *testing.T, language string, diags []Diagnostic) *routedAdapter {
	t.Helper()
	a := &routedAdapter{language: language, diags: diags}
	Register(a)
	return a
}

// TestVerifyInProcessRouteSkipsCommandParseAndSpawn checks the in-process
// route reaches none of Command, sysops.Run and Parse. Command and Parse are
// counted directly; the spawn is proved by the run succeeding at all, since
// Tool answers "go-analysis" there and no such executable exists on PATH —
// any attempt to spawn it would surface as an error from Run instead.
func TestVerifyInProcessRouteSkipsCommandParseAndSpawn(t *testing.T) {
	a := registerRouted(t, "routed-skip", []Diagnostic{{Severity: SeverityWarning, Message: "w"}})
	res, err := Run(context.Background(), Target{Language: a.language, Check: CheckLint, Dir: t.TempDir()},
		Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if command, parse := a.calls(); command != 0 || parse != 0 {
		t.Fatalf("in-process route called Command %d time(s) and Parse %d time(s), want 0 and 0", command, parse)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %d, want the 1 the in-process analysis returned", len(res.Diagnostics))
	}
}

// TestVerifyInProcessResultCarriesRouteNameAndEmptyCommand checks what an
// in-process run reports in place of a spawned tool: RunResult.Tool is the
// fixed route name, and the command field — on the result and in the log —
// is empty, because nothing was executed. The subprocess route on the same
// adapter carries a real argv, so this is a route difference and not an
// adapter that simply never reports one.
func TestVerifyInProcessResultCarriesRouteNameAndEmptyCommand(t *testing.T) {
	a := registerRouted(t, "routed-name", nil)
	dir, logDir := t.TempDir(), t.TempDir()

	inProc, err := Run(context.Background(), Target{Language: a.language, Check: CheckLint, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("in-process Run: %v", err)
	}
	if inProc.Tool != "go-analysis" {
		t.Fatalf("Tool = %q, want go-analysis", inProc.Tool)
	}
	if len(inProc.Command) != 0 {
		t.Fatalf("Command = %v, want empty on the in-process route", inProc.Command)
	}
	if logged := readLogDetail(t, inProc.LogRef); len(logged.Command) != 0 || logged.Tool != "go-analysis" {
		t.Fatalf("log carries tool %q command %v, want go-analysis and an empty command", logged.Tool, logged.Command)
	}

	sub, err := Run(context.Background(), Target{Language: a.language, Check: CheckBuild, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("subprocess Run: %v", err)
	}
	if len(sub.Command) == 0 {
		t.Fatalf("subprocess Command is empty; the test double must report an argv for the contrast to mean anything")
	}
	if sub.Tool == inProc.Tool {
		t.Fatalf("both routes reported tool %q, want the subprocess route to name its executable instead", sub.Tool)
	}
}

// TestVerifyBothRoutesCapCountClassifyAndLogAlike checks Run's shared tail is
// route-blind: fed the identical diagnostics, the two routes must agree on
// the counts, the capped slice, the overflow, the status, and the uncapped
// list written to the log. Only Tool, Command and the run ID may differ.
func TestVerifyBothRoutesCapCountClassifyAndLogAlike(t *testing.T) {
	const total = MaxDiagnostics + 5
	a := registerRouted(t, "routed-tail", errorDiagnostics(total))
	dir, logDir := t.TempDir(), t.TempDir()

	inProc, err := Run(context.Background(), Target{Language: a.language, Check: CheckLint, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("in-process Run: %v", err)
	}
	sub, err := Run(context.Background(), Target{Language: a.language, Check: CheckBuild, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("subprocess Run: %v", err)
	}

	if inProc.Counts != sub.Counts {
		t.Fatalf("counts differ by route: in-process %+v, subprocess %+v", inProc.Counts, sub.Counts)
	}
	if inProc.Counts.Errors != total {
		t.Fatalf("Counts.Errors = %d, want the uncapped %d", inProc.Counts.Errors, total)
	}
	if len(inProc.Diagnostics) != MaxDiagnostics || len(sub.Diagnostics) != MaxDiagnostics {
		t.Fatalf("capped lengths in-process %d, subprocess %d, want %d on both", len(inProc.Diagnostics), len(sub.Diagnostics), MaxDiagnostics)
	}
	if inProc.Overflow != sub.Overflow || inProc.Overflow != total-MaxDiagnostics {
		t.Fatalf("overflow in-process %d, subprocess %d, want %d on both", inProc.Overflow, sub.Overflow, total-MaxDiagnostics)
	}
	if inProc.Status != sub.Status {
		t.Fatalf("status differs by route: in-process %q, subprocess %q", inProc.Status, sub.Status)
	}
	if inProc.Status != clikit.StatusGateNegative {
		t.Fatalf("Status = %q, want %q for a run reporting errors", inProc.Status, clikit.StatusGateNegative)
	}
	for _, res := range []*RunResult{inProc, sub} {
		if logged := readLogDetail(t, res.LogRef); len(logged.Diagnostics) != total {
			t.Fatalf("log for tool %q holds %d diagnostics, want the uncapped %d", res.Tool, len(logged.Diagnostics), total)
		}
	}
}

// TestVerifyBothRoutesClassifyACleanRunAlike is the positive counterpart to
// the failing case above: with no diagnostics and nothing spawned, the
// in-process route must reach the same success verdict the subprocess route
// does rather than fall through to a default failure.
func TestVerifyBothRoutesClassifyACleanRunAlike(t *testing.T) {
	a := registerRouted(t, "routed-clean", nil)
	dir, logDir := t.TempDir(), t.TempDir()

	for _, check := range []Check{CheckLint, CheckBuild} {
		res, err := Run(context.Background(), Target{Language: a.language, Check: check, Dir: dir}, Options{LogDir: logDir})
		if err != nil {
			t.Fatalf("Run(%s): %v", check, err)
		}
		if res.Status != clikit.StatusSuccess {
			t.Fatalf("Run(%s) via %s: Status = %q, want %q", check, res.Tool, res.Status, clikit.StatusSuccess)
		}
	}
}

// TestVerifyBothRoutesReplayFromCacheAlike checks the content-hash cache
// covers the in-process route exactly as it covers a spawned one: an
// unchanged target replays its recorded verdict on the second call, and the
// replay reports the skip rather than a fresh execution.
func TestVerifyBothRoutesReplayFromCacheAlike(t *testing.T) {
	a := registerRouted(t, "routed-cache", errorDiagnostics(2))
	dir, logDir, cacheDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(dir+"/source.txt", []byte("unchanged"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	for _, check := range []Check{CheckLint, CheckBuild} {
		target := Target{Language: a.language, Check: check, Dir: dir}
		opts := Options{LogDir: logDir, CacheDir: cacheDir}
		first, err := Run(context.Background(), target, opts)
		if err != nil {
			t.Fatalf("first Run(%s): %v", check, err)
		}
		if first.Impact != ImpactExecuted {
			t.Fatalf("first Run(%s): Impact = %q, want %q", check, first.Impact, ImpactExecuted)
		}
		second, err := Run(context.Background(), target, opts)
		if err != nil {
			t.Fatalf("second Run(%s): %v", check, err)
		}
		if second.Impact != ImpactSkippedNoChange {
			t.Fatalf("second Run(%s): Impact = %q, want %q — the cache did not cover this route", check, second.Impact, ImpactSkippedNoChange)
		}
		if second.Tool != first.Tool || second.Counts != first.Counts || second.Status != first.Status {
			t.Fatalf("replayed verdict for %s differs from the recorded one: %+v vs %+v", check, second, first)
		}
	}
}

// TestVerifyInProcessFailureIsErrorNotRunResult checks the in-process route
// honors the same contract the spawned one does: an analysis that could not
// run is a Go error from Run, never a RunResult, and the adapter's sentinel
// survives Run's wrapping for an errors.Is caller.
func TestVerifyInProcessFailureIsErrorNotRunResult(t *testing.T) {
	a := registerRouted(t, "routed-fail", nil)
	a.failWith = errUnsupportedCheck("go-analysis", CheckLint)

	res, err := Run(context.Background(), Target{Language: a.language, Check: CheckLint, Dir: t.TempDir()},
		Options{LogDir: t.TempDir()})
	if err == nil {
		t.Fatalf("expected an error, got RunResult %+v", res)
	}
	if res != nil {
		t.Fatalf("expected nil RunResult on an in-process failure, got %+v", res)
	}
	if !errors.Is(err, ErrUnsupportedCheck) {
		t.Fatalf("errors.Is(err, ErrUnsupportedCheck) = false for %v; Run must wrap, not restate, the adapter's error", err)
	}
}

// readLogDetail decodes the uncapped record Run wrote at logRef.
func readLogDetail(t *testing.T, logRef string) logDetail {
	t.Helper()
	raw, err := os.ReadFile(logRef)
	if err != nil {
		t.Fatalf("read log %s: %v", logRef, err)
	}
	var detail logDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode log %s: %v", logRef, err)
	}
	return detail
}
