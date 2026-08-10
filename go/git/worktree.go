package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeAddOptions configures WorktreeAdd.
type WorktreeAddOptions struct {
	// NewBranch, if set, creates it at ref and checks it out in the new
	// worktree (git worktree add -b).
	NewBranch string
	// Force allows reusing a branch already checked out elsewhere, or
	// overwriting a path git would otherwise refuse.
	Force bool
	// DryRun validates the request (ref resolvable, target path free, and any
	// NewBranch not already taken) without creating anything. git worktree add
	// has no native dry-run, so this is a best-effort preflight: a clean dry
	// run does not prove the real add cannot fail for a reason not checked.
	DryRun bool
}

// WorktreeAdd creates a linked worktree at path checked out to ref.
func (r *Repo) WorktreeAdd(ctx context.Context, path, ref string, opts WorktreeAddOptions) error {
	if opts.DryRun {
		if _, err := r.resolveRef(ctx, ref); err != nil {
			return fmt.Errorf("git: worktree add %s at %s (dry-run): %w", path, ref, err)
		}
		statPath := path
		if !filepath.IsAbs(statPath) {
			statPath = filepath.Join(r.Dir, statPath)
		}
		if _, err := os.Stat(statPath); err == nil {
			return fmt.Errorf("git: worktree add (dry-run): path %s already exists", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("git: worktree add (dry-run): stat %s: %w", path, err)
		}
		if opts.NewBranch != "" && !opts.Force {
			if _, err := r.resolveRef(ctx, "refs/heads/"+opts.NewBranch); err == nil {
				return fmt.Errorf("git: worktree add (dry-run): branch %s already exists", opts.NewBranch)
			}
		}
		return nil
	}
	args := []string{"worktree", "add"}
	if opts.Force {
		args = append(args, "--force")
	}
	if opts.NewBranch != "" {
		args = append(args, "-b", opts.NewBranch)
	}
	args = append(args, path, ref)
	if _, err := r.git(ctx, args...); err != nil {
		return fmt.Errorf("git: worktree add %s at %s: %w", path, ref, err)
	}
	return nil
}

// WorktreeRemoveOptions configures WorktreeRemove.
type WorktreeRemoveOptions struct {
	// Force removes a worktree even with uncommitted changes or untracked
	// files.
	Force bool
	// DryRun confirms path is a registered worktree without removing it. git
	// worktree remove has no native dry-run, so this is a best-effort
	// preflight and does not check for the uncommitted changes a non-Force
	// remove would still refuse.
	DryRun bool
}

// WorktreeRemove deletes the linked worktree at path.
func (r *Repo) WorktreeRemove(ctx context.Context, path string, opts WorktreeRemoveOptions) error {
	if opts.DryRun {
		list, err := r.WorktreeList(ctx)
		if err != nil {
			return err
		}
		removePath := path
		if !filepath.IsAbs(removePath) {
			removePath = filepath.Join(r.Dir, removePath)
		}
		target := canonPath(removePath)
		for _, wt := range list {
			if canonPath(wt.Path) == target {
				return nil
			}
		}
		return fmt.Errorf("git: worktree remove (dry-run): %s is not a registered worktree", path)
	}
	args := []string{"worktree", "remove"}
	if opts.Force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := r.git(ctx, args...); err != nil {
		return fmt.Errorf("git: worktree remove %s: %w", path, err)
	}
	return nil
}

// WorktreeInfo is one entry from `git worktree list`.
type WorktreeInfo struct {
	Path   string
	Head   string
	Branch string // empty when detached
}

// WorktreeList reports every worktree linked to this repository, including
// the main one.
func (r *Repo) WorktreeList(ctx context.Context) ([]WorktreeInfo, error) {
	out, err := r.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git: worktree list: %w", err)
	}
	return parseWorktreeList(out), nil
}

// canonPath resolves symlinks so a caller-supplied worktree path matches the
// real path `git worktree list` reports, falling back to the input when the
// path cannot be resolved.
func canonPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// parseWorktreeList reads `git worktree list --porcelain`'s block format:
// one or more "key value" lines per worktree, blocks separated by a blank
// line.
func parseWorktreeList(out string) []WorktreeInfo {
	var list []WorktreeInfo
	cur := WorktreeInfo{}
	flush := func() {
		if cur.Path != "" {
			list = append(list, cur)
		}
		cur = WorktreeInfo{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(line, "branch ")
		}
	}
	flush()
	return list
}
