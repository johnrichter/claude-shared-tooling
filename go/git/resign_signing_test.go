package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genEphemeralGPGKey creates a throwaway, no-passphrase GPG key in an
// isolated GNUPGHOME so signing tests never touch the host's real keyring.
// It skips the test if gpg isn't installed.
func genEphemeralGPGKey(t *testing.T) (fingerprint, gnupgHome string) {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg not installed; skipping real-signature resign test")
	}
	gnupgHome = filepath.Join(t.TempDir(), "gnupg")
	if err := os.MkdirAll(gnupgHome, 0o700); err != nil {
		t.Fatalf("mkdir GNUPGHOME: %v", err)
	}
	batch := filepath.Join(t.TempDir(), "keygen.batch")
	script := "%no-protection\n" +
		"Key-Type: eddsa\nKey-Curve: ed25519\n" +
		"Subkey-Type: eddsa\nSubkey-Curve: ed25519\n" +
		"Name-Real: Resign Test\nName-Email: resign-test@example.com\n" +
		"Expire-Date: 0\n%commit\n"
	if err := os.WriteFile(batch, []byte(script), 0o600); err != nil {
		t.Fatalf("write keygen batch: %v", err)
	}
	cmd := exec.Command("gpg", "--batch", "--gen-key", batch)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+gnupgHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gpg --gen-key: %v\n%s", err, out)
	}
	cmd = exec.Command("gpg", "--with-colons", "--list-secret-keys")
	cmd.Env = append(os.Environ(), "GNUPGHOME="+gnupgHome)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpg --list-secret-keys: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			fields := strings.Split(line, ":")
			if len(fields) > 9 {
				return fields[9], gnupgHome
			}
		}
	}
	t.Fatalf("no fingerprint found in gpg output:\n%s", out)
	return "", ""
}

// TestResign_WithRealKeyChangesHashButNotTree exercises Resign's default
// signing path (SignArgs left nil, so commit-tree gets -S) against a real
// ephemeral key: the resigned commit is a genuinely different object (the
// embedded signature changes its bytes) but its tree is byte-identical to
// the original, and the signature verifies.
func TestResign_WithRealKeyChangesHashButNotTree(t *testing.T) {
	fingerprint, gnupgHome := genEphemeralGPGKey(t)
	t.Setenv("GNUPGHOME", gnupgHome)

	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	// The host's own git config may default gpg.format to "ssh" (a common
	// modern default); pin this scratch repo to openpgp so
	// user.signingkey is read as a GPG fingerprint, not an SSH key path.
	runGit(t, dir, "config", "gpg.format", "openpgp")
	runGit(t, dir, "config", "user.signingkey", fingerprint)

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	oldHead := commitFile(t, dir, "next.txt", "next\n", "next")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if out.NewHead == oldHead {
		t.Fatalf("NewHead == OldHead, a real signature should have changed the object")
	}
	if treeOf(t, dir, out.NewHead) != treeOf(t, dir, oldHead) {
		t.Fatalf("resigned commit's tree differs from the original")
	}
	runGit(t, dir, "verify-commit", out.NewHead)
}
