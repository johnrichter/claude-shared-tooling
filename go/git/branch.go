package git

import (
	"context"
	"fmt"
	"strings"
)

// BranchOptions configures CreateBranch.
type BranchOptions struct {
	// Force moves an existing branch of the same name instead of refusing.
	Force bool
	// DryRun validates startPoint resolves without creating the branch.
	DryRun bool
}

// CreateBranch creates name pointing at startPoint.
func (r *Repo) CreateBranch(ctx context.Context, name, startPoint string, opts BranchOptions) error {
	if _, err := r.resolveRef(ctx, startPoint); err != nil {
		return fmt.Errorf("git: create branch %s: %w", name, err)
	}
	if opts.DryRun {
		return nil
	}
	args := []string{"branch"}
	if opts.Force {
		args = append(args, "-f")
	}
	args = append(args, name, startPoint)
	if _, err := r.git(ctx, args...); err != nil {
		return fmt.Errorf("git: create branch %s at %s: %w", name, startPoint, err)
	}
	return nil
}

// DeleteBranch deletes name as a compare-and-swap against expectedHead: it
// writes a backup ref for expectedHead for recovery, then removes the ref
// only if it still points there, refusing (StaleRefError) if something moved
// it first. name may be a short branch name or a full refs/heads/... ref.
func (r *Repo) DeleteBranch(ctx context.Context, name, expectedHead string, dryRun bool) (*RewriteOutcome, error) {
	ref := name
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + name
	}
	current, err := r.resolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if current != expectedHead {
		return nil, &StaleRefError{Ref: ref, ExpectedOld: expectedHead, ActualOld: current}
	}

	backupRef := backupRefName(ref, current)
	out := &RewriteOutcome{Ref: ref, OldHead: current, BackupRef: backupRef, DryRun: dryRun}
	if dryRun {
		return out, nil
	}
	if _, err := r.git(ctx, "update-ref", backupRef, current); err != nil {
		return nil, fmt.Errorf("git: backup-ref %s before deleting %s: %w", backupRef, ref, err)
	}
	if _, err := r.git(ctx, "update-ref", "-d", ref, current); err != nil {
		return nil, fmt.Errorf("git: CAS delete %s: %w", ref, err)
	}
	return out, nil
}
