"""The changed-module gate: no behaviour change ships without a release.

A module that changed since the base ref must carry a version bump AND a published
release. This is the half of the transaction a release record cannot cover, because the
defect it catches is a release that was never declared at all -- an edit merged into a
module whose version and tags stayed where they were, leaving every consumer pinned to
bytes that no longer describe the module.

Module roots, where each kind's version is read, which tag names count as its release, and
the short list of repository-infrastructure paths that ship in no release are all declared
in the contract.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass
from fnmatch import fnmatch
from pathlib import Path, PurePosixPath
from typing import Any

from . import gitstate
from .contract import Contract, parse_version, render_template, resolve_pointer

_JSON_POINTER = "json-pointer"
_REGEX = "regex"
_TAG = "tag"


class ChangedError(RuntimeError):
    """The changed-module gate cannot run: a bad ref, or an unreadable repository."""


@dataclass(frozen=True)
class Module:
    """A released module that owns changed paths.

    Attributes:
        kind: Contract module-kind id that recognised it.
        path: Module root, relative to the repository root (`.` for the repository itself).
        name: Module name used to render tag templates.
        version_source: How the kind declares its version: a JSON pointer, a regex, or tags.
        declaration: The contract's module-kind entry, for the pointer or pattern.
    """

    kind: str
    path: str
    name: str
    version_source: str
    declaration: dict[str, Any]


@dataclass(frozen=True)
class ModuleVerdict:
    """One module's outcome under the changed-module rule."""

    module: Module
    status: str
    detail: str
    changed: tuple[str, ...]

    @property
    def fails(self) -> bool:
        """True if this module changed without a complete release."""
        return self.status != "released"


@dataclass(frozen=True)
class ChangedReport:
    """The gate's report over every module that changed between two refs."""

    base: str
    head: str
    verdicts: tuple[ModuleVerdict, ...]
    exempt: tuple[str, ...]
    unowned: tuple[str, ...]

    @property
    def failures(self) -> tuple[ModuleVerdict, ...]:
        """Modules that changed without a version bump and a published release."""
        return tuple(verdict for verdict in self.verdicts if verdict.fails)

    @property
    def exit_code(self) -> int:
        """Process exit code: 1 when any module changed without a release."""
        return 1 if self.failures else 0


def check_changed(contract: Contract, repo: Path, base: str, head: str = "HEAD") -> ChangedReport:
    """Require every module changed between `base` and `head` to carry a release.

    Args:
        contract: The loaded contract.
        repo: Repository directory.
        base: Base revision the change is measured from.
        head: Head revision (default: `HEAD`).

    Returns:
        One verdict per changed module, plus the paths skipped as exempt or unowned.

    Raises:
        ChangedError: `repo` is not a repository, or a revision does not resolve. A base
            ref that cannot be read never passes the gate by default.
    """
    if not gitstate.is_repo(repo):
        raise ChangedError(f"{repo} is not a git working tree")
    for ref in (base, head):
        if not gitstate.ref_exists(repo, ref):
            raise ChangedError(f"{repo}: revision {ref!r} does not resolve -- refusing to judge a release against a ref that is not there")

    rule = contract.changed_module_rule
    kinds = tuple(rule.get("module_kinds", ()))
    exempt_globs = tuple(rule.get("exempt_paths", ()))

    exempt: list[str] = []
    unowned: list[str] = []
    owned: dict[str, list[str]] = {}
    modules: dict[str, Module] = {}

    for path in gitstate.changed_paths(repo, base, head):
        if any(fnmatch(path, glob) for glob in exempt_globs):
            exempt.append(path)
            continue
        module = _owning_module(repo, path, kinds)
        if module is None:
            unowned.append(path)
            continue
        modules[module.path] = module
        owned.setdefault(module.path, []).append(path)

    verdicts = [
        _verdict(contract, repo, modules[module_path], base, head, tuple(sorted(paths)))
        for module_path, paths in sorted(owned.items())
    ]
    return ChangedReport(base=base, head=head, verdicts=tuple(verdicts), exempt=tuple(exempt), unowned=tuple(unowned))


def _owning_module(repo: Path, path: str, kinds: tuple[dict[str, Any], ...]) -> Module | None:
    """The nearest module root above `path`, or None when no module owns it."""
    candidate = PurePosixPath(path).parent
    while True:
        relative = "." if str(candidate) == "." else str(candidate)
        for declaration in kinds:
            if _matches(repo, relative, declaration):
                return Module(
                    kind=declaration["id"],
                    path=relative,
                    name=repo.name if relative == "." else PurePosixPath(relative).name,
                    version_source=declaration.get("version_source", _TAG),
                    declaration=declaration,
                )
        if str(candidate) == ".":
            return None
        candidate = candidate.parent


