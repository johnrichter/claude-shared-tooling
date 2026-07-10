package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/john-richter/ai/shared-tooling/go/manifest"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written to it. run() writes its diagnostics via fmt.Fprintln(os.Stderr, ...).
func captureStderr(t *testing.T, fn func() int) (exitCode int, stderr string) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	exitCode = fn()

	w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return exitCode, string(buf[:n])
}

func TestRun_ManifestSubcommand_PrintsExpectedDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\ndescription: \"D\"\n---\n")
	exitCode, _ := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "manifest", dir}) })
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
}

func TestRun_CheckSubcommand_ExitZeroOnMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\ndescription: \"D\"\n---\n")
	digest, err := manifest.Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	writeFile(t, filepath.Join(dir, "README.md"), "<!-- manifest: "+manifest.FormatDigest(digest)+" -->\n")

	exitCode, stderr := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "check", dir}) })
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr on success = %q, want empty", stderr)
	}
}

func TestRun_CheckSubcommand_ExitOneOnMismatch_StderrNamesFolderAndDigests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\ndescription: \"D\"\n---\n")
	wrongDigest := "00000000000000000000000000000000000000000000000000000000000000f0"
	writeFile(t, filepath.Join(dir, "README.md"), "<!-- manifest: sha256:"+wrongDigest+" -->\n")

	actual, err := manifest.Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	exitCode, stderr := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "check", dir}) })
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, dir) {
		t.Fatalf("stderr = %q, want it to name the folder %q", stderr, dir)
	}
	if !strings.Contains(stderr, wrongDigest) {
		t.Fatalf("stderr = %q, want it to contain the expected digest %q", stderr, wrongDigest)
	}
	if !strings.Contains(stderr, actual) {
		t.Fatalf("stderr = %q, want it to contain the actual digest %q", stderr, actual)
	}
}

func TestRun_CheckSubcommand_ExitOneOnMissingMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\n---\n")
	writeFile(t, filepath.Join(dir, "README.md"), "no marker line here at all\n")

	exitCode, stderr := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "check", dir}) })
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stderr == "" {
		t.Fatal("stderr empty, want a diagnostic about the missing marker")
	}
}

func TestRun_CheckSubcommand_ExitOneOnMalformedMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\n---\n")
	// Malformed: not 64 lowercase-hex chars (uppercase + short).
	writeFile(t, filepath.Join(dir, "README.md"), "<!-- manifest: sha256:DEADBEEF -->\n")

	exitCode, stderr := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "check", dir}) })
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stderr == "" {
		t.Fatal("stderr empty, want a diagnostic about the malformed marker")
	}
}

func TestRun_CheckSubcommand_ExitOneOnMissingReadme(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A\"\n---\n")
	// No README.md at all.

	exitCode, stderr := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "check", dir}) })
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stderr == "" {
		t.Fatal("stderr empty, want a diagnostic about the missing README")
	}
}

func TestRun_WrongArgCount_ExitTwo(t *testing.T) {
	cases := [][]string{
		{"jr-readme-manifest"},
		{"jr-readme-manifest", "manifest"},
		{"jr-readme-manifest", "manifest", "a", "b"},
	}
	for _, args := range cases {
		exitCode, _ := captureStderr(t, func() int { return run(args) })
		if exitCode != 2 {
			t.Errorf("run(%v) exit code = %d, want 2", args, exitCode)
		}
	}
}

func TestRun_UnknownSubcommand_ExitTwo(t *testing.T) {
	exitCode, _ := captureStderr(t, func() int { return run([]string{"jr-readme-manifest", "bogus", t.TempDir()}) })
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
}

func TestRun_NonexistentDir_ExitOne(t *testing.T) {
	exitCode, stderr := captureStderr(t, func() int {
		return run([]string{"jr-readme-manifest", "manifest", filepath.Join(t.TempDir(), "does-not-exist")})
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q", exitCode, stderr)
	}
}
