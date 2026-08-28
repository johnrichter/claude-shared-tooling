package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This file drives Run() against a real, temporary Cargo crate for every
// Rust check the dispatch table declares, spawning the real tools (cargo
// build/fmt/clippy/check, cargo-audit, cargo-deny, cargo-nextest,
// cargo-llvm-cov, cargo bench) rather than exercising only the pure parser
// functions in cargo.go. Every sub-test skips (never fails) a tool absent
// from PATH, so this suite degrades gracefully on a runner missing one
// binary instead of reporting a false negative — but a present tool is
// never mocked.

// requireCargoTool skips the calling test if tool is not resolvable on
// PATH. An absent cargo plugin binary is an environment gap, not a defect
// in the adapter.
func requireCargoTool(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s not on PATH; skipping (not a defect in the adapter)", tool)
	}
}

// writeRustProbeCrate lays out a crate under a fresh t.TempDir() with:
//   - a lib (add) and a bin (main, printing add's result) so cargo has both
//     a library and an executable target;
//   - tests/e2e.rs, its own nextest binary by Cargo convention, driving the
//     crate's own CLI through assert_cmd — never a binary this adapter
//     spawns itself;
//   - benches/b.rs, a criterion harness wired with harness = false, again
//     never a spawned binary;
//   - an explicit SPDX license and a deny.toml that allows the
//     MIT/Apache-2.0/Unlicense set assert_cmd and criterion's own dependency
//     trees carry — without either, cargo-deny's strict default license
//     policy fails any crate that doesn't ship its own allow-list, which is
//     a property of cargo-deny's default config, not of the adapter under
//     test.
//
// libRS is the lib's own body, letting a caller introduce a defect scoped to
// one check (e.g. a test-only compile error) without touching the rest of
// the crate.
func writeRustProbeCrate(t *testing.T, libRS string) string {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not on PATH")
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("Cargo.toml", `[package]
name = "rustprobe"
version = "0.1.0"
edition = "2021"
license = "MIT"

[dependencies]
assert_cmd = "2"

[dev-dependencies]
criterion = "0.5"

[[bench]]
name = "b"
harness = false
`)
	write("deny.toml", `[licenses]
allow = ["MIT", "Apache-2.0", "Unlicense"]

[bans]
multiple-versions = "allow"

[sources]
unknown-registry = "allow"
unknown-git = "allow"
`)
	write("src/lib.rs", libRS)
	write("src/main.rs", "fn main() { println!(\"{}\", rustprobe::add(2, 3)); }\n")
	write("tests/e2e.rs", `use assert_cmd::Command;

#[test]
fn cli_runs() {
    let mut cmd = Command::cargo_bin("rustprobe").unwrap();
    cmd.assert().success();
}
`)
	write("benches/b.rs", `use criterion::{criterion_group, criterion_main, Criterion};

fn bench(c: &mut Criterion) {
    c.bench_function("add", |b| b.iter(|| rustprobe::add(2, 3)));
}
criterion_group!(benches, bench);
criterion_main!(benches);
`)

	lock := exec.Command("cargo", "generate-lockfile")
	lock.Dir = dir
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("cargo generate-lockfile: %v\n%s", err, out)
	}
	fmtCmd := exec.Command("cargo", "fmt")
	fmtCmd.Dir = dir
	if out, err := fmtCmd.CombinedOutput(); err != nil {
		t.Fatalf("cargo fmt (seed formatting): %v\n%s", err, out)
	}
	return dir
}

const probeCleanLibRS = "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n"

// rustProbePairs enumerates the eight Rust dispatch-table checks and the
// tool set each needs actually present to run for real (never mocked).
var rustProbePairs = []struct {
	name  string
	check Check
	test  TestKind
	tools []string
}{
	{"build", CheckBuild, "", []string{"cargo"}},
	{"format", CheckFormat, "", []string{"cargo"}},
	{"lint", CheckLint, "", []string{"cargo-clippy"}},
	{"vet", CheckVet, "", []string{"cargo"}},
	{"security", CheckSecurity, "", []string{"cargo-audit", "cargo-deny"}},
	{"test unit", CheckTest, TestUnit, []string{"cargo-nextest", "cargo-llvm-cov"}},
	{"test e2e", CheckTest, TestE2E, []string{"cargo-nextest"}},
	{"test benchmark", CheckTest, TestBenchmark, []string{"cargo"}},
}

