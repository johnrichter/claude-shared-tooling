package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// writeCrate lays out a minimal Cargo crate under t.TempDir() with lib.rs's
// contents given verbatim, and returns the crate root. Each call gets its
// own directory, so tests never share a cache or build-output state.
func writeCrate(t *testing.T, name, libRS string) string {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not on PATH")
	}
	dir := t.TempDir()
	toml := "[package]\nname = \"" + name + "\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(libRS), 0o644); err != nil {
		t.Fatalf("write lib.rs: %v", err)
	}
	// Pre-generate Cargo.lock so a crate's tree is stable across repeated
	// Runs: cargo build itself creates/updates Cargo.lock as a side effect
	// on its first invocation, which would otherwise change ContentHash
	// between two calls that never touched a source file.
	lock := exec.Command("cargo", "generate-lockfile")
	lock.Dir = dir
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("cargo generate-lockfile: %v\n%s", err, out)
	}
	return dir
}

const cleanLib = "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n"
const warningLib = "pub fn add(a: i32, b: i32) -> i32 {\n    let unused = 5;\n    a + b\n}\n"
const errorLib = "pub fn add(a: i32, b: i32) -> i32 {\n    a +\n}\n"

// TestSanityCleanBuildSucceeds checks a clean crate reports Success with no
// diagnostics and a log file that exists.
func TestSanityCleanBuildSucceeds(t *testing.T) {
	dir := writeCrate(t, "cratecleansanity", cleanLib)
	logDir := t.TempDir()
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusSuccess {
		t.Fatalf("Status = %q, want success", res.Status)
	}
	if res.Counts.Errors != 0 || res.Counts.Warnings != 0 {
		t.Fatalf("Counts = %+v, want zero", res.Counts)
	}
	if len(res.Diagnostics) != 0 || res.Overflow != 0 {
		t.Fatalf("Diagnostics/Overflow = %+v/%d, want empty/0", res.Diagnostics, res.Overflow)
	}
	if res.Impact != ImpactExecuted {
		t.Fatalf("Impact = %q, want executed", res.Impact)
	}
	if _, err := os.Stat(res.LogRef); err != nil {
		t.Fatalf("log_ref %s does not exist: %v", res.LogRef, err)
	}
	if res.SchemaVersion != SchemaVersion || res.Tool != "cargo" || res.Language != "rust" {
		t.Fatalf("schema identity fields wrong: %+v", res)
	}
}

// TestSanityWarningFailsByDefault checks the must-fail-on-warning default:
// Options{} (AllowWarnings false) turns a warning-only build into
// GateNegative.
func TestSanityWarningFailsByDefault(t *testing.T) {
	dir := writeCrate(t, "cratewarnsanity", warningLib)
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusGateNegative {
		t.Fatalf("Status = %q, want gate_negative (warnings must fail by default)", res.Status)
	}
	if res.Counts.Warnings != 1 {
		t.Fatalf("Counts.Warnings = %d, want 1", res.Counts.Warnings)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Severity != SeverityWarning {
		t.Fatalf("Diagnostics = %+v, want one warning", res.Diagnostics)
	}
}

// TestSanityAllowWarningsOptsOutOfMustFail checks a caller can explicitly
// relax the default and get Success on a warning-only build.
func TestSanityAllowWarningsOptsOutOfMustFail(t *testing.T) {
	dir := writeCrate(t, "cratewarnallow", warningLib)
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: t.TempDir(), AllowWarnings: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusSuccess {
		t.Fatalf("Status = %q, want success with AllowWarnings", res.Status)
	}
}

// TestSanityCompileErrorFails checks a crate that fails to compile reports
// GateNegative with the error captured as a Diagnostic.
func TestSanityCompileErrorFails(t *testing.T) {
	dir := writeCrate(t, "crateerrsanity", errorLib)
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusGateNegative {
		t.Fatalf("Status = %q, want gate_negative", res.Status)
	}
	if res.Counts.Errors == 0 {
		t.Fatalf("Counts.Errors = 0, want > 0")
	}
}

// TestSanityCacheSkipsUnchangedTarget checks the Level-0 content-hash cache:
// a second Run against an unchanged target is skipped and replays the first
// run's verdict rather than re-invoking cargo.
func TestSanityCacheSkipsUnchangedTarget(t *testing.T) {
	dir := writeCrate(t, "cratecachesanity", cleanLib)
	cacheDir := t.TempDir()
	opts := Options{LogDir: t.TempDir(), CacheDir: cacheDir}
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}

	first, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Impact != ImpactExecuted {
		t.Fatalf("first Impact = %q, want executed", first.Impact)
	}

	second, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Impact != ImpactSkippedNoChange {
		t.Fatalf("second Impact = %q, want skipped_no_change", second.Impact)
	}
	if second.DurationMS != 0 {
		t.Fatalf("second DurationMS = %d, want 0 for a skipped run", second.DurationMS)
	}
	if second.Status != first.Status || second.ID != first.ID {
		t.Fatalf("cached verdict diverged from original: %+v vs %+v", second, first)
	}
}

