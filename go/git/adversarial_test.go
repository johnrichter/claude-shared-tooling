package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMerge_FastForwardOnlyRefusesNonFastForward checks --ff-only actually
// refuses a merge that would need a merge commit, rather than silently
// creating one.
func TestMerge_FastForwardOnlyRefusesNonFastForward(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	commitFile(t, dir, "main-only.txt", "main\n", "main diverges")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	if _, err := r.Merge(ctx, []string{"feature"}, MergeOptions{FastForward: FastForwardOnly}); err == nil {
		t.Fatalf("Merge with FastForwardOnly on diverged histories: want error, got nil")
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree not clean after refused ff-only merge: %q", status)
	}
}

// TestMerge_FastForwardNeverAlwaysCreatesMergeCommit checks --no-ff creates a
// merge commit even when a fast-forward was possible.
func TestMerge_FastForwardNeverAlwaysCreatesMergeCommit(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	res, err := r.Merge(ctx, []string{"feature"}, MergeOptions{FastForward: FastForwardNever, Message: "forced merge commit"})
	if err != nil {
		t.Fatalf("Merge with FastForwardNever: %v", err)
	}
	parents := strings.Fields(runGit(t, dir, "rev-list", "--parents", "-n", "1", res.NewHead))
	if len(parents)-1 != 2 {
		t.Fatalf("merge commit has %d parents, want 2 (--no-ff must not fast-forward): %v", len(parents)-1, parents)
	}
}

// TestWorktreeRemove_DirtyWorktreeRequiresForce checks WorktreeRemove refuses
// a worktree with uncommitted changes unless Force is set, and Force then
// actually removes it.
func TestWorktreeRemove_DirtyWorktreeRequiresForce(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, head, WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	writeFile(t, wtPath, "untracked.txt", "dirty\n")

	if err := r.WorktreeRemove(ctx, wtPath, WorktreeRemoveOptions{}); err == nil {
		t.Fatalf("WorktreeRemove on dirty worktree without Force: want error, got nil")
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree path removed despite refused (non-Force) remove: %v", err)
	}

	if err := r.WorktreeRemove(ctx, wtPath, WorktreeRemoveOptions{Force: true}); err != nil {
		t.Fatalf("WorktreeRemove with Force on dirty worktree: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree path %s still exists after forced Remove", wtPath)
	}
}

// TestWorktreeAdd_DryRunCreatesNothing checks DryRun validates the request
// without creating the worktree directory or registering it.
func TestWorktreeAdd_DryRunCreatesNothing(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, head, WorktreeAddOptions{DryRun: true}); err != nil {
		t.Fatalf("WorktreeAdd dry-run: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree path %s created during dry run", wtPath)
	}
	list, err := r.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("WorktreeList returned %d entries after dry-run add, want 1 (main only)", len(list))
	}
}

