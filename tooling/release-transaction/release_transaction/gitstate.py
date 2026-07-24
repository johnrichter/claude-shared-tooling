"""Read-only git access for the release gate.

Every repository fact the gate needs -- does a tag exist, what did a file say at a
ref, what changed between two refs -- is read here and nowhere else, so the rest of
the package works on plain values and stays testable against a throwaway repo.

Nothing in this module writes to a repository.
"""
from __future__ import annotations

import subprocess
from pathlib import Path


class GitError(RuntimeError):
    """A git invocation failed, or git itself is unavailable."""


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run git in `repo` and return the completed process without raising on exit status.

    Args:
        repo: Repository (or worktree) directory to run in.
        *args: Arguments after the `git` executable.

    Returns:
        The completed process, stdout/stderr captured as text.

    Raises:
        GitError: git is not installed, or `repo` is not a directory.
    """
    try:
        return subprocess.run(["git", *args], cwd=repo, capture_output=True, text=True, check=False)
    except OSError as exc:
        raise GitError(f"git unavailable in {repo}: {exc}") from exc


def is_repo(repo: Path) -> bool:
    """True if `repo` is inside a git working tree."""
    return _git(repo, "rev-parse", "--is-inside-work-tree").returncode == 0


def ref_exists(repo: Path, ref: str) -> bool:
    """True if `ref` resolves to an object in `repo`."""
    return _git(repo, "rev-parse", "-q", "--verify", f"{ref}^{{commit}}").returncode == 0


def tag_exists(repo: Path, tag: str) -> bool:
    """True if `tag` exists as a tag ref in `repo` (annotated or lightweight)."""
    return _git(repo, "rev-parse", "-q", "--verify", f"refs/tags/{tag}").returncode == 0


def list_tags(repo: Path, pattern: str) -> list[str]:
    """Tags matching a shell glob, sorted.

    Args:
        repo: Repository directory.
        pattern: Shell glob as `git tag --list` understands it.

    Returns:
        Matching tag names, sorted.
    """
    result = _git(repo, "tag", "--list", pattern)
    if result.returncode != 0:
        raise GitError(f"git tag --list {pattern} failed: {result.stderr.strip()}")
    return sorted(line.strip() for line in result.stdout.splitlines() if line.strip())


def file_at_ref(repo: Path, ref: str, path: str) -> str | None:
    """Contents of `path` as of `ref`, or None if the ref carries no such file.

    Args:
        repo: Repository directory.
        ref: Any revision git understands.
        path: Repository-relative path.

    Returns:
        The file's text at that revision, or None when it does not exist there.
    """
    result = _git(repo, "show", f"{ref}:{path}")
    return result.stdout if result.returncode == 0 else None


def changed_paths(repo: Path, base: str, head: str) -> list[str]:
    """Repository-relative paths that differ between the merge base of `base`/`head` and `head`.

    Three-dot semantics deliberately: a gate on a branch must judge what the branch
    changed, not what the base branch moved on to in the meantime.

    Args:
        repo: Repository directory.
        base: Base revision.
        head: Head revision.

    Returns:
        Sorted, de-duplicated changed paths.

    Raises:
        GitError: the diff could not be computed.
    """
    result = _git(repo, "diff", "--name-only", f"{base}...{head}")
    if result.returncode != 0:
        raise GitError(f"git diff {base}...{head} failed: {result.stderr.strip()}")
    return sorted({line for line in result.stdout.splitlines() if line})


def is_ancestor(repo: Path, candidate: str, descendant: str) -> bool:
    """True if `candidate` is an ancestor of `descendant` (both revisions must exist)."""
    return _git(repo, "merge-base", "--is-ancestor", candidate, descendant).returncode == 0
