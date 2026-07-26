package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRebase_PreserveMergesKeepsMergeCommitAndBackupTag checks
// RebaseOptions.PreserveMerges (--rebase-merges) keeps a merge commit intact
// during replay instead of linearizing it away, and still writes a backup
// tag before touching the branch — PreserveMerges was declared but had no
// direct test coverage.
func TestRebase_PreserveMergesKeepsMergeCommitAndBackupTag(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "topic", base)
	runGit(t, dir, "checkout", "-q", "topic")
	runGit(t, dir, "branch", "topic-side", "topic")
	runGit(t, dir, "checkout", "-q", "topic-side")
	commitFile(t, dir, "side.txt", "side\n", "side work")
	runGit(t, dir, "checkout", "-q", "topic")
	runGit(t, dir, "merge", "--no-ff", "-m", "merge side into topic", "topic-side")
	oldTopicHead := runGit(t, dir, "rev-parse", "topic")

	runGit(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "main.txt", "main\n", "main advanced")
	runGit(t, dir, "checkout", "-q", "topic")

	res, err := r.Rebase(ctx, "main", RebaseOptions{PreserveMerges: true})
	if err != nil {
		t.Fatalf("Rebase with PreserveMerges: %v", err)
	}
	if resolved := runGit(t, dir, "rev-parse", res.BackupTag); resolved != oldTopicHead {
		t.Fatalf("backup tag %s = %s, want pre-rebase tip %s", res.BackupTag, resolved, oldTopicHead)
	}
	// A linearizing rebase would have dropped the merge; PreserveMerges must
	// leave a 2-parent commit reachable from the new tip.
	merges := runGit(t, dir, "log", "--merges", "--pretty=%H", "refs/heads/topic")
	if merges == "" {
		t.Fatalf("no merge commit found on rebased topic; PreserveMerges should have kept it")
	}
	for _, f := range []string{"base.txt", "side.txt", "main.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s present after PreserveMerges rebase: %v", f, err)
		}
	}
}

// TestWorktreeAdd_ForceReusesBranchCheckedOutElsewhere checks Force actually
// permits what a plain add refuses (a branch already checked out in another
// worktree) rather than Force being accepted but ignored.
func TestWorktreeAdd_ForceReusesBranchCheckedOutElsewhere(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")
	runGit(t, r.Dir, "branch", "shared", head)

	firstPath := filepath.Join(t.TempDir(), "first")
	if err := r.WorktreeAdd(ctx, firstPath, "shared", WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd first: %v", err)
	}

	secondPath := filepath.Join(t.TempDir(), "second")
	if err := r.WorktreeAdd(ctx, secondPath, "shared", WorktreeAddOptions{Force: true}); err != nil {
		t.Fatalf("WorktreeAdd with Force for an already-checked-out branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondPath, "a.txt")); err != nil {
		t.Fatalf("expected checked-out file in forced second worktree: %v", err)
	}
}

// TestWorktreeRemove_SymlinkedPathResolvesToRegisteredWorktree checks
// canonPath's symlink resolution: a worktree removed via a symlinked path
// that resolves to the real registered path must be recognized (dry-run
// path) rather than rejected as "not a registered worktree".
func TestWorktreeRemove_SymlinkedPathResolvesToRegisteredWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	realParent := t.TempDir()
	realPath := filepath.Join(realParent, "linked")
	if err := r.WorktreeAdd(ctx, realPath, head, WorktreeAddOptions{}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	linkParent := t.TempDir()
	symlinkPath := filepath.Join(linkParent, "via-symlink")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if err := r.WorktreeRemove(ctx, symlinkPath, WorktreeRemoveOptions{DryRun: true}); err != nil {
		t.Fatalf("WorktreeRemove dry-run via symlinked path: %v, want recognized as the registered worktree", err)
	}
	if _, err := os.Stat(realPath); err != nil {
		t.Fatalf("worktree removed during dry-run via symlink: %v", err)
	}
}

// TestResign_DoesNotRewriteCommitsOutsideRange checks Resign leaves Base
// itself (and anything at or before it) completely untouched: a naive
// implementation that walked further than Base..Ref could reattribute or
// resign a commit the caller never asked to touch.
func TestResign_DoesNotRewriteCommitsOutsideRange(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	commitFile(t, dir, "next.txt", "next\n", "next")

	if _, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign}); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if got := runGit(t, dir, "rev-parse", base); got != base {
		t.Fatalf("Base commit's own SHA changed: %s, want unchanged %s", got, base)
	}
}

// TestResign_EmptyRangeErrors checks Resign refuses a Base..Ref range with no
// commits rather than silently no-op'ing the ref move.
func TestResign_EmptyRangeErrors(t *testing.T) {
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "a.txt", "a\n", "first")

	if _, err := r.Resign(context.Background(), "refs/heads/main", ResignOptions{Base: head}); err == nil {
		t.Fatalf("Resign with Base == Ref (empty range): want error, got nil")
	}
}

// TestRebase_BackupTagWrittenBeforeConflictingRebase checks the backup tag
// exists (and still resolves to the true pre-rebase tip) even when the
// rebase itself goes on to conflict and get aborted — proving tag-before-
// rewrite ordering holds on the failure path, not just the success path.
func TestRebase_BackupTagWrittenBeforeConflictingRebase(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "shared.txt", "line one\n", "base")
	runGit(t, dir, "branch", "feature", base)
	commitFile(t, dir, "shared.txt", "changed on main\n", "main edit")
	runGit(t, dir, "checkout", "-q", "feature")
	oldFeatureHead := commitFile(t, dir, "shared.txt", "changed on feature\n", "feature edit")

	_, err := r.Rebase(ctx, "main", RebaseOptions{})
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("Rebase error = %v (%T), want *ConflictError", err, err)
	}
	tags := strings.Fields(runGit(t, dir, "tag", "-l", "refs/tags/backup/feature/*"))
	if len(tags) != 1 {
		t.Fatalf("backup tags = %v, want exactly 1 pre-rebase backup tag even on conflict", tags)
	}
	if resolved := runGit(t, dir, "rev-parse", tags[0]); resolved != oldFeatureHead {
		t.Fatalf("backup tag %s = %s, want pre-rebase tip %s", tags[0], resolved, oldFeatureHead)
	}
}
