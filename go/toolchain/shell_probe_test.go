package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This file drives Run() against a real, temporary shell script root for
// every MATRIX pair shellAdapter declares, spawning the real tools (shfmt,
// shellcheck, checkbashisms, semgrep, bats, kcov) rather than exercising
// only the pure parser functions in shell.go. Every sub-test skips (never
// fails) a tool absent from PATH, mirroring rust_probe_test.go's,
// python_probe_test.go's and golang_e2e_probe_test.go's own convention. A
// present tool is never mocked.

// requireShellTool skips the calling test if tool is not resolvable on
// PATH. An absent tool is an environment gap, not a defect in the adapter.
func requireShellTool(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s not on PATH; skipping (not a defect in the adapter)", tool)
	}
}

// shellProbeCleanScript is pre-formatted to shfmt's own default style (tab
// indent) and assigns $1/$2 to named variables before the arithmetic
// expansion, rather than expanding them directly inside it — semgrep's r/bash
// ruleset's unquoted-variable-expansion-in-command rule flags a bare
// positional parameter inside $((...)) despite the surrounding double quotes,
// so the counter-probe must avoid that shape to stay genuinely clean under
// all three of shfmt -d, shellcheck and semgrep --config r/bash (all confirm
// zero findings against this exact byte sequence).
const shellProbeCleanScript = "#!/usr/bin/env bash\nset -euo pipefail\n\nadd() {\n\ta=\"$1\"\n\tb=\"$2\"\n\techo \"$((a + b))\"\n}\n\nadd 2 3\n"

const shellProbeDirtyScript = "#!/bin/bash\nfoo=$1\necho $foo\nif [ $foo == \"x\" ]; then\n  echo `pwd`\nfi\n"

// shellProbeUnformattedScript is shfmt-clean content deliberately laid out
// with two-space vs tab inconsistency shfmt -l flags without shellcheck or
// checkbashisms ever objecting to it, isolating the format pair's own
// counter-probe from the other pairs' diagnostics.
const shellProbeUnformattedScript = "#!/usr/bin/env bash\nif true; then\n echo hi\nfi\n"

// writeShellProbeRoot lays out a shell script root under a fresh t.TempDir()
// with: bin/build.sh (an extension-carrying script), bin/run (an
// extensionless script whose first line is a shell shebang — the shape
// discoverShellFiles's isShellShebang matches, per F49's nine extensionless
// scripts), .githooks/pre-commit (proving discovery walks .githooks/ rather
// than skipping it, per OD54), and a bats suite under test/ split into a
// unit file and an e2e-tagged file, the same unit/e2e partition
// runUnitTest's and runE2ETest's own --filter-tags flags select between.
func writeShellProbeRoot(t *testing.T, mainScript string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string, mode os.FileMode) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("bin/build.sh", mainScript, 0o755)
	write("bin/run", mainScript, 0o755) // extensionless, shell shebang
	write(".githooks/pre-commit", "#!/bin/sh\necho pre-commit\n", 0o755)
	write("test/unit.bats", "#!/usr/bin/env bats\n\n@test \"addition works\" {\n  run bash -c 'echo $((2+3))'\n  [ \"$status\" -eq 0 ]\n  [ \"$output\" = \"5\" ]\n}\n", 0o644)
	write("test/e2e.bats", "#!/usr/bin/env bats\n# bats file_tags=e2e\n\n@test \"smoke\" {\n  run true\n  [ \"$status\" -eq 0 ]\n}\n", 0o644)
	return dir
}

// shellProbePairs enumerates the five shell MATRIX pairs this suite drives,
// mirroring committedMatrix's five shell entries, and the tool set each
// needs actually present to run for real (never mocked).
var shellProbePairs = []struct {
	name  string
	check Check
	test  TestKind
	tools []string
}{
	{"format", CheckFormat, "", []string{"shfmt"}},
	{"lint", CheckLint, "", []string{"shellcheck", "checkbashisms"}},
	{"security", CheckSecurity, "", []string{"semgrep"}},
	{"test unit", CheckTest, TestUnit, []string{"bats", "kcov"}},
	{"test e2e", CheckTest, TestE2E, []string{"bats"}},
}

