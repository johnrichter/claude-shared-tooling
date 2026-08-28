package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// manyWarningsLib is cratemany-shaped source: 25 unused-variable warnings in
// one function, well past MaxDiagnostics, so the cap-and-overflow path is
// exercised against a real, uncontrived cargo run rather than a fixture
// hand-shaped to fit the cap.
func manyWarningsLib(n int) string {
	src := "pub fn f() {\n"
	for i := 0; i < n; i++ {
		src += fmt.Sprintf("    let unused%d = %d;\n", i, i)
	}
	return src + "}\n"
}

// TestAdversarialDiagnosticsCappedWithOverflowAndFullLog checks a run
// producing more than MaxDiagnostics caps the returned slice, counts the
// remainder in Overflow, and still writes every diagnostic — capped and
// overflow alike — to the log file LogRef names.
func TestAdversarialDiagnosticsCappedWithOverflowAndFullLog(t *testing.T) {
	const total = 25
	dir := writeCrate(t, "cratemanyadv", manyWarningsLib(total))
	logDir := t.TempDir()
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: logDir, AllowWarnings: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Counts.Warnings != total {
		t.Fatalf("Counts.Warnings = %d, want %d (the true, uncapped count)", res.Counts.Warnings, total)
	}
	if len(res.Diagnostics) != MaxDiagnostics {
		t.Fatalf("len(Diagnostics) = %d, want exactly MaxDiagnostics (%d)", len(res.Diagnostics), MaxDiagnostics)
	}
	if res.Overflow != total-MaxDiagnostics {
		t.Fatalf("Overflow = %d, want %d", res.Overflow, total-MaxDiagnostics)
	}

	raw, err := os.ReadFile(res.LogRef)
	if err != nil {
		t.Fatalf("read log_ref %s: %v", res.LogRef, err)
	}
	var detail logDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("log_ref is not valid JSON: %v", err)
	}
	if len(detail.Diagnostics) != total {
		t.Fatalf("log_ref carries %d diagnostics, want all %d (the cap must never drop data, only defer it)", len(detail.Diagnostics), total)
	}
}

// TestAdversarialContentHashDetectsSingleByteChange checks ContentHash is
// sensitive to a one-byte edit anywhere under the tree, not just to which
// files are present.
func TestAdversarialContentHashDetectsSingleByteChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, err := ContentHash(dir)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if err := os.WriteFile(path, []byte("hellp"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	after, err := ContentHash(dir)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if before == after {
		t.Fatalf("ContentHash unchanged after a single-byte edit")
	}
}

// TestAdversarialContentHashIgnoresBuildOutputDirs checks a change confined
// to a build-output directory (e.g. target/, the very thing a build run
// produces) never invalidates the cache — otherwise every run would
// invalidate its own next cache entry.
func TestAdversarialContentHashIgnoresBuildOutputDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.rs"), []byte("fn f() {}"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	before, err := ContentHash(dir)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	targetDir := filepath.Join(dir, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "artifact.bin"), []byte("built bytes"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	after, err := ContentHash(dir)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if before != after {
		t.Fatalf("ContentHash changed after writing only under target/, want unaffected (before=%s after=%s)", before, after)
	}
}

// TestAdversarialCacheCorruptDocumentDegradesToMiss checks a corrupt cache
// file never blocks a run: it is treated as a cache miss (execute, don't
// error), matching state.Read's own safe-degradation contract.
func TestAdversarialCacheCorruptDocumentDegradesToMiss(t *testing.T) {
	dir := writeCrate(t, "cratecachecorrupt", cleanLib)
	cacheDir := t.TempDir()
	if err := os.WriteFile(cachePath(cacheDir), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt cache: %v", err)
	}
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: t.TempDir(), CacheDir: cacheDir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Impact != ImpactExecuted {
		t.Fatalf("Impact = %q, want executed (a corrupt cache must degrade to a miss, not an error)", res.Impact)
	}
}

