package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestWorktreeAddAndRemove checks the basic lifecycle: adding a linked
// worktree makes the checkout appear on disk and in the worktree list;
// removing it cleans both up.
func TestWorktreeAddAndRemove(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	// Check out the commit itself (detached), not the "main" branch name —
	// main is already checked out in the primary worktree, and git refuses
	// to check out the same branch in two worktrees at once.
	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, head, WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "a.txt")); err != nil {
		t.Fatalf("expected checked-out file in new worktree: %v", err)
	}

	list, err := r.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("WorktreeList returned %d entries, want 2 (main + linked)", len(list))
	}

	if err := r.WorktreeRemove(ctx, wtPath, WorktreeRemoveOptions{}); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree path %s still exists after Remove", wtPath)
	}
	list, err = r.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("WorktreeList after remove: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("WorktreeList returned %d entries after remove, want 1 (main only)", len(list))
	}
}

// TestWorktreeAdd_NewBranch checks -b creates and checks out a new branch
// in the linked worktree.
func TestWorktreeAdd_NewBranch(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	commitFile(t, r.Dir, "a.txt", "a\n", "first")

	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, "main", WorktreeAddOptions{NewBranch: "feature"}); err != nil {
		t.Fatalf("WorktreeAdd with NewBranch: %v", err)
	}
	if got := runGit(t, wtPath, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("checked-out branch = %s, want feature", got)
	}
}

// TestWorktreeRemove_NonexistentPathErrors checks removing a path git never
// tracked as a worktree surfaces an error rather than succeeding silently.
func TestWorktreeRemove_NonexistentPathErrors(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	commitFile(t, r.Dir, "a.txt", "a\n", "first")

	if err := r.WorktreeRemove(ctx, filepath.Join(t.TempDir(), "never-added"), WorktreeRemoveOptions{}); err == nil {
		t.Fatalf("WorktreeRemove on an untracked path: want error, got nil")
	}
}

// TestWorktreeAdd_DuplicateBranchWithoutForceErrors checks git's own
// same-branch-checked-out-twice guard surfaces as an error, not a silent
// no-op.
func TestWorktreeAdd_DuplicateBranchWithoutForceErrors(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")
	runGit(t, r.Dir, "branch", "shared", head)

	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, "shared", WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	// "shared" is now checked out in wtPath; a second worktree for the same
	// branch is refused without --force.
	secondPath := filepath.Join(t.TempDir(), "linked2")
	if err := r.WorktreeAdd(ctx, secondPath, "shared", WorktreeAddOptions{}); err == nil {
		t.Fatalf("WorktreeAdd for an already-checked-out branch without Force: want error, got nil")
	}
}
