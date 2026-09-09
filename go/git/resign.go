package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// ResignOptions configures Resign.
type ResignOptions struct {
	// Base is the commit or ref bounding the rewrite range: Resign rewrites
	// Base..Ref (exclusive of Base, inclusive of Ref). Required — Resign
	// never infers a range, since a wrong guess would silently rewrite
	// history the caller did not ask for.
	Base string
	// SignArgs are the flags commit-tree receives to produce a signed
	// commit, e.g. []string{"-S"} for the default signing key, or
	// []string{"-S", "--gpg-sign=<keyid>"} for an explicit one. A nil slice
	// defaults to []string{"-S"}; pass an empty non-nil slice to skip
	// signing entirely (only meaningful for testing this function's tree-
	// and parent-remapping behavior in isolation from signing setup).
	SignArgs []string
	// Sync selects what happens to ref's remote counterpart; see SyncMode.
	Sync SyncMode
	// Remote names the remote a SyncEmitForceWithLease push targets.
	// Defaults to "origin".
	Remote string
	// DryRun computes the rewrite and reports the resulting head without
	// moving ref: no backup ref, no ref move. The recomputed commit objects
	// still land in git's object database (commit-tree always writes), but
	// unreferenced by any ref they are ordinary unreachable objects left
	// for a future git gc, not a repository-visible change.
	DryRun bool
}

// commitMeta is the subset of a commit's plumbing fields Resign needs to
// reconstruct it with an identical tree and a new signature.
type commitMeta struct {
	sha        string
	tree       string
	parents    []string
	authorLine string // "Name <email> unixtime tz", git's raw identity format
	committer  string
	message    string
}

// Resign rewrites every commit in Base..Ref into a signed equivalent that
// reuses the ORIGINAL commit's tree via commit-tree — it never re-applies a
// diff, so the replacement commit is content-identical to the one it
// replaces and cannot conflict with anything already merged from the
// original. This is what makes Resign safe over merge history where a
// rebase is not: a rebase replays patches (dropping or re-running merges
// depending on flags), while commit-tree only forges a new commit object
// around an unchanged tree and remapped parents — nothing is re-derived,
// so there is no content to conflict over.
//
// Resign never shells to `git rebase --exec ... -S`.
func (r *Repo) Resign(ctx context.Context, ref string, opts ResignOptions) (*RewriteOutcome, error) {
	if opts.Base == "" {
		return nil, fmt.Errorf("git: resign %s: Base is required", ref)
	}
	signArgs := opts.SignArgs
	if signArgs == nil {
		signArgs = []string{"-S"}
	}

	oldHead, err := r.resolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}

	commits, err := r.commitRange(ctx, opts.Base, ref)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("git: resign %s: empty range %s..%s", ref, opts.Base, ref)
	}

	// No-op short-circuit: when signing is actually requested and every
	// commit in the range already carries a verifying signature, rewriting
	// would mint fresh SHAs for zero security gain and needlessly detach any
	// downstream built on the current tips. Leave the ref untouched and say
	// so. Skipped when signArgs is empty (the signing-independent test path),
	// which can neither produce nor recognize a signature.
	if len(signArgs) > 0 {
		allSigned, err := r.rangeFullySigned(ctx, commits)
		if err != nil {
			return nil, err
		}
		if allSigned {
			report, err := r.resignReport(ctx, commits, nil, opts.Base, oldHead)
			if err != nil {
				return nil, err
			}
			return &RewriteOutcome{Ref: ref, OldHead: oldHead, NewHead: oldHead, NoOp: true, DryRun: opts.DryRun, Post: report}, nil
		}
	}

	remap := make(map[string]string, len(commits))
	var newHead string
	for _, c := range commits {
		newParents := make([]string, len(c.parents))
		for i, p := range c.parents {
			if mapped, ok := remap[p]; ok {
				newParents[i] = mapped
			} else {
				// p sits outside the rewritten range (Base itself, or an
				// ancestor reached through a merge that predates Base) —
				// only commits inside Base..Ref get a new identity.
				newParents[i] = p
			}
		}
		newSHA, err := r.commitTree(ctx, c, newParents, signArgs)
		if err != nil {
			return nil, fmt.Errorf("git: resign: recommit %s: %w", c.sha, err)
		}
		remap[c.sha] = newSHA
		newHead = newSHA
	}

	report, err := r.resignReport(ctx, commits, remap, opts.Base, newHead)
	if err != nil {
		return nil, err
	}

	if opts.DryRun {
		return &RewriteOutcome{Ref: ref, OldHead: oldHead, NewHead: newHead, BackupRef: backupRefName(ref, oldHead), DryRun: true, Post: report}, nil
	}
	out, err := r.MoveRef(ctx, ref, oldHead, newHead, opts.Sync, opts.Remote, false)
	if err != nil {
		return nil, err
	}
	out.Post = report
	return out, nil
}

