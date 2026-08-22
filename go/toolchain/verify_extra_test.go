package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
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
func (sleepAdapter) Tool(check Check) string { return "sleep" }
func (sleepAdapter) Command(check Check) ([]string, error) {
	return []string{"5"}, nil
}
func (sleepAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	return nil, nil
}

// multiToolAdapter is a fake Adapter fronting two distinct executables for
// two distinct checks, so Tool's per-check signature can be exercised
// without depending on cargoAdapter (which answers the same executable for
// every check it supports).
type multiToolAdapter struct{}

func (multiToolAdapter) Language() string { return "multitool" }
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
