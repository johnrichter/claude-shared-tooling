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
