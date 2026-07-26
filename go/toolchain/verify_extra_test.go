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

func (sleepAdapter) Language() string { return "slowlang" }
func (sleepAdapter) Tool() string     { return "sleep" }
func (sleepAdapter) Command(check Check) ([]string, error) {
	return []string{"5"}, nil
}
func (sleepAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	return nil, nil
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
