package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestMatrixPairCounts checks the dispatch table enumerates exactly the
// twenty-seven pairs section 4.7 names, with the per-language split Go seven,
// Rust eight, Python seven, shell five — SC1's after-value.
func TestMatrixPairCounts(t *testing.T) {
	m := Matrix()
	if len(m) != 27 {
		t.Fatalf("Matrix pair count = %d, want 27", len(m))
	}
	byLanguage := map[string]int{}
	for _, e := range m {
		byLanguage[e.Language]++
	}
	want := map[string]int{LanguageGo: 7, LanguageRust: 8, LanguagePython: 7, LanguageShell: 5}
	for lang, n := range want {
		if byLanguage[lang] != n {
			t.Errorf("%s pair count = %d, want %d", lang, byLanguage[lang], n)
		}
	}
	if len(byLanguage) != len(want) {
		t.Errorf("Matrix covers languages %v, want exactly %v", byLanguage, want)
	}
}

// TestMatrixImplementedCountMatchesBaseline checks the table marks exactly
// the nineteen pairs implemented today (Go seven, Rust eight, Python four),
// so the other eight fail closed until their adapter lands.
func TestMatrixImplementedCountMatchesBaseline(t *testing.T) {
	implemented := map[string]int{}
	total := 0
	for _, e := range Matrix() {
		if e.Implemented {
			implemented[e.Language]++
			total++
		}
	}
	if total != 19 {
		t.Fatalf("implemented pair count = %d, want 19", total)
	}
	want := map[string]int{LanguageGo: 7, LanguageRust: 8, LanguagePython: 4}
	for lang, n := range want {
		if implemented[lang] != n {
			t.Errorf("%s implemented count = %d, want %d", lang, implemented[lang], n)
		}
	}
	if implemented[LanguageShell] != 0 {
		t.Errorf("shell implemented count = %d, want 0 (no shell adapter yet)", implemented[LanguageShell])
	}
}

// TestMatrixTestSubcommandLayer checks CheckTest is the only check carrying a
// subcommand, always names one of the three kinds, and never appears as a
// bare "test" pair — so coverage and structured reports have no pair of their
// own and instead ride test unit.
func TestMatrixTestSubcommandLayer(t *testing.T) {
	for _, e := range Matrix() {
		if e.Check == CheckTest {
			switch e.Test {
			case TestUnit, TestE2E, TestBenchmark:
			default:
				t.Errorf("%s test pair has kind %q, want one of unit/e2e/benchmark", e.Language, e.Test)
			}
			continue
		}
		if e.Test != "" {
			t.Errorf("%s %s carries test kind %q, want none on a non-test check", e.Language, e.Check, e.Test)
		}
	}
	// Rust alone takes benchmark; no other language does.
	for _, e := range Matrix() {
		if e.Test == TestBenchmark && e.Language != LanguageRust {
			t.Errorf("%s takes a benchmark pair, want Rust only", e.Language)
		}
	}
}

// TestMatrixEveryPairResolvesToAnEntry checks every pair the grid regenerates
// is present in the committed table via LookupPair — the "every MATRIX pair
// resolves to an adapter entry" clause.
func TestMatrixEveryPairResolvesToAnEntry(t *testing.T) {
	for _, e := range Matrix() {
		got, ok := LookupPair(e.Language, e.Check, e.Test)
		if !ok {
			t.Errorf("LookupPair(%q,%q,%q) = not found, want the pair's entry", e.Language, e.Check, e.Test)
			continue
		}
		if got.PairID() != e.PairID() {
			t.Errorf("LookupPair returned %q, want %q", got.PairID(), e.PairID())
		}
	}
}

// TestResolveCheckUnsupportedPairs checks every request that is not a
// declared, implemented pair — an out-of-matrix language/check, and a
// declared pair no adapter implements yet — resolves to the unsupported
// diagnostic at EXIT 80 rather than a silent pass.
func TestResolveCheckUnsupportedPairs(t *testing.T) {
	cases := []struct {
		name     string
		language string
		check    Check
		test     TestKind
	}{
		{"shell build is out of matrix", LanguageShell, CheckBuild, ""},
		{"go benchmark is out of matrix", LanguageGo, CheckTest, TestBenchmark},
		{"unknown language", "ruby", CheckBuild, ""},
		{"bare test is not a pair", LanguageGo, CheckTest, ""},
		{"python vet declared but unimplemented", LanguagePython, CheckVet, ""},
		{"shell lint declared but unimplemented", LanguageShell, CheckLint, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry, diag := ResolveCheck(c.language, c.check, c.test)
			if diag == nil {
				t.Fatalf("ResolveCheck(%q,%q,%q) = entry %+v, nil diag; want unsupported diagnostic", c.language, c.check, c.test, entry)
			}
			if diag.Code != DiagUnsupportedCheck {
				t.Errorf("diagnostic code = %q, want %q", diag.Code, DiagUnsupportedCheck)
			}
			class := strings.SplitN(diag.Code, ".", 2)[0]
			if got := clikit.Status(class).ExitCode(); got != ExitUnsupported {
				t.Errorf("exit code for %q = %d, want %d", diag.Code, got, ExitUnsupported)
			}
		})
	}
}

