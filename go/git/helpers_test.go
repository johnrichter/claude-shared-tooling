package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newScratchRepo creates a throwaway git repository in a t.TempDir(),
// configured with a fixed test identity and signing disabled, so tests
// never depend on a real GPG/SSH signing key being available on the host.
func newScratchRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open scratch repo: %v", err)
	}
	return r
}

// runGit runs git args in dir, failing the test immediately on a non-zero
// exit.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes content to name inside dir, creating parent directories
// as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// commitFile writes content to name, stages, and commits it, returning the
// new commit's SHA.
func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	writeFile(t, dir, name, content)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// noSign disables commit-tree signing for tests: no scratch environment
// ships a usable signing key, and Resign's tree/parent-remapping logic is
// independent of signing.
var noSign = []string{}

// treeOf resolves sha's tree object hash.
func treeOf(t *testing.T, dir, sha string) string {
	t.Helper()
	return runGit(t, dir, "rev-parse", sha+"^{tree}")
}
