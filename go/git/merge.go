package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// FastForward selects merge fast-forward behavior.
type FastForward int

const (
	FastForwardAllow FastForward = iota // git's default: fast-forward when possible
	FastForwardNever                    // always create a merge commit (--no-ff)
	FastForwardOnly                     // refuse unless a fast-forward is possible (--ff-only)
)

// MergeOptions configures Merge.
type MergeOptions struct {
	Message     string
	FastForward FastForward
	// DryRun merges into the index, reports whether it would succeed or
	// conflict, then always aborts — the checked-out branch and working tree
	// are unchanged either way, including for a fast-forwardable branch.
	// Reports clean-mergeability regardless of FastForward.
	DryRun bool
	// Sign requests a signed merge commit by passing -S to `git merge`. Its
	// zero value (false) adds no signing flag at all, reproducing today's
	// unsigned behavior exactly — every existing caller is unaffected. This
	// package neither checks whether signing is configured nor enforces that
	// it happen; the caller owns the decision and requirement, and git's own
	// error (e.g. no usable key) surfaces unchanged on failure. Honored only
	// on the real-merge path: a dry run never lands a commit, so there is
	// nothing for it to sign.
	Sign bool
}

// MergeResult is the outcome of a (possibly dry-run) merge.
type MergeResult struct {
	NewHead string
	DryRun  bool
	// WouldMerge is set only for a dry run: whether the merge would succeed
	// cleanly.
	WouldMerge bool
}

// Merge merges branches into the branch currently checked out in r.Dir. Two
// or more branches perform an octopus merge — git's own native support for
// combining more than one head into a single commit — with no special
// casing here between the two- and many-branch cases.
//
// A conflicting merge is left no messier than it started: on failure, Merge
// always runs `git merge --abort` before returning ConflictError, so a
// caller never has to notice and clean up a half-merged working tree.
func (r *Repo) Merge(ctx context.Context, branches []string, opts MergeOptions) (*MergeResult, error) {
	if len(branches) == 0 {
		return nil, fmt.Errorf("git: merge: at least one branch is required")
	}

	args := []string{"merge"}
	if opts.DryRun {
		// A dry run must never mutate HEAD. --no-commit alone is not enough:
		// it only suppresses the commit of a true merge, so a fast-forwardable
		// branch still advances HEAD. Pairing it with --no-ff forces a real,
		// abortable merge-in-progress for every case, cleaned up below.
		// FastForward only shapes the real commit, not whether the branches
		// merge cleanly, so it is deliberately not applied on the dry-run path.
		args = append(args, "--no-ff", "--no-commit")
	} else {
		switch opts.FastForward {
		case FastForwardNever:
			args = append(args, "--no-ff")
		case FastForwardOnly:
			args = append(args, "--ff-only")
		}
		if opts.Sign {
			args = append(args, "-S")
		}
	}
	if opts.Message != "" {
		args = append(args, "-m", opts.Message)
	}
	args = append(args, branches...)

	_, mergeErr := r.git(ctx, args...)

	if opts.DryRun {
		// --no-commit leaves MERGE_HEAD staged on success too; a dry run
		// must never leave the working tree merged, so always abort
		// whatever state the attempt left behind.
		if r.mergeInProgress(ctx) {
			_, _ = r.git(ctx, "merge", "--abort")
		}
		if mergeErr != nil {
			var cmdErr *CommandError
			if errors.As(mergeErr, &cmdErr) {
				return &MergeResult{DryRun: true, WouldMerge: false}, nil
			}
			return nil, mergeErr
		}
		return &MergeResult{DryRun: true, WouldMerge: true}, nil
	}

	if mergeErr != nil {
		files, _ := r.conflictedFiles(ctx)
		_, _ = r.git(ctx, "merge", "--abort")
		if len(files) > 0 {
			return nil, &ConflictError{Op: "merge", Files: files}
		}
		return nil, fmt.Errorf("git: merge %s: %w", strings.Join(branches, " "), mergeErr)
	}

	head, err := r.resolveRef(ctx, "HEAD")
	if err != nil {
		return nil, err
	}
	return &MergeResult{NewHead: head}, nil
}

func (r *Repo) mergeInProgress(ctx context.Context) bool {
	_, err := r.resolveRef(ctx, "MERGE_HEAD")
	return err == nil
}

// conflictedFiles lists paths with unresolved merge markers in the index.
func (r *Repo) conflictedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