// TestE2EShellAdapterFivePairsCleanInputNeverExit80 runs every declared
// shell check against a real clean script root and asserts none resolves to
// EXIT 80 (unsupported) — the concrete evidence for AC1 (shell dispatches
// its five MATRIX pairs, none returning EXIT 80).
func TestE2EShellAdapterFivePairsCleanInputNeverExit80(t *testing.T) {
	dir := writeShellProbeRoot(t, shellProbeCleanScript)
	logDir := t.TempDir()

	if len(shellProbePairs) != 5 {
		t.Fatalf("shellProbePairs lists %d pairs, want 5", len(shellProbePairs))
	}

	for _, p := range shellProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requireShellTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguageShell, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() == ExitUnsupported {
				t.Fatalf("Run(%s) resolved to EXIT %d (unsupported) — AC1 violated: %+v", p.name, ExitUnsupported, res)
			}
			if res.Tool == "" {
				t.Errorf("Run(%s): RunResult.Tool is empty, want a named tool", p.name)
			}
		})
	}
}

// TestE2EShellAdapterFormatDetectsUnformattedFile is the format pair's
// dirty-input probe: shfmt -l over a badly-indented script must report at
// least one diagnostic naming the offending file.
func TestE2EShellAdapterFormatDetectsUnformattedFile(t *testing.T) {
	requireShellTool(t, "shfmt")
	dir := writeShellProbeRoot(t, shellProbeUnformattedScript)
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguageShell, Check: CheckFormat, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(format): unexpected infrastructure error: %v", err)
	}
	if res.Counts.Errors == 0 {
		t.Errorf("Run(format) on an unformatted script reported 0 errors, want >=1 (README format row)")
	}
}

// TestE2EShellAdapterLintDetectsShellcheckAndBashism is the lint pair's
// dirty-input probe: an unquoted variable and a backtick command
// substitution should surface at least one shellcheck finding.
func TestE2EShellAdapterLintDetectsShellcheckAndBashism(t *testing.T) {
	requireShellTool(t, "shellcheck")
	requireShellTool(t, "checkbashisms")
	dir := writeShellProbeRoot(t, shellProbeDirtyScript)
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguageShell, Check: CheckLint, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(lint): unexpected infrastructure error: %v", err)
	}
	if res.Counts.Errors == 0 && res.Counts.Warnings == 0 {
		t.Errorf("Run(lint) on a script with unquoted vars / backticks reported no diagnostics, want >=1 (README lint row)")
	}
}

// TestE2EShellAdapterFivePairsCleanInputExitZero is the test-strategy's
// clean-input counter-probe per pair: on a genuinely clean script root, each
// pair should classify as success (EXIT 0).
func TestE2EShellAdapterFivePairsCleanInputExitZero(t *testing.T) {
	dir := writeShellProbeRoot(t, shellProbeCleanScript)
	logDir := t.TempDir()

	for _, p := range shellProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requireShellTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguageShell, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() != 0 {
				t.Errorf("Run(%s) on a clean script root: EXIT %d, want 0; diagnostics: %+v", p.name, res.Status.ExitCode(), res.Diagnostics)
			}
		})
	}
}