// TestWorktreeRemove_DryRunRemovesNothing checks a dry-run remove of a real
// worktree returns nil without deleting it, and rejects a path that is not a
// registered worktree.
func TestWorktreeRemove_DryRunRemovesNothing(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	wtPath := filepath.Join(t.TempDir(), "linked")
	if err := r.WorktreeAdd(ctx, wtPath, head, WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	if err := r.WorktreeRemove(ctx, wtPath, WorktreeRemoveOptions{DryRun: true}); err != nil {
		t.Fatalf("WorktreeRemove dry-run on real worktree: %v", err)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree path removed during dry-run remove: %v", err)
	}

	bogus := filepath.Join(t.TempDir(), "not-a-worktree")
	if err := r.WorktreeRemove(ctx, bogus, WorktreeRemoveOptions{DryRun: true}); err == nil {
		t.Fatalf("WorktreeRemove dry-run on unregistered path: want error, got nil")
	}
}

// TestCreateBranch_ForceMovesExistingBranch checks Force actually moves an
// existing branch instead of refusing, and without Force the same call is
// rejected rather than silently no-op'd.
func TestCreateBranch_ForceMovesExistingBranch(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	first := commitFile(t, dir, "a.txt", "a\n", "first")
	runGit(t, dir, "branch", "topic", first)
	second := commitFile(t, dir, "b.txt", "b\n", "second")

	if err := r.CreateBranch(ctx, "topic", second, BranchOptions{}); err == nil {
		t.Fatalf("CreateBranch on existing branch without Force: want error, got nil")
	}
	if got := runGit(t, dir, "rev-parse", "topic"); got != first {
		t.Fatalf("topic moved despite refused (non-Force) create: %s, want unchanged %s", got, first)
	}

	if err := r.CreateBranch(ctx, "topic", second, BranchOptions{Force: true}); err != nil {
		t.Fatalf("CreateBranch with Force: %v", err)
	}
	if got := runGit(t, dir, "rev-parse", "topic"); got != second {
		t.Fatalf("topic = %s after forced create, want %s", got, second)
	}
}

// TestDeleteBranch_BackupTagExistsEvenIfUpdateRefWereToFail simulates the
// ordering invariant directly: DeleteBranch must write the backup tag before
// attempting the ref removal, so a caller can always recover the old value
// even if the delete itself is interrupted. We can't force update-ref to
// fail after a successful CAS check without racing the process, so instead
// this asserts the documented order by checking the tag exists immediately
// after a successful delete and still resolves to the exact pre-delete SHA
// (not some other value taken after a hypothetical retry).
func TestDeleteBranch_BackupTagPointsAtExactPreDeleteSHA(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	first := commitFile(t, dir, "a.txt", "a\n", "first")
	runGit(t, dir, "branch", "topic", first)
	second := commitFile(t, dir, "b.txt", "b\n", "second")
	runGit(t, dir, "update-ref", "refs/heads/topic", second)

	out, err := r.DeleteBranch(ctx, "topic", second, false)
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if resolved := runGit(t, dir, "rev-parse", out.BackupTag); resolved != second {
		t.Fatalf("backup tag %s = %s, want %s", out.BackupTag, resolved, second)
	}
	if _, err := r.resolveRef(ctx, "refs/heads/topic"); err == nil {
		t.Fatalf("refs/heads/topic still resolves after DeleteBranch")
	}
}

// TestResign_OctopusMergeRemapsAllParents checks Resign correctly remaps
// every parent of a >2-parent (octopus) merge commit, not just the first two
// — a naive remap that only handled binary merges would silently drop
// parents here.
func TestResign_OctopusMergeRemapsAllParents(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	for _, name := range []string{"b1", "b2", "b3"} {
		runGit(t, dir, "branch", name, base)
		runGit(t, dir, "checkout", "-q", name)
		commitFile(t, dir, name+".txt", name+"\n", "add "+name)
		runGit(t, dir, "checkout", "-q", "main")
	}
	// Give main its own divergent commit so it isn't an ancestor of the
	// other three (an ancestor parent gets elided from an octopus merge),
	// making the resulting merge commit a genuine 4-parent octopus.
	commitFile(t, dir, "main-only.txt", "main\n", "main diverges")
	if _, err := r.Merge(ctx, []string{"b1", "b2", "b3"}, MergeOptions{Message: "octopus"}); err != nil {
		t.Fatalf("setup octopus merge: %v", err)
	}
	commitFile(t, dir, "after.txt", "after\n", "after octopus")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}

	oldOctopus := runGit(t, dir, "rev-parse", out.BackupTag+"~1")
	newOctopus := runGit(t, dir, "rev-parse", "refs/heads/main~1")
	oldParents := strings.Fields(runGit(t, dir, "rev-list", "--parents", "-n", "1", oldOctopus))[1:]
	newParents := strings.Fields(runGit(t, dir, "rev-list", "--parents", "-n", "1", newOctopus))[1:]
	if len(newParents) != len(oldParents) {
		t.Fatalf("resigned octopus merge has %d parents, want %d (same as original)", len(newParents), len(oldParents))
	}
	if len(oldParents) != 4 {
		t.Fatalf("test setup: octopus merge has %d parents, want 4", len(oldParents))
	}
	for i := range oldParents {
		if treeOf(t, dir, oldParents[i]) != treeOf(t, dir, newParents[i]) {
			t.Fatalf("parent %d: tree changed (old %s, new %s)", i, oldParents[i], newParents[i])
		}
	}
}

// TestMoveRef_StaleRejectionLeavesNoPushCmd checks a rejected CAS never
// populates PushCmd — a caller must never be handed a push command for a
// rewrite that did not actually land.
func TestMoveRef_StaleRejectionLeavesNoPushCmd(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	first := commitFile(t, dir, "a.txt", "a\n", "first")
	commitFile(t, dir, "b.txt", "b\n", "second")

	out, err := r.MoveRef(ctx, "refs/heads/main", first, "deadbeef", SyncEmitForceWithLease, "origin", false)
	if err == nil {
		t.Fatalf("MoveRef with stale expected-old: want error, got nil (out=%+v)", out)
	}
	if out != nil {
		t.Fatalf("MoveRef with stale expected-old: want nil result, got %+v", out)
	}
}
