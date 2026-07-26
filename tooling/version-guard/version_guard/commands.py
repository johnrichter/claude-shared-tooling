"""The exact tag-and-release command set SC-VERSIONING expects for a module cut.

One canonical rendering, so "how do I cut a release" has one answer instead of a
convention re-derived by hand each time — and so a test can assert on it verbatim.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ReleasePlan:
    """The tag this cut uses, and the commands that produce it."""

    tag: str
    commands: tuple[str, ...]


def tag_name(module_path: str, version: str) -> str:
    """The tag SC-VERSIONING assigns a module cut: `<module_path>/vX.Y.Z`, or bare
    `vX.Y.Z` for the top-level module (`module_path == ""`)."""
    version = version[1:] if version.startswith("v") else version
    return f"v{version}" if module_path == "" else f"{module_path}/v{version}"

def render(module_path: str, version: str, *, commit: str = "HEAD") -> ReleasePlan:
    """The exact commands that tag and release one module cut."""
    tag = tag_name(module_path, version)
    commands = (
        f'git tag -a {tag} -m "{tag}" {commit}',
        f"git push origin {tag}",
        f'gh release create {tag} --title "{tag}" --notes "Release {tag}."',
    )
    return ReleasePlan(tag=tag, commands=commands)
