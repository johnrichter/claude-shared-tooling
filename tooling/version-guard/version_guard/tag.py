"""SC-VERSIONING: a module tag's path prefix must equal the module's real path.

A tag names either the top-level package (bare `[v]X.Y.Z`, reserved for the Python package
at the repo root) or a module rooted below the repo root (`<path>/[v]X.Y.Z`, e.g.
`go/git/v1.2.0` or `schemas/model-roster/v1.0.0`). The prefix is the claim; this module
checks the claim against the tree it was cut from.
"""
from __future__ import annotations

import re
import tomllib
from dataclasses import dataclass
from pathlib import Path

_VERSION_RE = re.compile(r"^v?\d+\.\d+\.\d+$")

# Manifest files that mark a directory as a module root, independent of language.
_MANIFEST_NAMES = ("go.mod", "Cargo.toml", "pyproject.toml")


def _cargo_declares_package(manifest: Path) -> bool:
    """Whether a `Cargo.toml` declares a `[package]` — i.e. is a releasable crate, not a
    `[workspace]`-only container manifest (which carries no `[package]` and versions no
    crate of its own)."""
    try:
        data = tomllib.loads(manifest.read_text(encoding="utf-8"))
    except (OSError, tomllib.TOMLDecodeError):
        return False
    return "package" in data


class TagError(ValueError):
    """A tag fails SC-VERSIONING."""


@dataclass(frozen=True)
class ParsedTag:
    """A tag split into its module-path prefix and version segment."""

    raw: str
    prefix: str  # "" for a bare top-level tag
    version: str  # the "[v]X.Y.Z" segment as written


def parse_tag(tag: str) -> ParsedTag:
    """Split a tag into its prefix and version segment.

    Raises:
        TagError: the tag carries no recognizable `[v]X.Y.Z` version segment.
    """
    if "/" not in tag:
        if not _VERSION_RE.match(tag):
            raise TagError(f"{tag!r}: not a bare [v]X.Y.Z tag and carries no path prefix")
        return ParsedTag(raw=tag, prefix="", version=tag)

    prefix, _, version = tag.rpartition("/")
    if not _VERSION_RE.match(version):
        raise TagError(f"{tag!r}: final segment {version!r} is not [v]X.Y.Z")
    if not prefix:
        raise TagError(f"{tag!r}: empty path prefix before the version segment")
    return ParsedTag(raw=tag, prefix=prefix, version=version)


def is_module_root(repo_root: Path, rel_path: str) -> bool:
    """Whether `rel_path` (relative to `repo_root`) is a real module root.

    The top-level module is the repo root itself (`rel_path == ""`), recognized by the
    Python package manifest there. Every other module is recognized by carrying a
    language manifest directly (`go.mod`, `pyproject.toml`, or a `Cargo.toml` that declares
    a `[package]` — a `[workspace]`-only `Cargo.toml` marks a container, not a module), or
    by being an immediate child of `schemas/`, which versions manifest-less schema modules.
    """
    if rel_path == "":
        return (repo_root / "pyproject.toml").is_file()

    directory = repo_root / rel_path
    if not directory.is_dir():
        return False
    for name in _MANIFEST_NAMES:
        manifest = directory / name
        if not manifest.is_file():
            continue
        if name == "Cargo.toml" and not _cargo_declares_package(manifest):
            continue  # a [workspace]-only manifest marks a container, not a module
        return True
    parts = Path(rel_path).parts
    return len(parts) == 2 and parts[0] == "schemas"


def check_tag(repo_root: Path, tag: str) -> None:
    """Enforce SC-VERSIONING: the tag's prefix must equal a real module path.

    Raises:
        TagError: the tag is malformed, or its prefix names no real module.
    """
    parsed = parse_tag(tag)
    if not is_module_root(repo_root, parsed.prefix):
        where = parsed.prefix or "<repo root>"
        raise TagError(
            f"{tag!r}: prefix {where!r} is not a module path in this tree "
            "(SC-VERSIONING requires the tag prefix to equal the module's own path)"
        )
