"""Rust dependency rule: a crate depends on another module by git tag, never by a
path/relative dependency.

A `path = "..."` dependency only ever resolves inside the checkout that produced it, so it
can't survive the module crossing a repo boundary (or even just being tagged and consumed
independently of its neighbor). `git = "...", tag = "..."` is the only cross-module
dependency this repo's SC-VERSIONING convention recognizes.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path

_SECTION_RE = re.compile(r"^\[(?P<name>[^]]+)\]\s*$")
_QUOTED_VALUE_RE = re.compile(r'^"(?P<path>[^"]+)"')
_INLINE_PATH_RE = re.compile(r'path\s*=\s*"(?P<path>[^"]+)"')
_DEP_SECTION_WORDS = ("dependencies", "dev-dependencies", "build-dependencies")


class DepsError(ValueError):
    """A Cargo manifest could not be read."""


@dataclass(frozen=True)
class PathDepViolation:
    """One `path = ...` dependency found in a Cargo manifest."""

    manifest: Path
    line: int
    dependency: str
    path: str

    def __str__(self) -> str:
        return f"{self.manifest}:{self.line}: {self.dependency!r} is a path dependency ({self.path!r}); use a git tag dependency instead"


def _is_dependency_section(section: str) -> bool:
    parts = section.replace('"', "").replace("'", "").split(".")
    return any(part in _DEP_SECTION_WORDS for part in parts)


def _dependency_subtable_name(section: str) -> str | None:
    """If `section` is a dotted dependency subtable — `[dependencies.<name>]` and its
    `target.*`/`workspace`-scoped forms — the depended-on crate name; else None. In a
    subtable the crate name is the section, and a bare `path = "..."` line is its field; in
    a plain `[dependencies]` table the keys are the crate names, so `path` is a crate, not a
    field."""
    parts = section.replace('"', "").replace("'", "").split(".")
    for i, part in enumerate(parts):
        if part in _DEP_SECTION_WORDS and i < len(parts) - 1:
            return ".".join(parts[i + 1:])
    return None


def scan_manifest(manifest: Path) -> list[PathDepViolation]:
    """Find every `path = ...` dependency in one Cargo.toml."""
    try:
        text = manifest.read_text(encoding="utf-8")
    except OSError as exc:
        raise DepsError(f"{manifest}: {exc}") from exc

    violations: list[PathDepViolation] = []
    section = ""
    for lineno, raw in enumerate(text.splitlines(), start=1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        section_match = _SECTION_RE.match(line)
        if section_match:
            section = section_match.group("name").strip()
            continue
        if not _is_dependency_section(section):
            continue
        if "=" not in line:
            continue

        key, _, value = line.partition("=")
        key = key.strip()
        value = value.strip()

        if key == "path":
            crate = _dependency_subtable_name(section)
            quoted = _QUOTED_VALUE_RE.match(value) if crate is not None else None
            if quoted:
                violations.append(PathDepViolation(manifest, lineno, crate, quoted.group("path")))
            continue

        inline = _INLINE_PATH_RE.search(value) if "{" in value else None
        if inline:
            violations.append(PathDepViolation(manifest, lineno, key, inline.group("path")))

    return violations


def find_cargo_manifests(repo_root: Path) -> list[Path]:
    """Every `Cargo.toml` in the repo, deterministically ordered."""
    return sorted(repo_root.rglob("Cargo.toml"))


def scan_repo(repo_root: Path) -> list[PathDepViolation]:
    """Find every `path = ...` Rust dependency across the repo's Cargo manifests."""
    violations: list[PathDepViolation] = []
    for manifest in find_cargo_manifests(repo_root):
        violations.extend(scan_manifest(manifest))
    return violations
