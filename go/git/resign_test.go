package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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
	if resolved, err := r.resolveRef(ctx, out.BackupRef); err != nil || resolved != oldHead {
		t.Fatalf("backup ref %s = %q, %v; want %q, nil", out.BackupRef, resolved, err, oldHead)
	}

	// Walk the original chain (still reachable via the backup ref Resign
	// created) against the resigned chain and compare trees pairwise.
	oldChain := strings.Split(runGit(t, dir, "rev-list", "--reverse", "--topo-order", base+".."+out.BackupRef), "\n")
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
// creates the backup ref it computes the name for.
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
	if _, err := r.resolveRef(ctx, out.BackupRef); err == nil {
		t.Fatalf("backup ref %s exists after a dry run, want none created", out.BackupRef)
	}
}

// gitBytes runs git and returns its stdout untrimmed, for assertions that turn
// on exact bytes (a trailing carriage return, a missing final newline).
func gitBytes(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
	return out
}

// rawCommit stages file and forges a commit with an exact message via
// commit-tree — bypassing `git commit`'s message cleanup so the stored message
// keeps whatever unusual bytes (trailing \r, no final newline) the test needs.
func rawCommit(t *testing.T, dir, file, content, message, parent string) string {
	t.Helper()
	writeFile(t, dir, file, content)
	runGit(t, dir, "add", "-A")
	tree := runGit(t, dir, "write-tree")
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit-tree (in %s): %v\n%s", dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitMessageBytes returns sha's commit message exactly as stored — the bytes
// after the header block's terminating blank line.
func commitMessageBytes(t *testing.T, dir, sha string) []byte {
	t.Helper()
	raw := gitBytes(t, dir, "cat-file", "commit", sha)
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[i+2:]
	}
	return nil
}

// TestResign_PreservesExactMessageBytes checks Resign reproduces a commit's
// message byte-for-byte, including a trailing carriage return and a missing
// final newline — cases a line-oriented reassembly silently corrupts.
func TestResign_PreservesExactMessageBytes(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{"trailing carriage return", "subject\r\n\r\nbody ending in a carriage return\r"},
		{"no trailing newline", "subject with no trailing newline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := newScratchRepo(t)
			dir := r.Dir

			base := commitFile(t, dir, "base.txt", "base\n", "base")
			oldHead := rawCommit(t, dir, "payload.txt", "payload\n", tc.message, base)
			runGit(t, dir, "update-ref", "refs/heads/main", oldHead)

			oldMsg := commitMessageBytes(t, dir, oldHead)
			if string(oldMsg) != tc.message {
				t.Fatalf("precondition: original message not stored verbatim: got %q, want %q", oldMsg, tc.message)
			}

			out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign})
			if err != nil {
				t.Fatalf("Resign: %v", err)
			}
			if newMsg := commitMessageBytes(t, dir, out.NewHead); !bytes.Equal(oldMsg, newMsg) {
				t.Fatalf("message bytes changed across resign:\n old %q\n new %q", oldMsg, newMsg)
			}
		})
	}
}

// TestResign_PopulatesPostConditionReport checks Resign returns a filled-in
// post-condition report over a known range: every tree preserved, an accurate
// unsigned count (all, since this range is resigned without a key), no bad
// signatures, and Base still an ancestor of the new tip.
func TestResign_PopulatesPostConditionReport(t *testing.T) {
	ctx := context.Background()
	r := newScratchRepo(t)
	dir := r.Dir

	base := commitFile(t, dir, "base.txt", "base\n", "base")
	commitFile(t, dir, "a.txt", "a\n", "a")
	commitFile(t, dir, "b.txt", "b\n", "b")

	out, err := r.Resign(ctx, "refs/heads/main", ResignOptions{Base: base, SignArgs: noSign})
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	rep := out.Post
	if rep == nil {
		t.Fatalf("Resign carried no post-condition report")
	}
	if rep.Commits != 2 {
		t.Fatalf("report Commits = %d, want 2", rep.Commits)
	}
	if !rep.TreesPreserved {
		t.Fatalf("report TreesPreserved = false, want true")
	}
	if rep.UnsignedCount != 2 {
		t.Fatalf("report UnsignedCount = %d, want 2 (resigned without a key)", rep.UnsignedCount)
	}
	if rep.BadSignatureCount != 0 {
		t.Fatalf("report BadSignatureCount = %d, want 0", rep.BadSignatureCount)
	}
	if !rep.BaseIsAncestor {
		t.Fatalf("report BaseIsAncestor = false; Base must stay an ancestor of the new tip")
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
