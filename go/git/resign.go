package git

import (
	"bufio"
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
	// moving ref: no backup tag, no ref move. The recomputed commit objects
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

	if opts.DryRun {
		return &RewriteOutcome{Ref: ref, OldHead: oldHead, NewHead: newHead, BackupTag: backupTagName(ref, oldHead), DryRun: true}, nil
	}
	return r.MoveRef(ctx, ref, oldHead, newHead, opts.Sync, opts.Remote, false)
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
func (r *Repo) readCommit(ctx context.Context, sha string) (commitMeta, error) {
	raw, err := r.gitRaw(ctx, "cat-file", "-p", sha)
	if err != nil {
		return commitMeta{}, fmt.Errorf("git: read commit %s: %w", sha, err)
	}
	meta := commitMeta{sha: sha}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var msg strings.Builder
	inBody := false
	for scanner.Scan() {
		line := scanner.Text()
		if inBody {
			msg.WriteString(line)
			msg.WriteByte('\n')
			continue
		}
		if line == "" {
			inBody = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "tree "):
			meta.tree = strings.TrimPrefix(line, "tree ")
		case strings.HasPrefix(line, "author "):
			meta.authorLine = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "committer "):
			meta.committer = strings.TrimPrefix(line, "committer ")
		}
	}
	if err := scanner.Err(); err != nil {
		return commitMeta{}, fmt.Errorf("git: parse commit %s: %w", sha, err)
	}
	meta.message = msg.String()
	return meta, nil
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