// TestE2ERustAdapterEightPairsCleanInputNeverExit80 runs every declared Rust
// check against a real clean crate and asserts none resolves to EXIT 80
// (unsupported) — the concrete evidence that Rust dispatches all eight
// checks the standard names, with none falling back to the unsupported
// diagnostic.
func TestE2ERustAdapterEightPairsCleanInputNeverExit80(t *testing.T) {
	dir := writeRustProbeCrate(t, probeCleanLibRS)
	logDir := t.TempDir()

	if len(rustProbePairs) != 8 {
		t.Fatalf("rustProbePairs lists %d pairs, want 8", len(rustProbePairs))
	}

	for _, p := range rustProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requireCargoTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguageRust, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() == ExitUnsupported {
				t.Fatalf("Run(%s) resolved to EXIT %d (unsupported), want a resolved check", p.name, ExitUnsupported)
			}
			if res.Tool != "cargo" {
				t.Errorf("Run(%s): RunResult.Tool = %q, want %q (cargoAdapter.Tool answers cargo for every check)", p.name, res.Tool, "cargo")
			}
		})
	}
}

// TestE2ERustAdapterEightPairsCleanInputExitZero is the clean-input
// counter-probe per check: on a genuinely clean crate, each check should
// classify as success (EXIT 0).
func TestE2ERustAdapterEightPairsCleanInputExitZero(t *testing.T) {
	dir := writeRustProbeCrate(t, probeCleanLibRS)
	logDir := t.TempDir()

	for _, p := range rustProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requireCargoTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguageRust, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() != ExitSuccess {
				t.Errorf("Run(%s) on clean input = status %s (exit %d), want success (exit 0); diagnostics=%+v", p.name, res.Status, res.Status.ExitCode(), res.Diagnostics)
			}
		})
	}
}

// TestE2ERustAdapterVetCatchesTestOnlyDefectBuildMisses is the ground-truth
// probe for vet resolving to the compiler: it introduces a compile error
// that exists only inside a #[cfg(test)] module, so plain `cargo build`
// (build's narrower default target set) compiles clean while `cargo check
// --all-targets --all-features` (vet's widened surface) fails — proving
// vet's own check catches what build's does not, rather than duplicating
// it.
func TestE2ERustAdapterVetCatchesTestOnlyDefectBuildMisses(t *testing.T) {
	requireCargoTool(t, "cargo")
	const libWithBadTest = "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn broken() {\n        let _x: i32 = \"not an int\";\n    }\n}\n"
	dir := writeRustProbeCrate(t, libWithBadTest)
	logDir := t.TempDir()

	buildRes, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckBuild, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(build): unexpected infrastructure error: %v", err)
	}
	if buildRes.Status.ExitCode() != ExitSuccess {
		t.Fatalf("Run(build) on a test-only defect = status %s (exit %d), want success (build never compiles test code); diagnostics=%+v",
			buildRes.Status, buildRes.Status.ExitCode(), buildRes.Diagnostics)
	}

	vetRes, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckVet, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(vet): unexpected infrastructure error: %v", err)
	}
	if vetRes.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(vet) on a test-only defect = status %s (exit %d), want gate_negative (exit %d) — vet's widened --all-targets must catch what build misses; diagnostics=%+v",
			vetRes.Status, vetRes.Status.ExitCode(), ExitCheckFailed, vetRes.Diagnostics)
	}
	if vetRes.Tool != "cargo" {
		t.Errorf("Run(vet).Tool = %q, want %q — vet resolves to the compiler, never a second linter binary", vetRes.Tool, "cargo")
	}
}

// TestE2ERustAdapterSecurityFindsLicensePolicyViolation runs security
// against a crate whose deny.toml allows nothing, so cargo-deny's own
// license check fails even though cargo-audit finds no advisory — evidence
// that security invokes both cargo-audit and cargo-deny as two questions
// neither answers alone, so a failure can come from just one of them.
func TestE2ERustAdapterSecurityFindsLicensePolicyViolation(t *testing.T) {
	requireCargoTool(t, "cargo-audit")
	requireCargoTool(t, "cargo-deny")
	dir := writeRustProbeCrate(t, probeCleanLibRS)
	// Overwrite deny.toml with an empty allow-list: every license, including
	// the crate's own declared MIT, is now rejected by cargo-deny alone.
	if err := os.WriteFile(filepath.Join(dir, "deny.toml"), []byte("[licenses]\nallow = []\n"), 0o644); err != nil {
		t.Fatalf("overwrite deny.toml: %v", err)
	}
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckSecurity, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(security): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(security) with an empty deny.toml allow-list = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			res.Status, res.Status.ExitCode(), ExitCheckFailed, res.Diagnostics)
	}
	foundDeny := false
	for _, d := range res.Diagnostics {
		if containsSubstring(d.Message, cargoDenyTool) {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Errorf("Run(security) diagnostics = %+v, want at least one attributed to %q", res.Diagnostics, cargoDenyTool)
	}
}

