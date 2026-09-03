package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Adversarial probes authored by test-engineer to stress shell.go beyond the
// existing suite: boundary/edge cases in discovery, config content, and
// failure-mode behavior when a required tool is entirely absent.

func TestEngAdvUppercaseSHExtensionMatches(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "script.SH")
	if err := os.WriteFile(full, []byte(shellProbeCleanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f == full {
			found = true
		}
	}
	if !found {
		t.Errorf("discoverShellFiles did not match .SH (case-insensitive ext expected): %v", files)
	}
}

func TestEngAdvEmptyFileNoPanic(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "empty")
	if err := os.WriteFile(full, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == full {
			t.Errorf("empty extensionless file with no shebang wrongly classified as shell: %v", files)
		}
	}
}

func TestEngAdvNestedDirsRecursed(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(deep, "nested.sh")
	if err := os.WriteFile(full, []byte(shellProbeCleanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != full {
		t.Errorf("discoverShellFiles did not recurse arbitrarily deep: got %v, want [%s]", files, full)
	}
}

func TestEngAdvGitDirSkippedEvenWithShellFile(t *testing.T) {
	dir := t.TempDir()
	gitHook := filepath.Join(dir, ".git", "hooks", "pre-commit.sample.sh")
	if err := os.MkdirAll(filepath.Dir(gitHook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitHook, []byte(shellProbeCleanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("discoverShellFiles descended into .git: %v, want none", files)
	}
}

func TestEngAdvShebangWithLeadingWhitespaceRejected(t *testing.T) {
	// A shebang line must start at byte 0 to be honored by the OS loader;
	// verify isShellShebang does not falsely match a shifted shebang-like
	// string past the read limit or with leading content.
	dir := t.TempDir()
	full := filepath.Join(dir, "notreally")
	if err := os.WriteFile(full, []byte(" #!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isShellShebang(full) {
		t.Errorf("isShellShebang matched a shebang not at byte offset 0")
	}
}

func TestEngAdvMissingRequiredToolIsInfraErrorNotSilentPass(t *testing.T) {
	// Point PATH somewhere with no tools at all and confirm runFormat surfaces
	// an error (never nil, nil) when shfmt cannot be found — AC2's "never a
	// silent pass" principle extended to a genuinely absent executable, not
	// just an out-of-matrix check.
	dir := t.TempDir()
	full := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(full, []byte(shellProbeCleanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	a, ok := lookup(LanguageShell)
	if !ok {
		t.Fatalf("no adapter registered for shell")
	}
	_, err := a.RunInProcess(context.Background(), Target{Language: LanguageShell, Check: CheckFormat, Dir: dir})
	if err == nil {
		t.Errorf("RunInProcess(format) with shfmt absent from PATH: nil error, want an infrastructure error (never a silent pass)")
	}
}

func TestEngAdvShellcheckConfigContainsExternalSources(t *testing.T) {
	path, err := shellcheckConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "external-sources=true") {
		t.Errorf(".shellcheckrc content = %q, want external-sources=true (OD12 delegates the include list to ci-shell.yml)", string(b))
	}
	if !strings.HasSuffix(filepath.Base(path), ".shellcheckrc") {
		t.Errorf("shellcheckConfigPath base = %q, want .shellcheckrc (SC37)", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "language-tools" {
		t.Errorf("shellcheckConfigPath parent dir = %q, want language-tools (SC37: every tool config lives in language-tools)", filepath.Base(filepath.Dir(path)))
	}
}

func TestEngAdvSemgrepRulesetIsBashSpecific(t *testing.T) {
	if shellSecurityConfig != "r/bash" {
		t.Errorf("shellSecurityConfig = %q, want r/bash (AC3+E6: F49 measured zero .zsh/.fish files, ruling out a broader ruleset)", shellSecurityConfig)
	}
}

func TestEngAdvToolAndCommandForBuildAndVetNeverPanicAndAlwaysError(t *testing.T) {
	a := shellAdapter{}
	for _, check := range []Check{CheckBuild, CheckVet} {
		if got := a.Tool(check); got == "" {
			t.Errorf("Tool(%s) returned empty string, want a defined fallback name", check)
		}
		_, err := a.Command(check)
		if err == nil {
			t.Errorf("Command(%s): nil error, want ErrUnsupportedCheck", check)
		}
	}
}

func TestEngAdvRunFormatEmptyDirIsTrivialPassNotError(t *testing.T) {
	a := shellAdapter{}
	dir := t.TempDir()
	diags, err := a.RunInProcess(context.Background(), Target{Language: LanguageShell, Check: CheckFormat, Dir: dir})
	if err != nil {
		t.Fatalf("RunInProcess(format) on an empty root: unexpected error %v, want trivial pass", err)
	}
	if len(diags) != 0 {
		t.Errorf("RunInProcess(format) on an empty root produced diagnostics %v, want none", diags)
	}
}

func TestEngAdvSymlinkedShellFileDiscovered(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.sh")
	if err := os.WriteFile(real, []byte(shellProbeCleanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sh")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	if !got[link] {
		t.Errorf("discoverShellFiles did not include symlinked .sh file %s: %v", link, files)
	}
}
