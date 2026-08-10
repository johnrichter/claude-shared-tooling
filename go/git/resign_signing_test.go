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

// TestResign_AllSignedRangeIsNoOp checks that resigning a range whose commits
// already carry verifying signatures is a no-op: the ref does not move, no new
// SHAs are minted, no backup tag is created, and the result reports the no-op
// with a clean post-condition report.
func TestResign_AllSignedRangeIsNoOp(t *testing.T) {
	fingerprint, gnupgHome := genEphemeralGPGKey(t)
	t.Setenv("GNUPGHOME", gnupgHome)

	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	runGit(t, dir, "config", "gpg.format", "openpgp")
	runGit(t, dir, "config", "user.signingkey", fingerprint)
	runGit(t, dir, "config", "commit.gpgsign", "true")

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	commitFile(t, dir, "a.txt", "a\n", "a")
	oldHead := commitFile(t, dir, "b.txt", "b\n", "b")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if !out.NoOp {
		t.Fatalf("NoOp = false, want true for an already-signed range")
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != oldHead {
		t.Fatalf("ref moved on a no-op resign: main = %s, want unchanged %s", got, oldHead)
	}
	if out.NewHead != oldHead {
		t.Fatalf("NewHead = %s, want unchanged %s (a no-op mints no new SHA)", out.NewHead, oldHead)
	}
	if out.BackupTag != "" {
		t.Fatalf("backup tag %q created on a no-op resign, want none", out.BackupTag)
	}
	if out.Post == nil {
		t.Fatalf("no-op resign carried no post-condition report")
	}
	if out.Post.UnsignedCount != 0 || out.Post.BadSignatureCount != 0 {
		t.Fatalf("post report on all-signed range: unsigned=%d bad=%d, want 0/0",
			out.Post.UnsignedCount, out.Post.BadSignatureCount)
	}
	if !out.Post.BaseIsAncestor {
		t.Fatalf("Base no longer an ancestor of the tip after a no-op")
	}
}

// TestResign_MixedSignedRangeIsNotNoOp checks that a range with even one
// unsigned commit is NOT treated as a no-op: the no-op short-circuit requires
// EVERY commit in the range to already verify, so a single gap must still
// trigger a real rewrite (and the rewrite must leave every commit signed and
// every tree intact).
func TestResign_MixedSignedRangeIsNotNoOp(t *testing.T) {
	fingerprint, gnupgHome := genEphemeralGPGKey(t)
	t.Setenv("GNUPGHOME", gnupgHome)

	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	runGit(t, dir, "config", "gpg.format", "openpgp")
	runGit(t, dir, "config", "user.signingkey", fingerprint)

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	// Signed commit (gpgsign turned on for this one only)...
	runGit(t, dir, "config", "commit.gpgsign", "true")
	commitFile(t, dir, "a.txt", "a\n", "a")
	// ...followed by an UNSIGNED commit — the range is now mixed, not fully
	// signed.
	runGit(t, dir, "config", "commit.gpgsign", "false")
	oldHead := commitFile(t, dir, "b.txt", "b\n", "b")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if out.NoOp {
		t.Fatalf("NoOp = true, want false: range has an unsigned commit and must be rewritten")
	}
	if out.NewHead == oldHead {
		t.Fatalf("NewHead unchanged; a mixed range must be rewritten, not left alone")
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != out.NewHead {
		t.Fatalf("ref = %s, want moved to %s", got, out.NewHead)
	}
	if out.Post == nil {
		t.Fatalf("mixed-range resign carried no post-condition report")
	}
	if !out.Post.TreesPreserved {
		t.Fatalf("report TreesPreserved = false, want true")
	}
	if out.Post.UnsignedCount != 0 || out.Post.BadSignatureCount != 0 {
		t.Fatalf("post-resign report: unsigned=%d bad=%d, want 0/0 (default -S signs every rewritten commit)",
			out.Post.UnsignedCount, out.Post.BadSignatureCount)
	}
	runGit(t, dir, "verify-commit", out.NewHead)
}

// extractGpgsigBlock pulls the "gpgsig ...\n <cont>\n ..." header block
// (including its leading-space continuation lines) out of a raw `cat-file -p`
// commit object, as a single string with a trailing newline.
func extractGpgsigBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	var block []string
	inSig := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "gpgsig "):
			inSig = true
			block = append(block, line)
		case inSig && strings.HasPrefix(line, " "):
			block = append(block, line)
		case inSig:
			inSig = false
		}
	}
	if len(block) == 0 {
		return ""
	}
	return strings.Join(block, "\n") + "\n"
}

