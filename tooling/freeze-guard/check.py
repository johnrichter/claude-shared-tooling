#!/usr/bin/env python3
"""SC-FREEZE guardrail — deny writes to frozen plugin/corpus homes.

Fails CI if any diff touches a path under a frozen datadog-docs-agent or
datadog-code-agent plugin home or corpus. Passes for writes to Toolbelt
build homes.

Frozen paths are a single source of record in frozen-homes.json, re-read every
run (no cached state). Drift of that list against the documented spec is covered
by tests/test_freeze_guard.py, not by this guard at runtime.

Usage:
    python3 tooling/freeze-guard/check.py --base <ref>   # diff <ref>..HEAD (CI/pre-merge)
    python3 tooling/freeze-guard/check.py                # working-tree mode (local)
    python3 tooling/freeze-guard/check.py --list         # list frozen paths

In CI the changed surface is a committed PR: pass --base with the base ref
(e.g. the PR base SHA) so the guard diffs base..HEAD. Without --base only the
uncommitted working tree is inspected, which is empty in a clean CI checkout —
use that mode for local pre-commit checks only.
"""
from __future__ import annotations

import argparse
import json
import subprocess
from pathlib import Path


def load_frozen_homes(manifest_path: Path) -> list[str]:
    """Load frozen home list from frozen-homes.json."""
    try:
        with manifest_path.open("r", encoding="utf-8") as f:
            data = json.load(f)
        return data.get("frozen_homes", [])
    except (FileNotFoundError, json.JSONDecodeError) as e:
        raise RuntimeError(f"Failed to load frozen-homes.json: {e}") from e


def _git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )


def get_changed_files(repo_root: Path, base_ref: str | None = None) -> list[str]:
    """Get the changed-file surface as paths relative to repo root.

    With base_ref: the committed diff base_ref..HEAD (the PR surface in CI).
    Always unions the uncommitted working tree (staged, unstaged, untracked) so
    local runs and dirty checkouts are also covered.
    """
    try:
        changed: set[str] = set()

        if base_ref:
            # Merge-base diff (three-dot): files changed on HEAD since it forked
            # from base_ref — matches GitHub's PR "Files changed" surface.
            result = _git(repo_root, "diff", "--name-only", f"{base_ref}...HEAD")
            if result.returncode != 0:
                # No reachable merge base (e.g. shallow/force-push): fall back to
                # a direct two-dot diff so the guard still inspects the surface.
                result = _git(repo_root, "diff", "--name-only", base_ref, "HEAD")
            if result.returncode != 0:
                raise RuntimeError(
                    f"git diff against base {base_ref!r} failed: {result.stderr.strip()}"
                )
            if result.stdout.strip():
                changed.update(result.stdout.strip().split("\n"))

        for args in (
            ("diff", "--name-only", "--cached"),  # staged
            ("diff", "--name-only"),  # unstaged
            ("ls-files", "--others", "--exclude-standard"),  # untracked
        ):
            result = _git(repo_root, *args)
            if result.returncode == 0 and result.stdout.strip():
                changed.update(result.stdout.strip().split("\n"))

        return sorted(f for f in changed if f)
    except RuntimeError:
        raise
    except Exception as e:
        raise RuntimeError(f"Failed to get changed files from git: {e}") from e


def is_under_frozen_home(path: str, frozen_homes: list[str]) -> bool:
    """Check if a path is under any frozen home directory.

    Uses path-prefix matching with directory boundary awareness:
    a path is frozen if it starts with a frozen home + path separator, or equals a frozen home.
    """
    # Normalize path to use forward slashes.
    norm_path = path.replace("\\", "/")

    for home in frozen_homes:
        # Check exact match.
        if norm_path == home:
            return True
        # Check if path is under this home (must have path separator after home dir).
        if norm_path.startswith(home + "/"):
            return True

    return False


def check_freeze(
    repo_root: Path, manifest_path: Path, base_ref: str | None = None
) -> tuple[bool, list[str]]:
    """Check if any changed files touch frozen homes.

    Returns (success, violations) where violations is a list of violating paths.
    """
    frozen_homes = load_frozen_homes(manifest_path)
    changed_files = get_changed_files(repo_root, base_ref)

    violations = []
    for changed_file in changed_files:
        if is_under_frozen_home(changed_file, frozen_homes):
            violations.append(changed_file)

    return len(violations) == 0, violations


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        description="SC-FREEZE guardrail — deny writes to frozen plugin/corpus homes."
    )
    ap.add_argument(
        "--root",
        default=None,
        help="Repo root (default: parent of this script's parent dir).",
    )
    ap.add_argument(
        "--base",
        default=None,
        help="Base ref/SHA to diff HEAD against (the committed PR surface in CI).",
    )
    ap.add_argument(
        "--list",
        action="store_true",
        help="List frozen paths and exit.",
    )
    args = ap.parse_args(argv)

    self_path = Path(__file__).resolve()
    tooling_dir = self_path.parent
    repo_root = Path(args.root).resolve() if args.root else tooling_dir.parent.parent

    manifest_path = tooling_dir / "frozen-homes.json"

    if args.list:
        frozen_paths = load_frozen_homes(manifest_path)
        print("Frozen paths (from frozen-homes.json):")
        for p in frozen_paths:
            print(f"  - {p}")
        return 0

    success, violations = check_freeze(repo_root, manifest_path, args.base)

    scope = f"base {args.base}..HEAD" if args.base else "working tree"

    if success:
        print(f"OK — no changes to frozen homes ({scope}).")
        return 0

    print(
        f"FAIL — {len(violations)} file(s) under frozen homes (SC-FREEZE, {scope}):\n"
        f"Frozen-homes list (single source): {manifest_path.name}"
    )
    for v in violations:
        print(f"  - {v}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
