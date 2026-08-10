"""Finds a committed binary in the tracked tree, scoped to executables.

SC-DISTRIBUTION targets one class of committed artifact: a built binary meant to be exec'd
directly (a CLI output like `go/.bin/<tool>-<goos>-<goarch>`) — the exact
class CD (per-OS/arch release archives) exists to replace. It does not target a legitimately
committed binary asset (an image, a font) that carries no execute bit; those are a separate,
existing concern (`scripts/check_no_raw_binary.py`) with a different threshold and LFS
exemption. A candidate here is git's own tracked file mode, `100755`, whose content is binary
by git's own heuristic — a NUL byte or a UTF-8 decode failure in its leading bytes.
"""
from __future__ import annotations

import subprocess
from pathlib import Path

SNIFF_BYTES = 8000  # matches git's own is-binary heuristic read size
_EXEC_MODE = "100755"


def tracked_executables(root: Path) -> list[str]:
    """Repo-relative paths of every git-tracked file whose tracked mode is executable."""
    result = subprocess.run(
        ["git", "ls-files", "-s"], cwd=root, capture_output=True, text=True, check=True,
    )
    paths: list[str] = []
    for line in result.stdout.splitlines():
        if not line:
            continue
        meta, _, path = line.partition("\t")
        mode = meta.split()[0]
        if mode == _EXEC_MODE:
            paths.append(path)
    return paths


def is_binary_content(path: Path) -> bool:
    """True if the file's leading bytes look binary: a NUL byte, or a chunk that fails
    UTF-8 decoding."""
    try:
        with path.open("rb") as f:
            prefix = f.read(SNIFF_BYTES)
    except OSError:
        return False  # unreadable — nothing to flag
    if b"\x00" in prefix:
        return True
    try:
        prefix.decode("utf-8")
    except UnicodeDecodeError:
        return True
    return False


def scan(root: Path, allowlist: frozenset[str]) -> list[str]:
    """Repo-relative paths of every committed binary not in `allowlist`."""
    violations: list[str] = []
    for rel in tracked_executables(root):
        p = root / rel
        if not p.is_file():
            continue  # deleted since the index was written — nothing to inspect
        if not is_binary_content(p):
            continue
        if rel in allowlist:
            continue
        violations.append(rel)
    return violations
