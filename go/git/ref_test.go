package git

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// backupRefPattern matches the exact fully-qualified form a backup ref must
// take: refs/backup/<base>/<ns>-<short>, entirely outside refs/tags/.
var backupRefPattern = regexp.MustCompile(`^refs/backup/[^/]+/\d+-[0-9a-f]+$`)

// TestMoveRef_RejectsStaleExpectedOld checks a compare-and-swap against an
// out-of-date expected value is refused, leaves the ref untouched, and
// creates no backup ref.
func TestMoveRef_RejectsStaleExpectedOld(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	first := commitFile(t, dir, "a.txt", "a\n", "first")
	current := commitFile(t, dir, "b.txt", "b\n", "second")

	_, err := r.MoveRef(ctx, "refs/heads/main", first, "deadbeef", SyncLocalOnly, "", false)
	var stale *StaleRefError
	if err == nil {
		t.Fatalf("MoveRef with stale expected-old: want StaleRefError, got nil")
	}
	if !asStaleRefError(err, &stale) {
		t.Fatalf("MoveRef error = %v, want *StaleRefError", err)
	}
	if stale.ExpectedOld != first || stale.ActualOld != current {
		t.Fatalf("StaleRefError = %+v, want ExpectedOld=%s ActualOld=%s", stale, first, current)
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != current {
		t.Fatalf("ref moved despite rejected CAS: main = %s, want unchanged %s", got, current)
	}
	if refs := runGit(t, dir, "for-each-ref", "refs/backup/"); refs != "" {
		t.Fatalf("backup ref created despite rejected CAS: %s", refs)
	}
}

// TestMoveRef_CASSucceedsAndCreatesBackupRef checks a correct
// compare-and-swap moves the ref and leaves a backup ref pointing at the
// prior value.
func TestMoveRef_CASSucceedsAndCreatesBackupRef(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")
	newHead := commitFile(t, dir, "b.txt", "b\n", "second") // an arbitrary distinct SHA to move the ref to
	runGit(t, dir, "update-ref", "refs/heads/main", oldHead)

	out, err := r.MoveRef(ctx, "refs/heads/main", oldHead, newHead, SyncLocalOnly, "", false)
	if err != nil {
		t.Fatalf("MoveRef: %v", err)
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != newHead {
		t.Fatalf("main = %s, want %s", got, newHead)
	}
	if resolved := runGit(t, dir, "rev-parse", out.BackupRef); resolved != oldHead {
		t.Fatalf("backup ref %s = %s, want %s", out.BackupRef, resolved, oldHead)
	}
	if out.PushCmd != nil {
		t.Fatalf("PushCmd = %v, want nil for SyncLocalOnly", out.PushCmd)
	}
}

// TestMoveRef_EmitForceWithLeaseNeverExecutesPush checks SyncEmitForceWithLease
// returns the push argv for the caller to run, and does not invoke git push
// itself (no remote is even configured in this scratch repo, so a real push
// attempt would fail loudly).
func TestMoveRef_EmitForceWithLeaseNeverExecutesPush(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")
	newHead := commitFile(t, dir, "b.txt", "b\n", "second")
	runGit(t, dir, "update-ref", "refs/heads/main", oldHead)

	out, err := r.MoveRef(ctx, "refs/heads/main", oldHead, newHead, SyncEmitForceWithLease, "upstream", false)
	if err != nil {
		t.Fatalf("MoveRef: %v", err)
	}
	want := []string{"git", "push", "--force-with-lease=refs/heads/main:" + oldHead, "upstream", "refs/heads/main"}
	if len(out.PushCmd) != len(want) {
		t.Fatalf("PushCmd = %v, want %v", out.PushCmd, want)
	}
	for i := range want {
		if out.PushCmd[i] != want[i] {
			t.Fatalf("PushCmd = %v, want %v", out.PushCmd, want)
		}
	}
}

// TestMoveRef_DryRunWritesNothing checks a dry run reports the plan without
// creating the backup ref or moving the ref.
func TestMoveRef_DryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")

	out, err := r.MoveRef(ctx, "refs/heads/main", oldHead, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", SyncLocalOnly, "", true)
	if err != nil {
		t.Fatalf("MoveRef dry-run: %v", err)
	}
	if !out.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != oldHead {
		t.Fatalf("ref moved during dry run: main = %s, want unchanged %s", got, oldHead)
	}
	if refs := runGit(t, dir, "for-each-ref", "refs/backup/"); refs != "" {
		t.Fatalf("backup ref created during dry run: %s", refs)
	}
}

// TestBackupRefName_WrittenViaUpdateRefInEveryWritingPath checks the backup
// ref written by MoveRef, DeleteBranch, and Rebase resolves to exactly
// refs/backup/<base>/<ns>-<short> and was written as a plain ref (via
// update-ref) rather than a tag object — the regression this guards is this
// package ever going back to `git tag`, which would (a) live under
// refs/tags/ instead of refs/backup/ and (b) require a real GPG signature
// for this disposable marker whenever tag.gpgSign is on.
func TestBackupRefName_WrittenViaUpdateRefInEveryWritingPath(t *testing.T) {
	ctx := context.Background()

	t.Run("MoveRef", func(t *testing.T) {
		r := newScratchRepo(t)
		dir := r.Dir
		oldHead := commitFile(t, dir, "a.txt", "a\n", "first")
		newHead := commitFile(t, dir, "b.txt", "b\n", "second")
		runGit(t, dir, "update-ref", "refs/heads/main", oldHead)

		out, err := r.MoveRef(ctx, "refs/heads/main", oldHead, newHead, SyncLocalOnly, "", false)
		if err != nil {
			t.Fatalf("MoveRef: %v", err)
		}
		assertPlainBackupRef(t, dir, out.BackupRef, oldHead)
	})

	t.Run("DeleteBranch", func(t *testing.T) {
		r := newScratchRepo(t)
		dir := r.Dir
		head := commitFile(t, dir, "a.txt", "a\n", "first")
		runGit(t, dir, "branch", "feature", head)

		out, err := r.DeleteBranch(ctx, "feature", head, false)
		if err != nil {
			t.Fatalf("DeleteBranch: %v", err)
		}
		assertPlainBackupRef(t, dir, out.BackupRef, head)
	})

	t.Run("Rebase", func(t *testing.T) {
		r := newScratchRepo(t)
		dir := r.Dir
		base := commitFile(t, dir, "base.txt", "base\n", "base")
		runGit(t, dir, "branch", "feature", base)
		runGit(t, dir, "checkout", "-q", "feature")
		oldFeatureHead := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")
		commitFile(t, dir, "main.txt", "main\n", "main advanced")
		runGit(t, dir, "checkout", "-q", "feature")

		res, err := r.Rebase(ctx, "main", RebaseOptions{})
		if err != nil {
			t.Fatalf("Rebase: %v", err)
		}
		assertPlainBackupRef(t, dir, res.BackupRef, oldFeatureHead)
	})
}

// TestBackupRefName_DryRunAndWriteAgreeOnFormat checks a dry run computes a
// backup ref name of the same shape MoveRef would actually write, so the
// two paths never disagree about what update-ref would do with it.
func TestBackupRefName_DryRunAndWriteAgreeOnFormat(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")

	dryOut, err := r.MoveRef(ctx, "refs/heads/main", oldHead, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", SyncLocalOnly, "", true)
	if err != nil {
		t.Fatalf("MoveRef dry-run: %v", err)
	}
	if !backupRefPattern.MatchString(dryOut.BackupRef) {
		t.Fatalf("dry-run backup ref %q, want form matching %s", dryOut.BackupRef, backupRefPattern)
	}
}

// TestBackupRefName_NeverUnderTagsNamespace is a regression test for this
// package's move away from `git tag`: backupRefName must never return a
// name under refs/tags/ — it must be a plain ref under refs/backup/, never a
// tag object.
func TestBackupRefName_NeverUnderTagsNamespace(t *testing.T) {
	name := backupRefName("refs/heads/main", "0123456789abcdef")
	if !strings.HasPrefix(name, "refs/backup/") {
		t.Fatalf("backupRefName(...) = %q, want a refs/backup/ prefix", name)
	}
	if strings.Contains(name, "refs/tags/") {
		t.Fatalf("backupRefName(...) = %q, must never carry a refs/tags/ segment", name)
	}
}

// TestMoveRef_BackupIsNeverATagObject checks the backup MoveRef writes is a
// plain ref: `git tag -l` (which only lists tag objects/refs under
// refs/tags/) must find nothing named after it, proving the write went
// through update-ref rather than `git tag`.
func TestMoveRef_BackupIsNeverATagObject(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")
	newHead := commitFile(t, dir, "b.txt", "b\n", "second")
	runGit(t, dir, "update-ref", "refs/heads/main", oldHead)

	out, err := r.MoveRef(ctx, "refs/heads/main", oldHead, newHead, SyncLocalOnly, "", false)
	if err != nil {
		t.Fatalf("MoveRef: %v", err)
	}
	if tags := runGit(t, dir, "tag", "-l"); tags != "" {
		t.Fatalf("MoveRef created a tag object (%s); backups must be plain refs, never tags", tags)
	}
	if resolved := runGit(t, dir, "rev-parse", out.BackupRef); resolved != oldHead {
		t.Fatalf("backup ref %s = %s, want %s", out.BackupRef, resolved, oldHead)
	}
}

// assertPlainBackupRef checks ref resolves under dir to wantSHA, has the
// expected refs/backup/<base>/<ns>-<short> shape, and was never registered
// as a tag.
func assertPlainBackupRef(t *testing.T, dir, ref, wantSHA string) {
	t.Helper()
	if !backupRefPattern.MatchString(ref) {
		t.Fatalf("backup ref %q, want form matching %s", ref, backupRefPattern)
	}
	if resolved := runGit(t, dir, "rev-parse", ref); resolved != wantSHA {
		t.Fatalf("backup ref %s = %s, want %s", ref, resolved, wantSHA)
	}
	if tags := runGit(t, dir, "tag", "-l"); tags != "" {
		t.Fatalf("backup ref %s has a tag object (%s); want a plain ref only", ref, tags)
	}
}

func asStaleRefError(err error, target **StaleRefError) bool {
	if e, ok := err.(*StaleRefError); ok {
		*target = e
		return true
	}
	return false
}
