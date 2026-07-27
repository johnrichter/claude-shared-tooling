"""File-level orchestration.

Extracts comments, runs the banned-content and doc-presence rules, and returns every
violation found across a given file list.
"""
from __future__ import annotations

from pathlib import Path

from . import comments, extensions, rules


def scan_file(path: Path, repo_root: Path) -> list[rules.Violation]:
    """Scan one file (already known to be source) for every rule class this tool owns."""
    try:
        rel = str(path.relative_to(repo_root))
    except ValueError:
        # A path outside repo_root (e.g. an ad hoc fixture passed via --files) is still
        # scannable — it just gets reported under its own path instead of a repo-relative one.
        rel = str(path)
    ext = extensions.extension_of(rel)
    try:
        text = path.read_text(encoding="utf-8")
    except (UnicodeDecodeError, OSError):
        return []
    violations = []
    for comment in comments.extract(text, ext):
        violations.extend(rules.scan_comment(rel, comment.line, comment.text))
    violations.extend(rules.scan_doc_presence(rel, ext, text.splitlines()))
    return violations


def scan_files(paths: list[Path], repo_root: Path) -> list[rules.Violation]:
    """Scan every source-eligible path in `paths`, sorted for deterministic output."""
    violations = []
    for path in sorted(p for p in paths if extensions.is_source(str(p))):
        violations.extend(scan_file(path, repo_root))
    violations.sort(key=lambda v: (v.path, v.line, v.rule))
    return violations