def _matches(repo: Path, relative: str, declaration: dict[str, Any]) -> bool:
    """True if this directory is a module root of the declared kind."""
    directory = repo if relative == "." else repo / relative
    marker = declaration.get("root_marker")
    if marker is not None:
        return (directory / str(marker)).is_file()
    glob = declaration.get("root_glob")
    return bool(glob) and relative != "." and fnmatch(relative, str(glob))


def _verdict(contract: Contract, repo: Path, module: Module, base: str, head: str, changed: tuple[str, ...]) -> ModuleVerdict:
    """Judge one changed module: version bump, then published release."""
    if module.version_source == _TAG:
        return _tag_versioned_verdict(contract, repo, module, base, changed)

    marker = module.declaration["root_marker"]
    manifest_path = marker if module.path == "." else f"{module.path}/{marker}"
    head_version = _version_at(repo, head, manifest_path, module.declaration)
    if head_version is None:
        return ModuleVerdict(module, "unreadable", f"{manifest_path} at {head} states no version", changed)
    base_version = _version_at(repo, base, manifest_path, module.declaration)

    if base_version is not None:
        if head_version == base_version:
            return ModuleVerdict(
                module,
                "no-version-bump",
                f"{module.path} changed ({len(changed)} path(s)) but {manifest_path} is still {head_version} -- a change with no version bump ships to consumers as the previous release",
                changed,
            )
        ordering_failure = _ordering_failure(contract, base_version, head_version, manifest_path)
        if ordering_failure is not None:
            return ModuleVerdict(module, "version-not-increased", ordering_failure, changed)

    tags = _release_tags(contract, module, head_version)
    published = [tag for tag in tags if gitstate.tag_exists(repo, tag)]
    if not published:
        return ModuleVerdict(
            module,
            "no-published-release",
            f"{module.path} is at {head_version} with no release tag -- expected one of: {', '.join(tags)}",
            changed,
        )
    return ModuleVerdict(module, "released", f"{module.path} bumped to {head_version} and released as {published[0]}", changed)


def _tag_versioned_verdict(contract: Contract, repo: Path, module: Module, base: str, changed: tuple[str, ...]) -> ModuleVerdict:
    """Judge a module whose only version marker is a tag."""
    globs = _release_tags(contract, module, "*")
    candidates = sorted({tag for glob in globs for tag in gitstate.list_tags(repo, glob)})
    released = [tag for tag in candidates if not gitstate.is_ancestor(repo, tag, base)]
    if not released:
        return ModuleVerdict(
            module,
            "no-published-release",
            f"{module.path} changed ({len(changed)} path(s)) but carries no release tag cut after {base} -- its version is its tag, so an unreleased change is invisible to consumers",
            changed,
        )
    return ModuleVerdict(module, "released", f"{module.path} released as {released[0]}", changed)


def _release_tags(contract: Contract, module: Module, version: str) -> list[str]:
    """Tag names that count as this module's release at `version`."""
    substitutions = {"module_path": module.path, "module_name": module.name, "version": version}
    return [render_template(template, substitutions) for template in contract.changed_module_rule.get("tag_templates", ())]


def _version_at(repo: Path, ref: str, manifest_path: str, declaration: dict[str, Any]) -> str | None:
    """The version a module's manifest states at a revision, or None when unreadable."""
    blob = gitstate.file_at_ref(repo, ref, manifest_path)
    if blob is None:
        return None
    source = declaration.get("version_source")
    if source == _JSON_POINTER:
        try:
            document = json.loads(blob)
        except json.JSONDecodeError:
            return None
        found, value = resolve_pointer(document, declaration["pointer"])
        return value if found and isinstance(value, str) else None
    if source == _REGEX:
        match = re.search(declaration["pattern"], blob)
        return match.group(1) if match else None
    return None


def _ordering_failure(contract: Contract, base_version: str, head_version: str, manifest_path: str) -> str | None:
    """Reject a version change that is not an increase; None when the bump is a bump."""
    pattern = contract.changed_module_rule["version_pattern"]
    base_triple = parse_version(base_version, pattern)
    head_triple = parse_version(head_version, pattern)
    if base_triple is None or head_triple is None:
        return f"{manifest_path}: {base_version} -> {head_version} is not a comparable semantic-version change"
    if head_triple <= base_triple:
        return f"{manifest_path}: {base_version} -> {head_version} does not increase the version"
    return None