// TestE2EShellAdapterBuildAndVetUnsupported is AC2's concrete evidence: a
// shell target has no build and no vet pair in MATRIX, so both resolve to
// EXIT 80 (DiagUnsupportedCheck) through ResolveCheck, never a silent pass,
// and (defense in depth) the adapter's own RunInProcess default case answers
// the same error if a caller bypasses ResolveCheck entirely.
func TestE2EShellAdapterBuildAndVetUnsupported(t *testing.T) {
	for _, check := range []Check{CheckBuild, CheckVet} {
		t.Run(string(check), func(t *testing.T) {
			_, diag := ResolveCheck(LanguageShell, check, "")
			if diag == nil {
				t.Fatalf("ResolveCheck(shell, %s) = nil diagnostic, want unsupported (AC2)", check)
			}
			if diag.Code != DiagUnsupportedCheck {
				t.Errorf("ResolveCheck(shell, %s) diagnostic code = %q, want %q", check, diag.Code, DiagUnsupportedCheck)
			}

			a, ok := lookup(LanguageShell)
			if !ok {
				t.Fatalf("no adapter registered for %q", LanguageShell)
			}
			_, err := a.RunInProcess(context.Background(), Target{Language: LanguageShell, Check: check, Dir: t.TempDir()})
			if err == nil {
				t.Errorf("shellAdapter.RunInProcess(%s): nil error, want ErrUnsupportedCheck (adapter's own defense in depth)", check)
			}
		})
	}
}

// writeBareBatsFile writes a single untagged .bats file directly at dir's
// root — no test/ subdirectory, no bats file_tags/test_tags annotation —
// the shape a bats suite takes when a project never adopts OD51's tagging
// convention. content is the file's full body.
func writeBareBatsFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const shellBareUnitCleanBats = "#!/usr/bin/env bats\n\n@test \"probe passes\" {\n  [ 1 -eq 1 ]\n}\n"
const shellBareUnitFaultBats = "#!/usr/bin/env bats\n\n@test \"probe fails\" {\n  [ 1 -eq 2 ]\n}\n"

// TestE2EShellAdapterUnitTestSeparatesUntaggedBareBatsFile is the test-unit
// pair's counter-probe for a suite with no test/ subdirectory and no bats
// tag at all: a clean assertion must resolve EXIT 0 and a failing one must
// resolve EXIT 20 with a positioned diagnostic, even though the suite
// carries neither a fixed suite-root directory nor a bats tag to lean on.
func TestE2EShellAdapterUnitTestSeparatesUntaggedBareBatsFile(t *testing.T) {
	requireShellTool(t, "bats")
	requireShellTool(t, "kcov")
	logDir := t.TempDir()

	clean := t.TempDir()
	writeBareBatsFile(t, clean, "probe.bats", shellBareUnitCleanBats)
	res, err := Run(context.Background(), Target{Language: LanguageShell, Check: CheckTest, Test: TestUnit, Dir: clean}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test unit) on a clean bare bats file: unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitSuccess {
		t.Errorf("Run(test unit) on a clean bare bats file: EXIT %d, want %d", res.Status.ExitCode(), ExitSuccess)
	}

	fault := t.TempDir()
	writeBareBatsFile(t, fault, "probe.bats", shellBareUnitFaultBats)
	res, err = Run(context.Background(), Target{Language: LanguageShell, Check: CheckTest, Test: TestUnit, Dir: fault}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test unit) on a fault bare bats file: unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Errorf("Run(test unit) on a fault bare bats file: EXIT %d, want %d", res.Status.ExitCode(), ExitCheckFailed)
	}
	if len(res.Diagnostics) == 0 {
		t.Errorf("Run(test unit) on a fault bare bats file: no diagnostics, want one naming the failing test")
	}
}

const shellBareE2ECliScript = "#!/usr/bin/env bash\necho \"hi, $1\"\n"
const shellBareE2ECleanBats = "#!/usr/bin/env bats\n\n@test \"cli greets\" {\n  run bash \"$BATS_TEST_DIRNAME/cli.sh\" world\n  [ \"$output\" = \"hi, world\" ]\n}\n"
const shellBareE2EFaultBats = "#!/usr/bin/env bats\n\n@test \"cli greets\" {\n  run bash \"$BATS_TEST_DIRNAME/cli.sh\" world\n  [ \"$output\" = \"hello, world\" ]\n}\n"

