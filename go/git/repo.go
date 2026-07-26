package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// Repo is a git repository or linked worktree rooted at Dir. Every method on
// Repo runs relative to Dir, never the calling process's working directory,
// so a caller can drive independent operations against several worktrees of
// the same repository concurrently.
type Repo struct {
	Dir string
}

// Open returns a Repo rooted at dir after confirming dir sits inside a git
// working tree. It does not resolve dir to the repository's top level: dir
// may be any path git accepts, including a linked worktree created by
// WorktreeAdd.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	if _, err := r.git(ctx, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("git: open %s: not a git working tree: %w", dir, err)
	}
	return r, nil
}

// git runs a git subcommand rooted at r.Dir and returns its trimmed stdout.
// A non-zero exit surfaces as *CommandError carrying git's own stderr, since
// that diagnostic — not a generic wrapped error — is what every caller in
// this package acts on or reports.
func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	out, err := r.gitRaw(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitRaw is like git but returns untrimmed stdout bytes, for callers (commit
// message and tree-content plumbing) that must not lose meaningful leading
// or trailing whitespace.
func (r *Repo) gitRaw(ctx context.Context, args ...string) ([]byte, error) {
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: r.Dir})
	if err != nil {
		return nil, fmt.Errorf("git: exec git %s: %w", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		return nil, &CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return res.Stdout, nil
}

// resolveRef returns the commit SHA ref currently points at.
func (r *Repo) resolveRef(ctx context.Context, ref string) (string, error) {
	sha, err := r.git(ctx, "rev-parse", "--verify", ref)
	if err != nil {
		return "", fmt.Errorf("git: resolve ref %s: %w", ref, err)
	}
	return sha, nil
}
