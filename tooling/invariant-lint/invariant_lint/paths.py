"""Repo-qualified path resolution, matching the scheme the registry itself defines.

Every path an entry declares is qualified by checkout name (`ai-shared-lib/...`,
`marketplace/...`): the first path segment names the repository it resolves in, so an
entry means the same thing regardless of which checkout the reader happens to sit in.
This repo is located from this file; every other repo defaults to a sibling directory of
it, which an explicit override replaces.
"""
from __future__ import annotations

from pathlib import Path

SELF_REPO = "ai-shared-lib"


def resolve_repos(self_root: Path, overrides: list[str]) -> dict[str, Path | None]:
    """Map each repo name named by an override to its checkout, seeded with this one."""
    resolved: dict[str, Path | None] = {SELF_REPO: self_root}
    for override in overrides:
        name, _, raw = override.partition("=")
        if not name or not raw:
            raise ValueError(f"--repo expects NAME=PATH, got {override!r}")
        path = Path(raw).expanduser().resolve()
        resolved[name] = path if path.is_dir() else None
    return resolved


def repo_dir(repos: dict[str, Path | None], self_root: Path, name: str) -> Path | None:
    """Resolve a repo name to a checkout directory, caching the answer."""
    if name not in repos:
        sibling = self_root.parent / name
        repos[name] = sibling if sibling.is_dir() else None
    return repos[name]


def resolve_path(repos: dict[str, Path | None], self_root: Path, qualified: str) -> Path | None:
    """Resolve a repo-qualified path to a filesystem path, or None if its repo is absent."""
    repo, _, rest = qualified.partition("/")
    root = repo_dir(repos, self_root, repo)
    return None if root is None else root / rest