// TestResolveCheckImplementedPair checks an implemented pair resolves to its
// entry with no diagnostic — the pass-through the unsupported cases invert.
func TestResolveCheckImplementedPair(t *testing.T) {
	entry, diag := ResolveCheck(LanguageGo, CheckBuild, "")
	if diag != nil {
		t.Fatalf("ResolveCheck(go,build) diag = %+v, want nil for an implemented pair", diag)
	}
	if entry.PairID() != "go build" {
		t.Fatalf("entry PairID = %q, want %q", entry.PairID(), "go build")
	}
}

// TestResolveCheckRustEightPairsNeverExit80 checks every Rust MATRIX pair —
// build, format, lint, vet, security and all three test kinds — resolves to
// its declared entry with no diagnostic, the table-level evidence for AC1
// (Rust dispatches its eight MATRIX pairs, none returning EXIT 80). This is
// the pure dispatch check, with no tool spawned; the adapter's own probe
// suite exercises the real cargo subcommands.
func TestResolveCheckRustEightPairsNeverExit80(t *testing.T) {
	pairs := []struct {
		check Check
		test  TestKind
	}{
		{CheckBuild, ""},
		{CheckFormat, ""},
		{CheckLint, ""},
		{CheckVet, ""},
		{CheckSecurity, ""},
		{CheckTest, TestUnit},
		{CheckTest, TestE2E},
		{CheckTest, TestBenchmark},
	}
	if len(pairs) != 8 {
		t.Fatalf("test table lists %d pairs, want 8", len(pairs))
	}
	for _, p := range pairs {
		entry, diag := ResolveCheck(LanguageRust, p.check, p.test)
		if diag != nil {
			t.Errorf("ResolveCheck(rust,%s,%s) = diag %+v, want nil (implemented pair)", p.check, p.test, diag)
			continue
		}
		if entry.Language != LanguageRust || entry.Check != p.check || entry.Test != p.test {
			t.Errorf("ResolveCheck(rust,%s,%s) entry = %+v, want a matching Rust entry", p.check, p.test, entry)
		}
	}
}

// TestMatrixMultiToolPairs checks OD46 is encoded — a pair can name more than
// one tool. Go lint names both golangci-lint and an import organiser, which
// is the acceptance example, and Go security names two tools as well.
func TestMatrixMultiToolPairs(t *testing.T) {
	goLint, ok := LookupPair(LanguageGo, CheckLint, "")
	if !ok {
		t.Fatal("go lint pair not found")
	}
	if len(goLint.Tools) < 2 {
		t.Fatalf("go lint tools = %v, want at least two (OD46)", goLint.Tools)
	}
	if !containsTool(goLint.Tools, "golangci-lint") || !containsTool(goLint.Tools, "goimports") {
		t.Errorf("go lint tools = %v, want both golangci-lint and goimports", goLint.Tools)
	}
}

// TestMatrixConfigSeam checks each pair whose tool reads a config records that
// config's base name, resolved from the language-tools tree (OD47/SC37): the
// four configs the fleet owns, and no config on a pair whose tools read none.
func TestMatrixConfigSeam(t *testing.T) {
	cases := []struct {
		language string
		check    Check
		config   string
	}{
		{LanguageGo, CheckLint, ".golangci.yml"},
		{LanguageRust, CheckLint, "clippy.toml"},
		{LanguagePython, CheckLint, "ruff.toml"},
		{LanguagePython, CheckFormat, "ruff.toml"},
		{LanguageShell, CheckLint, ".shellcheckrc"},
		{LanguageGo, CheckBuild, ""},
		{LanguageRust, CheckFormat, ""},
	}
	for _, c := range cases {
		e, ok := LookupPair(c.language, c.check, "")
		if !ok {
			t.Errorf("pair %s %s not found", c.language, c.check)
			continue
		}
		if e.Config != c.config {
			t.Errorf("%s %s config = %q, want %q", c.language, c.check, e.Config, c.config)
		}
	}
}

// TestMatrixReproParity is the REPRO regenerate-check: the pair set rebuilt
// from MATRIX must equal the committed table byte-for-byte, so a pair dropped
// from the table or invented in it fails this test (E9, both directions).
func TestMatrixReproParity(t *testing.T) {
	parity := VerifyMatrixParity()
	if !parity.Match {
		t.Fatalf("committed table drifted from MATRIX:\nregenerated:\n%s\ncommitted:\n%s",
			parity.Regenerated, parity.Committed)
	}
	if got := len(strings.Split(parity.Committed, "\n")); got != 27 {
		t.Fatalf("committed pair count = %d, want 27", got)
	}
}

// TestExitCodeConstantsMatchClikit checks the EXIT-code constants the contract
// restates still agree with clikit's exit taxonomy, so the two never drift.
func TestExitCodeConstantsMatchClikit(t *testing.T) {
	cases := []struct {
		status clikit.Status
		want   int
	}{
		{clikit.StatusSuccess, ExitSuccess},
		{clikit.StatusGateNegative, ExitCheckFailed},
		{clikit.StatusUnsupported, ExitUnsupported},
		{clikit.StatusInternal, ExitRunFailed},
	}
	for _, c := range cases {
		if got := c.status.ExitCode(); got != c.want {
			t.Errorf("clikit %s exit code = %d, contract constant = %d", c.status, got, c.want)
		}
	}
}

// containsTool reports whether tools holds want.
func containsTool(tools []string, want string) bool {
	for _, t := range tools {
		if t == want {
			return true
		}
	}
	return false
}
