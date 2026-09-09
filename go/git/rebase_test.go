package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRebase_ReplaysCommitsCleanly checks a feature branch rebased onto an
// advanced main ends up with both histories' content and a fresh backup ref
// pointing at its pre-rebase tip.
func TestRebase_ReplaysCommitsCleanly(t *testing.T) {
	ctx := context.Background()
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
	if res.OldHead != oldFeatureHead {
		t.Fatalf("OldHead = %s, want %s", res.OldHead, oldFeatureHead)
	}
	if res.NewHead == "" || res.NewHead == oldFeatureHead {
		t.Fatalf("NewHead = %s, want a new commit distinct from the pre-rebase tip", res.NewHead)
	}
	if resolved := runGit(t, dir, "rev-parse", res.BackupRef); resolved != oldFeatureHead {
		t.Fatalf("backup ref %s = %s, want %s", res.BackupRef, resolved, oldFeatureHead)
	}
	for _, f := range []string{"base.txt", "main.txt", "feature.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s present after rebase: %v", f, err)
		}
	}
}

// TestRebase_ConflictAbortsCleanly checks a conflicting rebase returns
// ConflictError naming the conflicted file and leaves the branch at its
// original tip with no rebase in progress.
func TestRebase_ConflictAbortsCleanly(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "shared.txt", "line one\n", "base")
	runGit(t, dir, "branch", "feature", base)

	commitFile(t, dir, "shared.txt", "changed on main\n", "main edit")

	runGit(t, dir, "checkout", "-q", "feature")
	oldFeatureHead := commitFile(t, dir, "shared.txt", "changed on feature\n", "feature edit")

	_, err := r.Rebase(ctx, "main", RebaseOptions{})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("Rebase error = %v (%T), want *ConflictError", err, err)
	}
	if len(ce.Files) != 1 || ce.Files[0] != "shared.txt" {
		t.Fatalf("ConflictError.Files = %v, want [shared.txt]", ce.Files)
	}
	if ce.Op != "rebase" {
		t.Fatalf("ConflictError.Op = %q, want rebase", ce.Op)
	}

	if got := runGit(t, dir, "rev-parse", "refs/heads/feature"); got != oldFeatureHead {
		t.Fatalf("feature moved after aborted rebase: %s, want unchanged %s", got, oldFeatureHead)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree not clean after aborted rebase: %q", status)
	}
	if _, err := r.resolveRef(ctx, "REBASE_HEAD"); err == nil {
		t.Fatalf("REBASE_HEAD still set after aborted rebase")
	}
}

// TestRebase_DryRunListsCommitsWithoutRunning checks a dry run reports the
// commits that would be replayed and never moves the branch.
func TestRebase_DryRunListsCommitsWithoutRunning(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	oldFeatureHead := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "main.txt", "main\n", "main advanced")
	runGit(t, dir, "checkout", "-q", "feature")

	res, err := r.Rebase(ctx, "main", RebaseOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Rebase dry-run: %v", err)
	}
	if !res.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	if len(res.Commits) != 1 || res.Commits[0] != oldFeatureHead {
		t.Fatalf("Commits = %v, want [%s]", res.Commits, oldFeatureHead)
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/feature"); got != oldFeatureHead {
		t.Fatalf("feature moved during dry-run rebase: %s, want unchanged %s", got, oldFeatureHead)
	}
}

// TestRebase_DetachedHeadErrors checks Rebase refuses to run against a
// detached HEAD, since there is no branch ref for it to land the result on.
func TestRebase_DetachedHeadErrors(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	head := commitFile(t, r.Dir, "base.txt", "base\n", "base")
	runGit(t, r.Dir, "checkout", "-q", "--detach", head)

	if _, err := r.Rebase(ctx, "main", RebaseOptions{}); err == nil {
		t.Fatalf("Rebase on detached HEAD: want error, got nil")
	}
}