// TestSanityCacheRerunsAfterContentChange checks a source edit invalidates
// the cache: a target whose content hash changed executes again rather than
// replaying the stale verdict.
func TestSanityCacheRerunsAfterContentChange(t *testing.T) {
	dir := writeCrate(t, "cratecachechange", cleanLib)
	cacheDir := t.TempDir()
	opts := Options{LogDir: t.TempDir(), CacheDir: cacheDir}
	target := Target{Language: "rust", Check: CheckBuild, Dir: dir}

	if _, err := Run(context.Background(), target, opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(warningLib), 0o644); err != nil {
		t.Fatalf("edit lib.rs: %v", err)
	}
	second, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Impact != ImpactExecuted {
		t.Fatalf("second Impact = %q, want executed after content change", second.Impact)
	}
	if second.Counts.Warnings != 1 {
		t.Fatalf("second Counts.Warnings = %d, want 1 (edited crate now warns)", second.Counts.Warnings)
	}
}

// TestSanityArgsAppendedToExecutedArgv checks Target.Args lands at the end
// of the argv Run actually executes (captured verbatim on RunResult.Command).
func TestSanityArgsAppendedToExecutedArgv(t *testing.T) {
	dir := writeCrate(t, "crateargssanity", cleanLib)
	args := []string{"--release", "x86_64-unknown-linux-gnu"}
	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckBuild, Dir: dir, Args: args}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Command) < len(args) {
		t.Fatalf("Command = %v, too short to hold Args %v", res.Command, args)
	}
	got := res.Command[len(res.Command)-len(args):]
	if got[0] != args[0] || got[1] != args[1] {
		t.Fatalf("Command tail = %v, want Args %v", got, args)
	}
}

// TestSanityArgsAloneChangeRunIdentity checks two targets that differ only
// in Args get distinct run identities, so they never collide on one cache
// entry or one log file.
func TestSanityArgsAloneChangeRunIdentity(t *testing.T) {
	dir := writeCrate(t, "crateargsidsanity", cleanLib)
	debug := Target{Language: "rust", Check: CheckBuild, Dir: dir, Args: []string{"--target", "x86_64-unknown-linux-gnu"}}
	release := Target{Language: "rust", Check: CheckBuild, Dir: dir, Args: []string{"--release", "x86_64-unknown-linux-gnu"}}

	debugRes, err := Run(context.Background(), debug, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run debug: %v", err)
	}
	releaseRes, err := Run(context.Background(), release, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run release: %v", err)
	}
	if debugRes.ID == releaseRes.ID {
		t.Fatalf("ID = %q for both targets, want distinct IDs for distinct Args", debugRes.ID)
	}
}

// unformattedLib is valid Rust that rustfmt would rewrite (spacing around
// the signature and body), so it fails a format check without failing a
// build.
const unformattedLib = "pub fn add(a:i32,b:i32)->i32{a+b}\n"

// TestSanityFormatCheckFailsAndLeavesFileUnchanged checks the format check
// against a crate with one unformatted file: the run reports GateNegative,
// and the file's bytes on disk are exactly what they were before the run —
// cargo fmt --check must never rewrite what it's asked only to check.
func TestSanityFormatCheckFailsAndLeavesFileUnchanged(t *testing.T) {
	dir := writeCrate(t, "cratefmtsanity", unformattedLib)
	libPath := filepath.Join(dir, "src", "lib.rs")
	before, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read lib.rs before run: %v", err)
	}

	res, err := Run(context.Background(), Target{Language: "rust", Check: CheckFormat, Dir: dir}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != clikit.StatusGateNegative {
		t.Fatalf("Status = %q, want gate_negative for an unformatted file", res.Status)
	}
	if res.Counts.Errors == 0 {
		t.Fatalf("Counts.Errors = 0, want > 0 for a failed format check")
	}

	after, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read lib.rs after run: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("lib.rs changed by the format check: before=%q after=%q", before, after)
	}
}

// TestSanityVerifyBinaryParityMatchesFreshBuild checks the committed==fresh
// parity check against a trivially reproducible Go build, and that a
// tampered committed artifact is caught as a mismatch.
func TestSanityVerifyBinaryParityMatchesFreshBuild(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	buildDir := t.TempDir()
	src := "package main\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte("module paritysanity\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	committedPath := filepath.Join(buildDir, "committed")
	freshPath := "fresh-output" // relative to buildDir, kept apart from committedPath
	buildCmd := []string{"go", "build", "-o", freshPath, "."}
	seed := exec.Command("go", "build", "-o", committedPath, ".")
	seed.Dir = buildDir
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed build: %v\n%s", err, out)
	}

	parity, err := VerifyBinaryParity(context.Background(), committedPath, buildCmd, buildDir, filepath.Join(buildDir, freshPath))
	if err != nil {
		t.Fatalf("VerifyBinaryParity: %v", err)
	}
	if !parity.Match {
		t.Fatalf("parity.Match = false for an untouched binary, want true (committed=%s fresh=%s)", parity.Committed, parity.Fresh)
	}

	if err := os.WriteFile(committedPath, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("tamper binary: %v", err)
	}
	parity, err = VerifyBinaryParity(context.Background(), committedPath, buildCmd, buildDir, filepath.Join(buildDir, freshPath))
	if err != nil {
		t.Fatalf("VerifyBinaryParity after tamper: %v", err)
	}
	if parity.Match {
		t.Fatalf("parity.Match = true for a tampered binary, want false")
	}
}