// TestAdversarialCacheDoesNotReplayAcrossWarningPolicy checks the cache never
// serves a verdict classified under a different must-fail-on-warning policy:
// the same unchanged warning-only crate is gate_negative under the default
// but success under AllowWarnings, so a second run that flips the policy must
// reclassify (execute), not replay the stale verdict from the first.
func TestAdversarialCacheDoesNotReplayAcrossWarningPolicy(t *testing.T) {
	dir := writeCrate(t, "cratepolicyflip", warningLib)
	cacheDir := t.TempDir()
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}

	first, err := Run(context.Background(), target, Options{LogDir: t.TempDir(), CacheDir: cacheDir})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Status != clikit.StatusGateNegative {
		t.Fatalf("first Status = %q, want gate_negative under the default", first.Status)
	}

	second, err := Run(context.Background(), target, Options{LogDir: t.TempDir(), CacheDir: cacheDir, AllowWarnings: true})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Impact != ImpactExecuted {
		t.Fatalf("second Impact = %q, want executed (policy changed, a stale verdict must not be replayed)", second.Impact)
	}
	if second.Status != clikit.StatusSuccess {
		t.Fatalf("second Status = %q, want success under AllowWarnings", second.Status)
	}
}

// TestAdversarialUnknownLanguageErrors checks Run refuses a language with no
// registered adapter, rather than silently no-op-ing.
func TestAdversarialUnknownLanguageErrors(t *testing.T) {
	_, err := Run(context.Background(), Target{Language: "cobol", Check: CheckBuild, Dir: t.TempDir()}, Options{LogDir: t.TempDir()})
	if err == nil {
		t.Fatalf("expected an error for an unregistered language")
	}
}

// TestAdversarialMissingLogDirErrors checks Run refuses to run without
// somewhere to write the uncapped detail — a capped RunResult with no
// LogRef target would silently lose data rather than defer it.
func TestAdversarialMissingLogDirErrors(t *testing.T) {
	dir := writeCrate(t, "cratenologdir", cleanLib)
	_, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{})
	if err == nil {
		t.Fatalf("expected an error for Options.LogDir empty")
	}
}

// TestAdversarialCargoRejectsUnsupportedCheck checks the cargo Adapter
// reports an error, not a zero-value argv, for a Check it has no cargo
// subcommand for.
func TestAdversarialCargoRejectsUnsupportedCheck(t *testing.T) {
	_, err := cargoAdapter{}.Command(Check("nonexistent-check"))
	if err == nil {
		t.Fatalf("expected an error for an unsupported check")
	}
}

// TestAdversarialErrUnsupportedCheckMatchesSecurity checks errors.Is matches
// the exported ErrUnsupportedCheck sentinel against the error
// cargoAdapter.Command returns for CheckSecurity — a check declared in
// types.go that Command has no cargo subcommand for directly (it routes
// in-process instead) — not merely that an error is non-nil, and not by
// comparing error text.
func TestAdversarialErrUnsupportedCheckMatchesSecurity(t *testing.T) {
	_, err := cargoAdapter{}.Command(CheckSecurity)
	if err == nil {
		t.Fatalf("Command(%s): expected an error, got nil", CheckSecurity)
	}
	if !errors.Is(err, ErrUnsupportedCheck) {
		t.Fatalf("Command(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", CheckSecurity, err)
	}
}

// TestAdversarialRunPropagatesErrUnsupportedCheck checks Run's own error for
// a check the registered adapter rejects still satisfies errors.Is against
// ErrUnsupportedCheck — the sentinel must survive Run's passthrough
// (run.go's `if err != nil { return nil, err }` immediately after
// adapter.Command), not just Command's own direct return. python vet is the
// example here: pythonAdapter routes every check through the subprocess
// path, and its Command has no equivalent for vet.
func TestAdversarialRunPropagatesErrUnsupportedCheck(t *testing.T) {
	_, err := Run(context.Background(), Target{Language: "python", Check: CheckVet, Dir: t.TempDir()}, Options{LogDir: t.TempDir()})
	if err == nil {
		t.Fatalf("Run: expected an error for CheckVet against uv, got nil")
	}
	if !errors.Is(err, ErrUnsupportedCheck) {
		t.Fatalf("Run: errors.Is(err, ErrUnsupportedCheck) = false; err = %v", err)
	}
}

// TestAdversarialErrUnsupportedCheckDoesNotMatchUnrelatedError checks
// errors.Is against ErrUnsupportedCheck is a real sentinel match, not a
// tautology that would pass against any error — an unrelated error must not
// spuriously match.
func TestAdversarialErrUnsupportedCheckDoesNotMatchUnrelatedError(t *testing.T) {
	unrelated := errors.New("toolchain: some other failure")
	if errors.Is(unrelated, ErrUnsupportedCheck) {
		t.Fatalf("errors.Is matched an unrelated error against ErrUnsupportedCheck")
	}
}
