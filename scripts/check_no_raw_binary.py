#!/usr/bin/env python3
"""No-raw-binary guard — stdlib-only, zero setup.

Closes the gap left by extension-based Git LFS routing: a `.gitattributes` glob
only catches a binary whose extension it names. A binary committed with no
extension (or an unlisted one) lands as a raw git object — this checker finds
it by CONTENT, not by name, so the gap is closed for every extension at once.

A candidate blob FAILS only when ALL three hold:
  (a) BINARY   — a NUL byte in the first ~8000 bytes of the file (git's own
                 is-binary heuristic), OR that prefix fails UTF-8 decoding.
  (b) OVERSIZE — file size exceeds --max-bytes (default 5 MiB).
  (c) NOT LFS  — `git check-attr filter -- <path>` reports a filter other
                 than `lfs` for the path. An LFS-tracked file staged/committed
                 as its small text pointer is NOT binary by (a), so it never
                 reaches this condition and always passes regardless.

Candidate set is mode-dependent:
  --staged   (default, for the pre-commit hook) — currently staged additions
             and modifications: `git diff --cached --name-only --diff-filter=AM`.
  --tracked  (for CI, authoritative — also catches a raw binary already
             committed in the past) — the full tracked tree: `git ls-files`.

Paths that no longer exist on disk (e.g. staged deletions) are skipped — there
is no content left to inspect. SKIP_DIRS mirrors check_privacy.py's build/venv/
VCS exclusions; RAW_BINARY_ALLOWLIST is an explicit, currently-empty escape
hatch for a future documented exception (add a path, never a glob, with a
comment justifying it).

Usage:
    python3 scripts/check_no_raw_binary.py --staged                  # pre-commit hook
    python3 scripts/check_no_raw_binary.py --tracked --root .        # CI, authoritative
    python3 scripts/check_no_raw_binary.py --tracked --max-bytes N   # tune the threshold
"""
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

# Directories never scanned (VCS internals, build/venv/test-cache artifacts).
# Mirrors check_privacy.py's SKIP_DIRS so the two guardrails agree on what a
# "real" repo path is (shared-directory-enumerator-drift).
SKIP_DIRS = {".git", ".venv", "venv", "__pycache__", ".pytest_cache", ".cache", ".mypy_cache", ".ruff_cache", "node_modules", "dist", "build"}
SKIP_SUFFIX_DIRS = (".egg-info",)

# Explicit escape hatch for a future documented exception — a specific
# repo-relative path string, never a glob. Empty today: no exception exists.
RAW_BINARY_ALLOWLIST: frozenset[str] = frozenset()

DEFAULT_MAX_BYTES = 5 * 1024 * 1024  # 5 MiB
SNIFF_BYTES = 8000  # git's own is-binary heuristic reads this many leading bytes


def in_skip_dir(rel_path: Path) -> bool:
    parts = set(rel_path.parts)
    if parts & SKIP_DIRS:
        return True
    return any(part.endswith(SKIP_SUFFIX_DIRS) for part in rel_path.parts)


def is_binary_content(path: Path) -> bool:
    """True if the file's leading bytes look binary: a NUL byte, or a chunk
    that fails UTF-8 decoding (git's is-binary heuristic, content-based)."""
    try:
        with path.open("rb") as f:
            prefix = f.read(SNIFF_BYTES)
    except OSError:
        return False  # unreadable — treated as not-binary, nothing to flag
    if b"\x00" in prefix:
        return True
    try:
        prefix.decode("utf-8")
    except UnicodeDecodeError:
        return True
    return False


def is_lfs_routed(root: Path, rel_path: Path) -> bool:
    """True if `git check-attr filter` reports `lfs` for this path."""
    try:
        result = subprocess.run(
            ["git", "check-attr", "filter", "--", str(rel_path)],
            cwd=root, capture_output=True, text=True, check=False,
        )
    except OSError:
        return False
    # Output line shape: "<path>: filter: <value>"
    return result.stdout.strip().endswith("filter: lfs")


def staged_candidates(root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "diff", "--cached", "--name-only", "--diff-filter=AM"],
        cwd=root, capture_output=True, text=True, check=True,
    )
    return [line for line in result.stdout.splitlines() if line]


def tracked_candidates(root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "ls-files"], cwd=root, capture_output=True, text=True, check=True,
    )
    return [line for line in result.stdout.splitlines() if line]


def scan(root: Path, candidates: list[str], max_bytes: int) -> list[str]:
    """Return a list of human-readable failure strings for candidate paths."""
    failures: list[str] = []
    for rel_str in candidates:
        rel_path = Path(rel_str)
        if rel_str in RAW_BINARY_ALLOWLIST:
            continue
        if in_skip_dir(rel_path):
            continue
        p = root / rel_path
        if not p.is_file():
            continue  # deleted / not on disk — nothing to inspect
        size = p.stat().st_size
        if size <= max_bytes:
            continue
        if not is_binary_content(p):
            continue
        if is_lfs_routed(root, rel_path):
            continue
        failures.append(f"{rel_path}: raw binary ({size} bytes, over {max_bytes}-byte threshold, not LFS-routed)")
    return failures


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="No-raw-binary guard: content-based, catches binaries no extension glob names.")
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--staged", action="store_true", help="Scan staged additions/modifications (default; for the pre-commit hook).")
    mode.add_argument("--tracked", action="store_true", help="Scan the full tracked tree (for CI; also catches already-committed raw binaries).")
    ap.add_argument("--root", default=None, help="Repo root to scan (default: parent of scripts/).")
    ap.add_argument("--max-bytes", type=int, default=DEFAULT_MAX_BYTES, help=f"Size threshold in bytes (default: {DEFAULT_MAX_BYTES}).")
    args = ap.parse_args(argv)

    self_path = Path(__file__).resolve()
    root = Path(args.root).resolve() if args.root else self_path.parent.parent

    candidates = tracked_candidates(root) if args.tracked else staged_candidates(root)
    failures = scan(root, candidates, args.max_bytes)

    mode_label = "tracked" if args.tracked else "staged"
    if failures:
        print(f"FAIL — {len(failures)} raw binary file(s) under {root} (mode={mode_label}):")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"OK — no raw binaries under {root} (mode={mode_label}, max-bytes={args.max_bytes}).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
