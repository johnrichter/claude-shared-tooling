package git

import (
	"context"
	"fmt"
	"strings"
)

// RebaseOptions configures Rebase.
type RebaseOptions struct {
	// Onto replays commits onto a different base than upstream
	// (git rebase --onto). Empty means the plain two-argument form.
	Onto string
	// PreserveMerges keeps merge commits instead of linearizing history
	// (git rebase --rebase-merges). A merge commit replayed this way still
	// gets a NEW tree from re-running the merge — unlike Resign, which
	// never re-runs anything.
	PreserveMerges bool
	// DryRun reports the commits Rebase would replay without running
	// anything. This package does not attempt to predict whether a real
	// rebase would conflict — that requires actually replaying the
	// patches, which a dry run by definition does not do.
	DryRun bool
	// Sync and Remote describe how the rewritten ref's remote counterpart
	// is handled; see SyncMode. Ignored when DryRun is true.
	Sync   SyncMode
	Remote string
}

// RebaseResult is the outcome of a (possibly dry-run) rebase.
type RebaseResult struct {
	*RewriteOutcome
	// Commits is populated only for a dry run: the commits, oldest first,
	// that a real Rebase call would replay.
	Commits []string
}

// Rebase replays the currently checked-out branch's commits ahead of
// upstream. It never runs `git rebase --exec ... -S`: over merge history
// that path either linearizes merges away (plain rebase) or replays them by
// re-running the merge (--rebase-merges), either of which can produce a new
// tree that conflicts with something already merged from the original —
// exactly the failure mode Resign exists to avoid for the sign-only case.
// Rebase itself re-applies patches by design, that IS what a rebase is; the
// rule is narrower: don't ask rebase to sign for you mid-replay — replay
// first, then call Resign once the resulting trees are the ones you want
// signed.
//
// A conflicting rebase is aborted back to the pre-rebase state before this
// function returns ConflictError; it never leaves a half-finished rebase
// for the caller to discover later.
func (r *Repo) Rebase(ctx context.Context, upstream string, opts RebaseOptions) (*RebaseResult, error) {
	branch, err := r.currentBranch(ctx)
	if err != nil {
		return nil, err
	}
	ref := "refs/heads/" + branch
	oldHead, err := r.resolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}

	if opts.DryRun {
		rangeSpec := upstream + ".." + ref
		out, err := r.git(ctx, "rev-list", "--reverse", rangeSpec)
		if err != nil {
			return nil, fmt.Errorf("git: rebase dry-run: enumerate %s: %w", rangeSpec, err)
		}
		var commits []string
		if out != "" {
			commits = strings.Split(out, "\n")
		}
		return &RebaseResult{
			RewriteOutcome: &RewriteOutcome{Ref: ref, OldHead: oldHead, BackupRef: backupRefName(ref, oldHead), DryRun: true},
			Commits:        commits,
		}, nil
	}

	backupRef := backupRefName(ref, oldHead)
	if _, err := r.git(ctx, "update-ref", backupRef, oldHead); err != nil {
		return nil, fmt.Errorf("git: backup-ref %s before rebasing %s: %w", backupRef, ref, err)
	}

	args := []string{"rebase"}
	if opts.PreserveMerges {
		args = append(args, "--rebase-merges")
	}
	if opts.Onto != "" {
		args = append(args, "--onto", opts.Onto)
	}
	args = append(args, upstream)

	if _, rebaseErr := r.git(ctx, args...); rebaseErr != nil {
		files, _ := r.conflictedFiles(ctx)
		_, _ = r.git(ctx, "rebase", "--abort")
		if len(files) > 0 {
			return nil, &ConflictError{Op: "rebase", Files: files}
		}
		return nil, fmt.Errorf("git: rebase %s onto %s: %w", branch, upstream, rebaseErr)
	}

	newHead, err := r.resolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}

	out := &RewriteOutcome{Ref: ref, OldHead: oldHead, NewHead: newHead, BackupRef: backupRef}
	if opts.Sync == SyncEmitForceWithLease {
		remote := opts.Remote
		if remote == "" {
			remote = "origin"
		}
		out.PushCmd = []string{"git", "push", "--force-with-lease=" + ref + ":" + oldHead, remote, ref}
	}
	return &RebaseResult{RewriteOutcome: out}, nil
}

func (r *Repo) currentBranch(ctx context.Context) (string, error) {
	name, err := r.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git: current branch: %w", err)
	}
	if name == "HEAD" {
		return "", fmt.Errorf("git: current branch: detached HEAD, rebase needs a named branch")
	}
	return name, nil
}