// TestE2ERustAdapterUnitTestProducesCoverageInSameRun runs the unit-test
// check through the real `cargo llvm-cov nextest` invocation against a
// crate with one failing test, asserting (a) the failure surfaces as an
// attributed diagnostic and (b) cargoCoverageFile lands in target.Dir from
// this one run.
func TestE2ERustAdapterUnitTestProducesCoverageInSameRun(t *testing.T) {
	requireCargoTool(t, "cargo-nextest")
	requireCargoTool(t, "cargo-llvm-cov")
	const libWithFailingUnitTest = "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn add_is_wrong_on_purpose() {\n        assert_eq!(add(2, 3), 6);\n    }\n}\n"
	dir := writeRustProbeCrate(t, libWithFailingUnitTest)
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckTest, Test: TestUnit, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test unit): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(test unit) on a failing test = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			res.Status, res.Status.ExitCode(), ExitCheckFailed, res.Diagnostics)
	}
	if len(res.Diagnostics) == 0 {
		t.Errorf("Run(test unit) diagnostics = %+v, want at least one failure diagnostic", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(dir, cargoCoverageFile)); err != nil {
		t.Errorf("coverage file %s not written by test unit's own run: %v", cargoCoverageFile, err)
	}
}

// TestE2ERustAdapterE2ETestDrivesAssertCmdBinary runs the e2e-test check
// against a crate whose tests/e2e.rs (its own nextest binary by Cargo
// convention) uses assert_cmd to drive the crate's own CLI, and asserts a
// failing e2e binary surfaces as a gate_negative diagnostic distinct from
// the unit-test check (unit's own test file carries no e2e-shaped defect
// here) — evidence that e2e drives assert_cmd against the crate's compiled
// binary rather than reusing the unit-test path.
func TestE2ERustAdapterE2ETestDrivesAssertCmdBinary(t *testing.T) {
	requireCargoTool(t, "cargo-nextest")
	dir := writeRustProbeCrate(t, probeCleanLibRS)
	// Replace the e2e test with one asserting failure on a binary that
	// actually succeeds, so the e2e check itself reports a failure that the
	// unit-test check (a clean lib, no #[test] of its own here) does not.
	failingE2E := `use assert_cmd::Command;

#[test]
fn cli_should_fail_but_does_not() {
    let mut cmd = Command::cargo_bin("rustprobe").unwrap();
    cmd.assert().failure();
}
`
	if err := os.WriteFile(filepath.Join(dir, "tests", "e2e.rs"), []byte(failingE2E), 0o644); err != nil {
		t.Fatalf("overwrite tests/e2e.rs: %v", err)
	}
	logDir := t.TempDir()

	e2eRes, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckTest, Test: TestE2E, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test e2e): unexpected infrastructure error: %v", err)
	}
	if e2eRes.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(test e2e) on a deliberately-wrong assert_cmd expectation = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			e2eRes.Status, e2eRes.Status.ExitCode(), ExitCheckFailed, e2eRes.Diagnostics)
	}

	unitRes, err := Run(context.Background(), Target{Language: LanguageRust, Check: CheckTest, Test: TestUnit, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test unit): unexpected infrastructure error: %v", err)
	}
	if unitRes.Status.ExitCode() != ExitSuccess {
		t.Fatalf("Run(test unit) on the same crate = status %s (exit %d), want success — the e2e-only failure must not bleed into the unit check; diagnostics=%+v",
			unitRes.Status, unitRes.Status.ExitCode(), unitRes.Diagnostics)
	}
}

// TestE2ERustAdapterBenchmarkDispatchableOnEveryFleetTarget asserts the
// benchmark check's dispatch — Route, Tool and the dispatch-table entry, the
// parts that could hard-code a host assumption — carries no
// platform-specific branch, so the same dispatch would resolve identically
// on any host platform rather than being gated to whichever one happens to
// run this test. Executing cargo bench for real only proves the local host;
// this test proves the adapter's own decision never depends on the host's
// own OS/architecture.
func TestE2ERustAdapterBenchmarkDispatchableOnEveryFleetTarget(t *testing.T) {
	a := cargoAdapter{}
	if got := a.Route(CheckTest); got != RouteInProcess {
		t.Fatalf("Route(CheckTest) = %q, want %q — benchmark dispatches through the same in-process test route as unit/e2e, not a host-gated one", got, RouteInProcess)
	}
	if tool := a.Tool(CheckTest); tool != "cargo" {
		t.Fatalf("Tool(CheckTest) = %q, want %q", tool, "cargo")
	}
	entry, ok := LookupPair(LanguageRust, CheckTest, TestBenchmark)
	if !ok {
		t.Fatal("LookupPair(rust, test, benchmark) = not found, want the declared entry")
	}
	if entry.PairID() != "rust test benchmark" {
		t.Fatalf("PairID = %q, want %q", entry.PairID(), "rust test benchmark")
	}
	for _, tool := range entry.Tools {
		if tool == "" {
			t.Fatalf("benchmark entry names an empty tool: %+v", entry)
		}
	}
	_, diag := ResolveCheck(LanguageRust, CheckTest, TestBenchmark)
	if diag != nil {
		t.Fatalf("ResolveCheck(rust, test, benchmark) = diag %+v, want nil — the same resolution outcome regardless of which platform asks", diag)
	}
}
