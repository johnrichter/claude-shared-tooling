package git

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// backupTagRefPattern matches the exact single-prefixed form a backup tag
// must resolve to: refs/tags/backup/<base>/<ns>-<short>.
var backupTagRefPattern = regexp.MustCompile(`^refs/tags/backup/[^/]+/\d+-[0-9a-f]+$`)

// TestMoveRef_RejectsStaleExpectedOld checks a compare-and-swap against an
// out-of-date expected value is refused, leaves the ref untouched, and
// creates no backup tag.
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
	if tags := runGit(t, dir, "tag", "-l", "backup/*"); tags != "" {
		t.Fatalf("backup tag created despite rejected CAS: %s", tags)
	}
}

// TestMoveRef_CASSucceedsAndCreatesBackupTag checks a correct
// compare-and-swap moves the ref and leaves a backup tag pointing at the
// prior value.
func TestMoveRef_CASSucceedsAndCreatesBackupTag(t *testing.T) {
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
	if resolved := runGit(t, dir, "rev-parse", out.BackupTag); resolved != oldHead {
		t.Fatalf("backup tag %s = %s, want %s", out.BackupTag, resolved, oldHead)
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
// creating the backup tag or moving the ref.
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
	if tags := runGit(t, dir, "tag", "-l", "backup/*"); tags != "" {
		t.Fatalf("backup tag created during dry run: %s", tags)
	}
}

// TestBackupTagName_SinglePrefixedInEveryWritingPath checks the backup tag
// written by MoveRef, DeleteBranch, and Rebase resolves to exactly
// refs/tags/backup/<base>/<ns>-<short> with no nesting under refs/tags/ —
// the failure mode this guards is backupTagName returning a name already
// prefixed with refs/tags/, which `git tag` then prefixes a second time.
func TestBackupTagName_SinglePrefixedInEveryWritingPath(t *testing.T) {
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
		assertSinglePrefixedBackupTag(t, dir, out.BackupTag, oldHead)
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
		assertSinglePrefixedBackupTag(t, dir, out.BackupTag, head)
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
		assertSinglePrefixedBackupTag(t, dir, res.BackupTag, oldFeatureHead)
	})
}

// TestBackupTagName_DryRunAndWriteAgreeOnFormat checks a dry run computes a
// backup tag name of the same single-prefixed shape MoveRef would actually
// write, so the two paths never disagree about what git tag would do with
// it.
func TestBackupTagName_DryRunAndWriteAgreeOnFormat(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir
	oldHead := commitFile(t, dir, "a.txt", "a\n", "first")

	dryOut, err := r.MoveRef(ctx, "refs/heads/main", oldHead, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", SyncLocalOnly, "", true)
	if err != nil {
		t.Fatalf("MoveRef dry-run: %v", err)
	}
	if strings.HasPrefix(dryOut.BackupTag, "refs/tags/") {
		t.Fatalf("dry-run backup tag %q is already refs/tags/-prefixed; git tag would nest it", dryOut.BackupTag)
	}
	if !backupTagRefPattern.MatchString("refs/tags/" + dryOut.BackupTag) {
		t.Fatalf("dry-run backup tag %q, want form matching %s once qualified", dryOut.BackupTag, backupTagRefPattern)
	}
}

// TestBackupTagName_NeverDoublePrefixed is a negative regression test for
// F45: backupTagName must never return a name that already starts with
// refs/tags/, since every caller passes it straight to `git tag <name>`,
// which prefixes refs/tags/ on its own.
func TestBackupTagName_NeverDoublePrefixed(t *testing.T) {
	name := backupTagName("refs/heads/main", "0123456789abcdef")
	if strings.HasPrefix(name, "refs/tags/") {
		t.Fatalf("backupTagName(...) = %q, must not carry a refs/tags/ prefix", name)
	}
}

// assertSinglePrefixedBackupTag checks tag resolves under dir to wantSHA and
// that its fully-qualified ref is exactly refs/tags/backup/<base>/<ns>-<short>
// — not nested a second time under refs/tags/.
func assertSinglePrefixedBackupTag(t *testing.T, dir, tag, wantSHA string) {
	t.Helper()
	if strings.HasPrefix(tag, "refs/tags/") {
		t.Fatalf("BackupTag %q is already refs/tags/-prefixed; git tag would nest it", tag)
	}
	qualified := "refs/tags/" + tag
	if !backupTagRefPattern.MatchString(qualified) {
		t.Fatalf("backup tag %q, want form matching %s", qualified, backupTagRefPattern)
	}
	if resolved := runGit(t, dir, "rev-parse", qualified); resolved != wantSHA {
		t.Fatalf("backup tag %s = %s, want %s", qualified, resolved, wantSHA)
	}
	nested := "refs/tags/" + qualified
	if _, err := (&Repo{Dir: dir}).resolveRef(context.Background(), nested); err == nil {
		t.Fatalf("nested backup ref %s unexpectedly exists", nested)
	}
}

func asStaleRefError(err error, target **StaleRefError) bool {
	if e, ok := err.(*StaleRefError); ok {
		*target = e
		return true
	}
	return false
}
