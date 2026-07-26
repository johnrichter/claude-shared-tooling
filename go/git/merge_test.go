package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMerge_Octopus checks three branches merge into one commit with all
// four parents (the checked-out branch plus the three merged branches) and
// every branch's file present afterward.
func TestMerge_Octopus(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	for _, name := range []string{"b1", "b2", "b3"} {
		runGit(t, dir, "branch", name, base)
		runGit(t, dir, "checkout", "-q", name)
		commitFile(t, dir, name+".txt", name+"\n", "add "+name)
	}
	// Give main its own commit that b1/b2/b3 don't share, so main's tip is
	// a genuinely distinct 4th parent rather than a common ancestor git's
	// octopus merge would otherwise fold away as redundant.
	runGit(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "main-only.txt", "main\n", "main-only work")

	res, err := r.Merge(ctx, []string{"b1", "b2", "b3"}, MergeOptions{Message: "octopus merge"})
	if err != nil {
		t.Fatalf("Merge (octopus): %v", err)
	}
	if res.NewHead == "" {
		t.Fatalf("MergeResult.NewHead is empty")
	}
	for _, f := range []string{"base.txt", "main-only.txt", "b1.txt", "b2.txt", "b3.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s present after octopus merge: %v", f, err)
		}
	}
	parents := strings.Fields(runGit(t, dir, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents)-1 != 4 {
		t.Fatalf("merge commit has %d parents, want 4 (main + b1 + b2 + b3): %v", len(parents)-1, parents)
	}
}

// TestMerge_ConflictAbortsCleanly checks a conflicting merge returns
// ConflictError naming the conflicted file and leaves the repository in the
// exact state it started in.
func TestMerge_ConflictAbortsCleanly(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "shared.txt", "line one\n", "base")
	runGit(t, dir, "branch", "other", base)

	mainHead := commitFile(t, dir, "shared.txt", "line one, changed on main\n", "main edit")

	runGit(t, dir, "checkout", "-q", "other")
	commitFile(t, dir, "shared.txt", "line one, changed on other\n", "other edit")
	runGit(t, dir, "checkout", "-q", "main")

	_, err := r.Merge(ctx, []string{"other"}, MergeOptions{Message: "will conflict"})
	var conflict *ConflictError
	if err == nil {
		t.Fatalf("Merge with conflicting edits: want ConflictError, got nil")
	}
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("Merge error = %v (%T), want *ConflictError", err, err)
	}
	conflict = ce
	if len(conflict.Files) != 1 || conflict.Files[0] != "shared.txt" {
		t.Fatalf("ConflictError.Files = %v, want [shared.txt]", conflict.Files)
	}
	if conflict.Op != "merge" {
		t.Fatalf("ConflictError.Op = %q, want merge", conflict.Op)
	}

	if got := runGit(t, dir, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("HEAD moved after aborted merge: %s, want unchanged %s", got, mainHead)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree not clean after aborted merge: %q", status)
	}
	if _, err := r.resolveRef(ctx, "MERGE_HEAD"); err == nil {
		t.Fatalf("MERGE_HEAD still set after aborted merge")
	}
}

// TestMerge_DryRunLeavesTreeUnchanged checks a dry run of a cleanly
// mergeable branch reports success without moving HEAD or leaving
// MERGE_HEAD behind.
func TestMerge_DryRunLeavesTreeUnchanged(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	mainHead := commitFile(t, dir, "more.txt", "more\n", "more on main")

	res, err := r.Merge(ctx, []string{"feature"}, MergeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Merge dry-run: %v", err)
	}
	if !res.WouldMerge {
		t.Fatalf("WouldMerge = false, want true for a cleanly mergeable branch")
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("HEAD moved during dry-run merge: %s, want unchanged %s", got, mainHead)
	}
	if _, err := r.resolveRef(ctx, "MERGE_HEAD"); err == nil {
		t.Fatalf("MERGE_HEAD left set after dry-run merge")
	}
}

// TestMerge_DryRunReportsConflictWithoutLeavingState checks a dry run of a
// conflicting merge reports the conflict via WouldMerge (no error) and
// still leaves the repository clean.
func TestMerge_DryRunReportsConflictWithoutLeavingState(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "shared.txt", "line one\n", "base")
	runGit(t, dir, "branch", "other", base)
	mainHead := commitFile(t, dir, "shared.txt", "changed on main\n", "main edit")
	runGit(t, dir, "checkout", "-q", "other")
	commitFile(t, dir, "shared.txt", "changed on other\n", "other edit")
	runGit(t, dir, "checkout", "-q", "main")

	res, err := r.Merge(ctx, []string{"other"}, MergeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Merge dry-run on conflicting branches: %v, want nil (conflict reported via WouldMerge)", err)
	}
	if res.WouldMerge {
		t.Fatalf("WouldMerge = true, want false for conflicting edits")
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("HEAD moved during dry-run merge: %s, want unchanged %s", got, mainHead)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree not clean after dry-run conflict: %q", status)
	}
}

// TestMerge_DryRunFastForwardableLeavesHeadUnmoved is the regression guard for
// a dry run advancing HEAD: when the branch is a pure fast-forward, --no-commit
// alone still moves HEAD, so a dry run must force a real (abortable) merge. HEAD
// must stay put and no MERGE_HEAD may leak.
func TestMerge_DryRunFastForwardableLeavesHeadUnmoved(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	// main stays at base, so feature is a clean fast-forward of main.

	res, err := r.Merge(ctx, []string{"feature"}, MergeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Merge dry-run (ff-able): %v", err)
	}
	if !res.WouldMerge {
		t.Fatalf("WouldMerge = false, want true for a fast-forwardable branch")
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != base {
		t.Fatalf("HEAD advanced during ff-able dry-run merge: %s, want unchanged %s", got, base)
	}
	if _, err := r.resolveRef(ctx, "MERGE_HEAD"); err == nil {
		t.Fatalf("MERGE_HEAD left set after ff-able dry-run merge")
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree not clean after ff-able dry-run merge: %q", status)
	}
}

// TestMerge_NoBranchesErrors checks Merge refuses an empty branch list
// rather than running a no-op git merge.
func TestMerge_NoBranchesErrors(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	commitFile(t, r.Dir, "base.txt", "base\n", "base")

	if _, err := r.Merge(ctx, nil, MergeOptions{}); err == nil {
		t.Fatalf("Merge with no branches: want error, got nil")
	}
}