// commitRange enumerates Base..Ref oldest-first with each commit's direct
// parents, in one call: --topo-order combined with --reverse guarantees
// every parent appears before its children, which is what lets Resign's
// single forward pass remap each parent before the child that needs it.
func (r *Repo) commitRange(ctx context.Context, base, ref string) ([]commitMeta, error) {
	out, err := r.git(ctx, "rev-list", "--reverse", "--topo-order", "--parents", base+".."+ref)
	if err != nil {
		return nil, fmt.Errorf("git: enumerate range %s..%s: %w", base, ref, err)
	}
	if out == "" {
		return nil, nil
	}
	var commits []commitMeta
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		meta, err := r.readCommit(ctx, fields[0])
		if err != nil {
			return nil, err
		}
		meta.parents = fields[1:]
		commits = append(commits, meta)
	}
	return commits, nil
}

// readCommit reads sha's tree, author, and committer identity, and message
// from git's plumbing. Parent SHAs come from commitRange's rev-list output,
// not from here, so this never has to reconcile two parent lists.
//
// The message is sliced out of the raw object verbatim — everything after the
// blank line that ends the header block, byte for byte. A line-oriented
// reassembly cannot be exact: it loses a message's trailing carriage return
// and forces a trailing newline onto one that has none, either of which would
// give the resigned commit a different message than the original.
func (r *Repo) readCommit(ctx context.Context, sha string) (commitMeta, error) {
	raw, err := r.gitRaw(ctx, "cat-file", "-p", sha)
	if err != nil {
		return commitMeta{}, fmt.Errorf("git: read commit %s: %w", sha, err)
	}
	meta := commitMeta{sha: sha}

	// The header block ends at the first blank line ("\n\n"); the rest is the
	// message. A gpgsig header never introduces an earlier "\n\n" because its
	// continuation lines (including the PGP block's own blank lines) are all
	// space-prefixed, so this split is unambiguous even on a signed commit.
	header := raw
	if sep := bytes.Index(raw, []byte("\n\n")); sep >= 0 {
		header = raw[:sep]
		meta.message = string(raw[sep+2:])
	}
	for _, line := range bytes.Split(header, []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("tree ")):
			meta.tree = string(line[len("tree "):])
		case bytes.HasPrefix(line, []byte("author ")):
			meta.authorLine = string(line[len("author "):])
		case bytes.HasPrefix(line, []byte("committer ")):
			meta.committer = string(line[len("committer "):])
		}
	}
	return meta, nil
}

// sigState classifies a commit object's embedded signature.
type sigState int

const (
	sigUnsigned sigState = iota // no signature header
	sigGood                     // a signature that verifies against an available key
	sigBad                      // a signature that does not verify (or cannot be trusted)
)

// classifyGitSig maps git's %G? signature-status code to a sigState. G/U are
// cryptographically good (U only lacks a trust path, irrelevant to whether the
// commit is signed); N is unsigned; everything else — B (bad), E (key
// unavailable), X/Y/R (expired/revoked) — is a signature we will not honor.
func classifyGitSig(code string) sigState {
	switch code {
	case "G", "U":
		return sigGood
	case "N", "":
		return sigUnsigned
	default:
		return sigBad
	}
}

// commitSigState reports whether sha's commit object is unsigned, well-signed,
// or badly signed, per git's own signature verification (%G?).
func (r *Repo) commitSigState(ctx context.Context, sha string) (sigState, error) {
	code, err := r.git(ctx, "log", "-1", "--format=%G?", sha)
	if err != nil {
		return sigUnsigned, fmt.Errorf("git: signature status of %s: %w", sha, err)
	}
	return classifyGitSig(code), nil
}

// rangeFullySigned reports whether every commit in the range already carries a
// verifying signature — the condition under which Resign is a no-op.
func (r *Repo) rangeFullySigned(ctx context.Context, commits []commitMeta) (bool, error) {
	for _, c := range commits {
		state, err := r.commitSigState(ctx, c.sha)
		if err != nil {
			return false, err
		}
		if state != sigGood {
			return false, nil
		}
	}
	return true, nil
}

