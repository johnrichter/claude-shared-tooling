#!/usr/bin/env python3
"""Security guardrail — stdlib-only, zero setup.

Scans committed files for accidentally-committed plaintext secrets: private
keys, cloud/API access-key ids, and chat/VCS-host tokens. Fails the build on
any high-confidence match, scanned across the FULL text of EVERY non-skipped
file, unconditionally.

Usage:
    python3 scripts/check_secrets.py            # scan repo root (exit 1 on any match)
    python3 scripts/check_secrets.py --root P   # scan a different root
"""
from __future__ import annotations

import argparse
import re
from pathlib import Path

# Directories never scanned (VCS internals, build/venv artifacts).
# ".git-worktrees" mirrors the .gitignore entry — never scan transient worktree checkouts.
SKIP_DIRS = {".git", ".git-worktrees", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", "dist", "build", "target"}
SKIP_SUFFIX_DIRS = (".egg-info",)

# Binary/asset extensions we never text-scan.
BINARY_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".gz", ".whl", ".pyc", ".ico", ".woff", ".woff2"}

# High-confidence plaintext secret patterns.
SECRET_PATTERNS = [
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), "private key block"),
    (re.compile(r"\bAKIA[0-9A-Z]{16}\b"), "AWS access-key id"),
    (re.compile(r"\bxox[baprs]-[0-9A-Za-z-]{10,}"), "Slack token"),
    (re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,}"), "GitHub token"),
]


def iter_files(root: Path, self_path: Path):
    """Yield scannable files under root, skipping VCS/build dirs and this checker."""
    for p in sorted(root.rglob("*")):
        if not p.is_file():
            continue
        if p.resolve() == self_path:
            continue  # the checker holds the pattern literals by design
        parts = set(p.relative_to(root).parts)
        if parts & SKIP_DIRS or any(part.endswith(SKIP_SUFFIX_DIRS) for part in parts):
            continue
        if p.suffix.lower() in BINARY_SUFFIXES:
            continue
        yield p


def scan(root: Path, self_path: Path) -> list[str]:
    """Return failures as human-readable strings."""
    failures: list[str] = []
    for p in iter_files(root, self_path):
        rel = p.relative_to(root)
        try:
            text = p.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue  # unreadable/binary — nothing to leak in text form
        for rx, label in SECRET_PATTERNS:
            if rx.search(text):
                failures.append(f"{rel}: possible {label}")
    return failures


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Secret-scanner guardrail (stdlib-only).")
    ap.add_argument("--root", default=None, help="Repo root to scan (default: parent of scripts/).")
    args = ap.parse_args(argv)

    self_path = Path(__file__).resolve()
    root = Path(args.root).resolve() if args.root else self_path.parent.parent

    failures = scan(root, self_path)

    if failures:
        print(f"FAIL — {len(failures)} possible secret(s) under {root}:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"OK — no secrets found under {root}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
