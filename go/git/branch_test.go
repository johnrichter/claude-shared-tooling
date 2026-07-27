package git

import (
	"context"
	"testing"
)

// TestCreateBranch checks a branch is created pointing at startPoint.
func TestCreateBranch(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	if err := r.CreateBranch(ctx, "feature", head, BranchOptions{}); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if got := runGit(t, r.Dir, "rev-parse", "refs/heads/feature"); got != head {
		t.Fatalf("feature = %s, want %s", got, head)
	}
}

// TestCreateBranch_UnresolvableStartPointErrors checks CreateBranch refuses
// a start point that doesn't resolve, rather than letting git fail
// opaquely.
func TestCreateBranch_UnresolvableStartPointErrors(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	commitFile(t, r.Dir, "a.txt", "a\n", "first")

	if err := r.CreateBranch(ctx, "feature", "does-not-exist", BranchOptions{}); err == nil {
		t.Fatalf("CreateBranch with an unresolvable start point: want error, got nil")
	}
}

// TestDeleteBranch_RejectsStaleExpectedHead checks a compare-and-swap
// delete refuses when the branch moved since the caller last read it.
func TestDeleteBranch_RejectsStaleExpectedHead(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	first := commitFile(t, r.Dir, "a.txt", "a\n", "first")
	runGit(t, r.Dir, "branch", "feature", first)
	runGit(t, r.Dir, "checkout", "-q", "feature")
	second := commitFile(t, r.Dir, "b.txt", "b\n", "second")

	_, err := r.DeleteBranch(ctx, "feature", first, false)
	var stale *StaleRefError
	if err == nil {
		t.Fatalf("DeleteBranch with stale expected head: want StaleRefError, got nil")
	}
	if !asStaleRefError(err, &stale) {
		t.Fatalf("DeleteBranch error = %v, want *StaleRefError", err)
	}
	if got := runGit(t, r.Dir, "rev-parse", "refs/heads/feature"); got != second {
		t.Fatalf("feature = %s, want unchanged %s", got, second)
	}
}

// TestDeleteBranch_CASSucceedsWithBackupTag checks a correct
// compare-and-swap delete removes the branch and leaves a backup tag.
func TestDeleteBranch_CASSucceedsWithBackupTag(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")
	runGit(t, r.Dir, "branch", "feature", head)

	out, err := r.DeleteBranch(ctx, "feature", head, false)
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if _, err := r.resolveRef(ctx, "refs/heads/feature"); err == nil {
		t.Fatalf("feature ref still resolves after delete")
	}
	if resolved := runGit(t, r.Dir, "rev-parse", out.BackupTag); resolved != head {
		t.Fatalf("backup tag %s = %s, want %s", out.BackupTag, resolved, head)
	}
}