// TestE2EShellAdapterE2ETestSeparatesUntaggedBareBatsFile is the test-e2e
// pair's counter-probe for the same untagged, test/-free shape: a bats file
// asserting the wrong CLI output must resolve EXIT 20 with a positioned
// diagnostic, and the correct assertion must resolve EXIT 0.
func TestE2EShellAdapterE2ETestSeparatesUntaggedBareBatsFile(t *testing.T) {
	requireShellTool(t, "bats")
	logDir := t.TempDir()

	clean := t.TempDir()
	writeBareBatsFile(t, clean, "cli.sh", shellBareE2ECliScript)
	writeBareBatsFile(t, clean, "cli.bats", shellBareE2ECleanBats)
	res, err := Run(context.Background(), Target{Language: LanguageShell, Check: CheckTest, Test: TestE2E, Dir: clean}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test e2e) on a clean bare bats file: unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitSuccess {
		t.Errorf("Run(test e2e) on a clean bare bats file: EXIT %d, want %d", res.Status.ExitCode(), ExitSuccess)
	}

	fault := t.TempDir()
	writeBareBatsFile(t, fault, "cli.sh", shellBareE2ECliScript)
	writeBareBatsFile(t, fault, "cli.bats", shellBareE2EFaultBats)
	res, err = Run(context.Background(), Target{Language: LanguageShell, Check: CheckTest, Test: TestE2E, Dir: fault}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test e2e) on a fault bare bats file: unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Errorf("Run(test e2e) on a fault bare bats file: EXIT %d, want %d", res.Status.ExitCode(), ExitCheckFailed)
	}
	if len(res.Diagnostics) == 0 {
		t.Errorf("Run(test e2e) on a fault bare bats file: no diagnostics, want one naming the failing test")
	}
}

const shellNonE2ETaggedBats = "#!/usr/bin/env bats\n# bats file_tags=slow\n\n@test \"slow probe passes\" {\n  [ 1 -eq 1 ]\n}\n"

// TestE2EShellAdapterE2ETestGatesSuiteCarryingOnlyANonE2ETag is the
// tag-fallback's own counter-probe: a suite that tags one file with
// something other than e2e (`# bats file_tags=slow`) still declares no e2e
// tag, so `--filter-tags e2e` over it would select zero tests and bats
// would exit 0 — the exact vacuous pass that let the shteste fault arm
// through before this repair, reachable by tagging any file in the suite
// for an unrelated reason. batsFilterTagsArgs therefore emits no filter
// here, and the failing test gates.
func TestE2EShellAdapterE2ETestGatesSuiteCarryingOnlyANonE2ETag(t *testing.T) {
	requireShellTool(t, "bats")
	logDir := t.TempDir()

	fault := t.TempDir()
	writeBareBatsFile(t, fault, "slow.bats", shellNonE2ETaggedBats)
	writeBareBatsFile(t, fault, "cli.sh", shellBareE2ECliScript)
	writeBareBatsFile(t, fault, "cli.bats", shellBareE2EFaultBats)
	res, err := Run(context.Background(), Target{Language: LanguageShell, Check: CheckTest, Test: TestE2E, Dir: fault}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test e2e) on a suite tagged only `slow`: unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Errorf("Run(test e2e) on a suite tagged only `slow` carrying a failing test: EXIT %d, want %d — an unrelated tag must not turn the e2e arm into a zero-selection pass",
			res.Status.ExitCode(), ExitCheckFailed)
	}
	if len(res.Diagnostics) == 0 {
		t.Errorf("Run(test e2e) on a suite tagged only `slow`: no diagnostics, want one naming the failing test")
	}
}

