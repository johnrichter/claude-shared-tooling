package git

import (
	"context"
	"testing"
)

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

func asStaleRefError(err error, target **StaleRefError) bool {
	if e, ok := err.(*StaleRefError); ok {
		*target = e
		return true
	}
	return false
}
