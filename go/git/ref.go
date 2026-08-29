package git

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SyncMode selects how a rewritten ref's remote counterpart is handled.
// This package never pushes on a caller's behalf; SyncMode only changes
// what MoveRef reports back after moving the LOCAL ref.
type SyncMode int

const (
	// SyncLocalOnly is for a ref with no shared remote counterpart yet — a
	// pre-merge branch nobody has pulled. The local compare-and-swap move
	// is the whole operation; there is nothing further to report.
	SyncLocalOnly SyncMode = iota
	// SyncEmitForceWithLease is for a ref that has already been pushed and
	// shared. MoveRef still only moves the local ref, then returns the
	// exact `git push --force-with-lease` argv a human or CI step must run
	// deliberately to publish the rewrite — this package never runs it.
	SyncEmitForceWithLease
)

// RewriteOutcome is what a history-rewriting operation (Resign, Rebase,
// DeleteBranch) did to a ref.
type RewriteOutcome struct {
	Ref       string
	OldHead   string
	NewHead   string
	BackupRef string
	DryRun    bool
	// PushCmd is the force-with-lease argv the caller may run to publish
	// the rewrite. Populated only for SyncEmitForceWithLease on a non-dry
	// run; this package never executes it.
	PushCmd []string
	// NoOp is set by Resign when the range was already fully signed: the ref
	// was left where it was and no new commit objects were minted. Other
	// operations never set it.
	NoOp bool
	// Post carries Resign's post-condition verification of the rewrite; nil
	// for operations other than Resign. See ResignReport.
	Post *ResignReport
}

// backupRefName derives a collision-resistant backup ref from ref and the SHA
// it currently points at: unique per call via a nanosecond timestamp, so
// repeated rewrites of the same ref never collide or silently overwrite an
// earlier recovery point.
//
// The returned name is a fully-qualified ref under refs/backup/ — a plain
// ref, deliberately outside refs/tags/ — that every caller hands straight to
// `git update-ref <name> <sha>`. Living outside refs/tags/ means this can
// never collide with (or double-prefix into) the tags namespace, and,
// because update-ref never creates a tag object, writing it never requires a
// GPG signature the way an annotated `git tag` write does whenever
// tag.gpgSign is on (tag.forceSignAnnotated alone does not trigger that;
// only tag.gpgSign does) — a real signature is the wrong thing to require
// for a disposable recovery marker anyway.
func backupRefName(ref, oldSHA string) string {
	base := ref
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		base = ref[i+1:]
	}
	short := oldSHA
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("refs/backup/%s/%d-%s", base, time.Now().UTC().UnixNano(), short)
}

// MoveRef lands newSHA onto ref as a compare-and-swap against oldSHA,
// tagging oldSHA for recovery first. It is the single choke point every
// history-rewriting operation in this package uses to move a branch ref —
// nothing in this package writes a branch ref any other way.
//
// The move happens in two layers: this function first re-reads ref and
// refuses (StaleRefError) if it no longer holds oldSHA, then issues
// `git update-ref ref newSHA oldSHA` — update-ref's own atomic
// compare-and-swap — closing the race between that read and the write.
//
// dryRun performs the read and computes the backup ref name it WOULD use,
// but performs neither write (no backup ref, no ref move).
func (r *Repo) MoveRef(ctx context.Context, ref, oldSHA, newSHA string, mode SyncMode, remote string, dryRun bool) (*RewriteOutcome, error) {
	current, err := r.resolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if current != oldSHA {
		return nil, &StaleRefError{Ref: ref, ExpectedOld: oldSHA, ActualOld: current}
	}

	backupRef := backupRefName(ref, oldSHA)
	out := &RewriteOutcome{Ref: ref, OldHead: oldSHA, NewHead: newSHA, BackupRef: backupRef, DryRun: dryRun}
	if dryRun {
		return out, nil
	}

	if _, err := r.git(ctx, "update-ref", backupRef, oldSHA); err != nil {
		return nil, fmt.Errorf("git: backup-ref %s before rewriting %s: %w", backupRef, ref, err)
	}
	if _, err := r.git(ctx, "update-ref", ref, newSHA, oldSHA); err != nil {
		return nil, fmt.Errorf("git: CAS move %s -> %s on %s: %w", oldSHA, newSHA, ref, err)
	}

	if mode == SyncEmitForceWithLease {
		if remote == "" {
			remote = "origin"
		}
		out.PushCmd = []string{"git", "push", "--force-with-lease=" + ref + ":" + oldSHA, remote, ref}
	}
	return out, nil
}