// fabricateBadSignature writes a raw commit object (via hash-object, bypassing
// commit-tree entirely) that carries a REAL PGP signature block signed over a
// DIFFERENT tree than this commit declares — the signature parses fine but
// fails to verify against this commit's content, which is exactly what git's
// sigBad classification (%G? not in {G,U,N}) exists to catch. A signature
// that merely fails to PARSE (garbage bytes) is reported by git as if there
// were no signature at all (%G?=N), so only a well-formed-but-mismatched
// signature exercises the sigBad branch.
func fabricateBadSignature(t *testing.T, dir, signedTree, mismatchedTree, parent string) string {
	t.Helper()
	signedHead := runGit(t, dir, "commit-tree", "-S", signedTree, "-m", "genuinely signed")
	raw := gitBytes(t, dir, "cat-file", "-p", signedHead)
	sigBlock := extractGpgsigBlock(string(raw))
	if sigBlock == "" {
		t.Fatalf("no gpgsig header found in reference commit %s:\n%s", signedHead, raw)
	}

	obj := "tree " + mismatchedTree + "\n"
	if parent != "" {
		obj += "parent " + parent + "\n"
	}
	obj += "author Test User <test@example.com> 1577836800 +0000\n" +
		"committer Test User <test@example.com> 1577836800 +0000\n" +
		sigBlock +
		"\n" +
		"forged: real signature, wrong tree\n"
	cmd := exec.Command("git", "hash-object", "-w", "-t", "commit", "--stdin")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(obj)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git hash-object (fabricate bad sig, in %s): %v\n%s", dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestResign_BadSignatureRangeIsNotNoOpAndGetsCounted checks two things about
// a commit whose signature exists but fails verification: (1) it does NOT
// qualify the range for the no-op short-circuit (only a verifying signature
// does), and (2) commitSigState/classifyGitSig correctly buckets it as bad,
// not unsigned — the distinction resignReport's BadSignatureCount depends on.
func TestResign_BadSignatureRangeIsNotNoOpAndGetsCounted(t *testing.T) {
	fingerprint, gnupgHome := genEphemeralGPGKey(t)
	t.Setenv("GNUPGHOME", gnupgHome)

	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	runGit(t, dir, "config", "gpg.format", "openpgp")
	runGit(t, dir, "config", "user.signingkey", fingerprint)

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	signedTree := treeOf(t, dir, base)
	writeFile(t, dir, "b.txt", "b\n")
	runGit(t, dir, "add", "-A")
	mismatchedTree := runGit(t, dir, "write-tree")

	badHead := fabricateBadSignature(t, dir, signedTree, mismatchedTree, base)
	runGit(t, dir, "update-ref", "refs/heads/main", badHead)

	// Precondition: git itself must not call this a verifying signature, or
	// the test proves nothing about the bad-signature path.
	code := runGit(t, dir, "log", "-1", "--format=%G?", badHead)
	if code == "G" || code == "U" || code == "N" || code == "" {
		t.Fatalf("precondition: fabricated commit's %%G? = %q, want a bad/unverifiable code", code)
	}
	if got := classifyGitSig(code); got != sigBad {
		t.Fatalf("classifyGitSig(%q) = %v, want sigBad", code, got)
	}

	state, err := r.commitSigState(ctx, badHead)
	if err != nil {
		t.Fatalf("commitSigState: %v", err)
	}
	if state != sigBad {
		t.Fatalf("commitSigState = %v, want sigBad", state)
	}

	// A bad signature must not be mistaken for a good one by the no-op gate:
	// rangeFullySigned must say false so Resign does not skip rewriting a
	// commit whose signature does not actually verify.
	commits, err := r.commitRange(ctx, base, "refs/heads/main")
	if err != nil {
		t.Fatalf("commitRange: %v", err)
	}
	allSigned, err := r.rangeFullySigned(ctx, commits)
	if err != nil {
		t.Fatalf("rangeFullySigned: %v", err)
	}
	if allSigned {
		t.Fatalf("rangeFullySigned = true over a bad-signature commit, want false")
	}

	// End to end: Resign must not no-op past a bad-signature commit, and the
	// commit it produces instead must carry a genuinely verifying signature.
	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base})
	if err != nil {
		t.Fatalf("Resign over a bad-signature range: %v", err)
	}
	if out.NoOp {
		t.Fatalf("NoOp = true over a bad-signature commit, want false")
	}
	runGit(t, dir, "verify-commit", out.NewHead)
}
