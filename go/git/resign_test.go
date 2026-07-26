package git

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestResign_PreservesTreesOverMergeHistory checks every commit in the
// rewritten range keeps its exact original tree, including a merge commit
// pulled in from a merged-in branch — the property that makes Resign safe
// where a rebase (which replays diffs) is not.
func TestResign_PreservesTreesOverMergeHistory(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "merge", "--no-ff", "-m", "merge feature", "feature")
	oldHead := commitFile(t, dir, "after-merge.txt", "after\n", "after merge")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	// With signing skipped (no key in this environment), commit-tree
	// deterministically reproduces the SAME object hash when every input —
	// tree, parents, identities, message — is unchanged: SHA equality here
	// is itself evidence the reconstruction is exact. A real signing key
	// would change the object bytes (and so the hash) without changing the
	// tree, which the chain comparison below verifies directly.
	if treeOf(t, dir, out.NewHead) != treeOf(t, dir, oldHead) {
		t.Fatalf("resigned head's tree differs from the original head's tree")
	}
	if resolved, err := r.resolveRef(ctx, out.BackupTag); err != nil || resolved != oldHead {
		t.Fatalf("backup tag %s = %q, %v; want %q, nil", out.BackupTag, resolved, err, oldHead)
	}

	// Walk the original chain (still reachable via the backup tag Resign
	// created) against the resigned chain and compare trees pairwise.
	oldChain := strings.Split(runGit(t, dir, "rev-list", "--reverse", "--topo-order", base+".."+out.BackupTag), "\n")
	newChain := strings.Split(runGit(t, dir, "rev-list", "--reverse", "--topo-order", base+"..refs/heads/main"), "\n")
	if len(oldChain) != len(newChain) {
		t.Fatalf("resigned chain has %d commits, original had %d", len(newChain), len(oldChain))
	}
	for i := range oldChain {
		if treeOf(t, dir, oldChain[i]) != treeOf(t, dir, newChain[i]) {
			t.Fatalf("commit %d: tree changed across resign (old %s, new %s)", i, oldChain[i], newChain[i])
		}
	}
}

// TestResign_ConflictProofOverMergeHistory proves the practical consequence
// of tree preservation: a branch that already merged the ORIGINAL
// (unsigned) chain merges cleanly with the RESIGNED chain too, because the
// resigned commits' trees are byte-identical to the ones already merged.
func TestResign_ConflictProofOverMergeHistory(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	runGit(t, dir, "branch", "feature", base)
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "merge", "--no-ff", "-m", "merge feature", "feature")
	preResignTip := commitFile(t, dir, "after-merge.txt", "after\n", "after merge")

	// downstream already built on top of the ORIGINAL, unsigned chain —
	// simulating a consumer who pulled before the resign.
	runGit(t, dir, "branch", "downstream", preResignTip)
	runGit(t, dir, "checkout", "-q", "downstream")
	commitFile(t, dir, "downstream.txt", "downstream\n", "downstream work")

	if _, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign}); err != nil {
		t.Fatalf("Resign: %v", err)
	}

	runGit(t, dir, "checkout", "-q", "main")
	res, err := r.Merge(ctx, []string{"downstream"}, MergeOptions{Message: "merge downstream into resigned main"})
	if err != nil {
		t.Fatalf("Merge resigned main with downstream: %v, want a clean merge", err)
	}
	if res.NewHead == "" {
		t.Fatalf("Merge returned no NewHead")
	}
	for _, f := range []string{"base.txt", "feature.txt", "after-merge.txt", "downstream.txt"} {
		if _, err := os.Stat(dir + "/" + f); err != nil {
			t.Fatalf("expected file %s present after merge: %v", f, err)
		}
	}
}

// TestResign_DryRunMovesNothing checks a dry run neither moves the ref nor
// creates the backup tag it computes the name for.
func TestResign_DryRunMovesNothing(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	oldHead := commitFile(t, dir, "next.txt", "next\n", "next")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign, DryRun: true})
	if err != nil {
		t.Fatalf("Resign dry-run: %v", err)
	}
	if !out.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	if got := runGit(t, dir, "rev-parse", "refs/heads/main"); got != oldHead {
		t.Fatalf("ref moved during dry run: main = %s, want unchanged %s", got, oldHead)
	}
	if _, err := r.resolveRef(ctx, out.BackupTag); err == nil {
		t.Fatalf("backup tag %s exists after a dry run, want none created", out.BackupTag)
	}
}

// TestResign_RequiresBase checks Resign refuses to infer a rewrite range.
func TestResign_RequiresBase(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	commitFile(t, r.Dir, "base.txt", "base\n", "base")

	if _, err := r.Resign(ctx, "refs/heads/main", ResignOptions{}); err == nil {
		t.Fatalf("Resign with no Base: want error, got nil")
	}
}
