package toolchain

// Adversarial coverage for the checkbashisms exit-4 masking runLint added
// (T8 criterion 4, the run's 55th error). checkbashisms is absent on this
// host, so these fake the binary at a controlled exit code — the same
// fixture style config_path_qa_test.go uses for shellcheck/golangci-lint —
// rather than skip the masking logic untested. shellcheck is also faked with
// an empty json1 report so runLint's first half never confuses the result.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// diagFromCheckbashisms reports whether d's Message names checkbashisms —
// Diagnostic carries no Tool field, so runLint's own fallbackDiagnostic and
// parseCheckbashisms both stamp checkbashismsTool into Message, which is the
// only place the tool identity survives.
func diagFromCheckbashisms(d Diagnostic) bool {
	return strings.Contains(d.Message, checkbashismsTool)
}

// writeExitCodeScript writes an executable at dir/name that ignores its argv,
// prints stdoutBody, and exits with code — used to fake checkbashisms's
// bitwise-sum exit status without a parsed diagnostic line.
func writeExitCodeScript(t *testing.T, dir, name string, code int, stdoutBody string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-tool fixture is a POSIX shell script")
	}
	script := "#!/bin/sh\n" +
		"cat <<'EOF'\n" + stdoutBody + "\nEOF\n" +
		"exit " + itoa(code) + "\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func newShellLintTarget(t *testing.T) Target {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sh"), []byte("#!/bin/bash\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write a.sh: %v", err)
	}
	return Target{Language: LanguageShell, Check: CheckLint, Dir: dir}
}

// TestCheckbashismsExit4CleanBashIsNotAFallback: exit 4, no parsed diagnostic
// (checkbashisms's "no bashisms in a bash script" bit) must not synthesize a
// fallback diagnostic — this is the exact defect this task closes.
func TestCheckbashismsExit4CleanBashIsNotAFallback(t *testing.T) {
	fakeDir := t.TempDir()
	writeExitCodeScript(t, fakeDir, "shellcheck", 0, `{"comments":[]}`)
	writeExitCodeScript(t, fakeDir, "checkbashisms", 4, "")
	fakeToolPATH(t, fakeDir)

	target := newShellLintTarget(t)
	target.ConfigPath = writeMinimalShellcheckrc(t)
	diags, err := shellAdapter{}.runLint(context.Background(), target)
	if err != nil {
		t.Fatalf("runLint: %v", err)
	}
	for _, d := range diags {
		if diagFromCheckbashisms(d) {
			t.Fatalf("runLint produced a checkbashisms diagnostic on a clean exit-4 run: %+v (masking regressed)", d)
		}
	}
}

// TestCheckbashismsExit2UnreadableFileStillGates: bit 2 (unreadable file) is
// not masked and must still surface as a fallback when no line was parsed —
// proves the mask is scoped to bit 4 alone, not "any non-zero exit".
func TestCheckbashismsExit2UnreadableFileStillGates(t *testing.T) {
	fakeDir := t.TempDir()
	writeExitCodeScript(t, fakeDir, "shellcheck", 0, `{"comments":[]}`)
	writeExitCodeScript(t, fakeDir, "checkbashisms", 2, "")
	fakeToolPATH(t, fakeDir)

	target := newShellLintTarget(t)
	target.ConfigPath = writeMinimalShellcheckrc(t)
	diags, err := shellAdapter{}.runLint(context.Background(), target)
	if err != nil {
		t.Fatalf("runLint: %v", err)
	}
	found := false
	for _, d := range diags {
		if diagFromCheckbashisms(d) {
			found = true
		}
	}
	if !found {
		t.Fatalf("runLint dropped an unmasked exit-2 checkbashisms failure (should still gate): diags=%+v", diags)
	}
}

// TestCheckbashismsExit5CleanBashPlusBashismStillParses: bit 4 (clean bash)
// OR'd with bit 1 (bashism found, which always has a parsed line per
// checkbashisms(1)) must still report the parsed finding — masking bit 4
// never hides a real bashism riding the same run.
func TestCheckbashismsExit5CleanBashPlusBashismStillParses(t *testing.T) {
	fakeDir := t.TempDir()
	writeExitCodeScript(t, fakeDir, "shellcheck", 0, `{"comments":[]}`)
	writeExitCodeScript(t, fakeDir, "checkbashisms", 5,
		"possible bashism in a.sh line 1 (should be \"[ \\$(...) = ... ]\"):")
	fakeToolPATH(t, fakeDir)

	target := newShellLintTarget(t)
	target.ConfigPath = writeMinimalShellcheckrc(t)
	diags, err := shellAdapter{}.runLint(context.Background(), target)
	if err != nil {
		t.Fatalf("runLint: %v", err)
	}
	found := false
	for _, d := range diags {
		if diagFromCheckbashisms(d) {
			found = true
		}
	}
	if !found {
		t.Fatalf("runLint dropped a real bashism finding riding the same exit as the masked clean-bash bit: diags=%+v", diags)
	}
}

// writeMinimalShellcheckrc gives runLint an --rcfile so it never falls back
// to shellcheckConfigPath's language-tools embed lookup, which is irrelevant
// to the checkbashisms masking under test.
func writeMinimalShellcheckrc(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".shellcheckrc")
	if err := os.WriteFile(p, []byte("enable=all\n"), 0o644); err != nil {
		t.Fatalf("write shellcheckrc: %v", err)
	}
	return p
}