// TestBatsFilterTagsArgsRequiresAnE2ETag pins batsFilterTagsArgs's
// soundness condition: it emits --filter-tags only for a suite that
// actually declares the e2e tag, reading each declaration's tag values
// rather than its mere presence. A suite with no tag, and one tagging
// something else entirely, both run in full for either kind.
func TestBatsFilterTagsArgsRequiresAnE2ETag(t *testing.T) {
	const untagged = "#!/usr/bin/env bats\n\n@test \"a\" {\n  [ 1 -eq 1 ]\n}\n"
	const taggedE2EFile = "#!/usr/bin/env bats\n# bats file_tags=e2e\n\n@test \"b\" {\n  [ 1 -eq 1 ]\n}\n"
	const taggedE2EAmongOthers = "#!/usr/bin/env bats\n\n# bats test_tags=slow, e2e ,flaky\n@test \"c\" {\n  [ 1 -eq 1 ]\n}\n"

	cases := []struct {
		name     string
		files    map[string]string
		wantUnit []string
		wantE2E  []string
	}{
		{"untagged suite", map[string]string{"probe.bats": untagged}, nil, nil},
		{"suite tagging only a non-e2e tag", map[string]string{"slow.bats": shellNonE2ETaggedBats, "probe.bats": untagged}, nil, nil},
		{"suite tagging e2e on one file", map[string]string{"e2e.bats": taggedE2EFile, "probe.bats": untagged},
			[]string{"--filter-tags", "!e2e"}, []string{"--filter-tags", "e2e"}},
		{"suite tagging e2e among other tags", map[string]string{"mixed.bats": taggedE2EAmongOthers},
			[]string{"--filter-tags", "!e2e"}, []string{"--filter-tags", "e2e"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				writeBareBatsFile(t, dir, name, content)
			}
			files, err := discoverBatsFiles(dir)
			if err != nil {
				t.Fatalf("discoverBatsFiles: %v", err)
			}
			if len(files) != len(tc.files) {
				t.Fatalf("discoverBatsFiles found %d files, want %d: %v", len(files), len(tc.files), files)
			}
			for _, kind := range []struct {
				kind TestKind
				want []string
			}{{TestUnit, tc.wantUnit}, {TestE2E, tc.wantE2E}} {
				got := batsFilterTagsArgs(files, kind.kind)
				if len(got) != len(kind.want) {
					t.Fatalf("batsFilterTagsArgs(%s) = %v, want %v", kind.kind, got, kind.want)
				}
				for i := range got {
					if got[i] != kind.want[i] {
						t.Errorf("batsFilterTagsArgs(%s)[%d] = %q, want %q", kind.kind, i, got[i], kind.want[i])
					}
				}
			}
		})
	}
}

// TestShellDiscoveryMatchesF49Population is the test-strategy's discovery
// test: discoverShellFiles must find every .sh file and every extensionless
// shell-shebang file at or below the root, .githooks/ included (OD54), and
// must not pick up a non-shell file placed alongside them — the population
// rule F49 measured against the fleet (117 in-scope files on 2026-08-25,
// 109 outside .githooks/ and 8 inside it) rather than a manifest-driven set.
func TestShellDiscoveryMatchesF49Population(t *testing.T) {
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

	write("bin/build.sh", shellProbeCleanScript)
	write("bin/run", shellProbeCleanScript)                // extensionless, shell shebang: in scope
	write(".githooks/pre-commit", "#!/bin/sh\ntrue\n")     // .githooks/ is walked, not skipped
	write("README.md", "# not shell\n")                    // wrong extension: out of scope
	write("bin/binary-no-shebang", "\x00\x01\x02not text") // extensionless, no shell shebang: out of scope
	write("bin/python-shebang", "#!/usr/bin/env python3\nprint('hi')\n")

	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatalf("discoverShellFiles: %v", err)
	}

	want := map[string]bool{
		filepath.Join(dir, "bin/build.sh"):         true,
		filepath.Join(dir, "bin/run"):              true,
		filepath.Join(dir, ".githooks/pre-commit"): true,
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	for f := range want {
		if !got[f] {
			t.Errorf("discoverShellFiles missed %s, want it in scope (F49 population)", f)
		}
	}
	for f := range got {
		if !want[f] {
			t.Errorf("discoverShellFiles included %s, want it out of scope (not .sh, no shell shebang)", f)
		}
	}
	if len(files) != len(want) {
		t.Errorf("discoverShellFiles found %d files, want %d: %v", len(files), len(want), files)
	}
}