// ResignReport is Resign's post-condition verification of a rewrite: proof
// that the operation changed only signatures and did not corrupt history.
type ResignReport struct {
	// Commits is the number of commits examined over the range.
	Commits int
	// TreesPreserved is true iff every examined commit's tree object is
	// identical to the original commit it replaced — Resign must never alter a
	// tree, only re-sign the commit around it.
	TreesPreserved bool
	// UnsignedCount is how many examined commits carry no signature.
	UnsignedCount int
	// BadSignatureCount is how many examined commits carry a signature that
	// does not verify.
	BadSignatureCount int
	// BaseIsAncestor confirms Base is still an ancestor of the new tip, i.e.
	// the rewrite re-based nothing off the range's foundation.
	BaseIsAncestor bool
}

// resignReport verifies the post-conditions of a resign over commits. remap
// maps each original SHA to its rewritten replacement; a nil remap means
// nothing was rewritten (the no-op path), so each original commit is examined
// in place. newTip is the SHA Base must remain an ancestor of.
func (r *Repo) resignReport(ctx context.Context, commits []commitMeta, remap map[string]string, base, newTip string) (*ResignReport, error) {
	rep := &ResignReport{Commits: len(commits), TreesPreserved: true}
	for _, c := range commits {
		target := c.sha
		if remap != nil {
			target = remap[c.sha]
		}
		tree, err := r.git(ctx, "rev-parse", "--verify", target+"^{tree}")
		if err != nil {
			return nil, err
		}
		if tree != c.tree {
			rep.TreesPreserved = false
		}
		state, err := r.commitSigState(ctx, target)
		if err != nil {
			return nil, err
		}
		switch state {
		case sigUnsigned:
			rep.UnsignedCount++
		case sigBad:
			rep.BadSignatureCount++
		}
	}
	anc, err := r.isAncestor(ctx, base, newTip)
	if err != nil {
		return nil, err
	}
	rep.BaseIsAncestor = anc
	return rep, nil
}

// isAncestor reports whether ancestor is an ancestor of descendant. It reads
// merge-base's exit code directly (0 yes, 1 no) rather than through git(),
// which would turn the legitimate "not an ancestor" exit into an error.
func (r *Repo) isAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	args := []string{"merge-base", "--is-ancestor", ancestor, descendant}
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: r.Dir})
	if err != nil {
		return false, fmt.Errorf("git: ancestry %s..%s: %w", ancestor, descendant, err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// parseIdentity splits git's raw "Name <email> date" identity line into its
// three fields.
func parseIdentity(line string) (name, email, date string) {
	open := strings.IndexByte(line, '<')
	closeIdx := strings.IndexByte(line, '>')
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return line, "", ""
	}
	return strings.TrimSpace(line[:open]), line[open+1 : closeIdx], strings.TrimSpace(line[closeIdx+1:])
}

// commitTree forges a new commit object around c's original tree with
// newParents and signArgs, preserving c's exact author and committer
// identity and date via GIT_AUTHOR_*/GIT_COMMITTER_* — Resign changes only
// the signature, never who wrote what or when.
func (r *Repo) commitTree(ctx context.Context, c commitMeta, newParents []string, signArgs []string) (string, error) {
	args := []string{"commit-tree"}
	args = append(args, signArgs...)
	for _, p := range newParents {
		args = append(args, "-p", p)
	}
	args = append(args, c.tree)

	authorName, authorEmail, authorDate := parseIdentity(c.authorLine)
	commName, commEmail, commDate := parseIdentity(c.committer)

	res, err := sysops.Run(ctx, "git", args, sysops.Options{
		Dir: r.Dir,
		Env: append(os.Environ(),
			"GIT_AUTHOR_NAME="+authorName,
			"GIT_AUTHOR_EMAIL="+authorEmail,
			"GIT_AUTHOR_DATE="+authorDate,
			"GIT_COMMITTER_NAME="+commName,
			"GIT_COMMITTER_EMAIL="+commEmail,
			"GIT_COMMITTER_DATE="+commDate,
		),
		Stdin: strings.NewReader(c.message),
	})
	if err != nil {
		return "", fmt.Errorf("git: commit-tree for %s: %w", c.sha, err)
	}
	if res.ExitCode != 0 {
		return "", &CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
