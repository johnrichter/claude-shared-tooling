"""Argument parsing and file-set resolution for the code-authoring linter.

Three ways to pick the file set to check, in order of intended use:

  --files PATH...   an explicit list (test fixtures, editor integrations).
  --diff REF        every file changed between REF and the working tree (the CI mode —
                     lints what a task actually touched, not the repo's full history).
  --all             every tracked source file (audits only; not wired into required CI,
                     since the existing tree predates this rule set and carries a real
                     remediation backlog these rules would otherwise block on).
"""
from __future__ import annotations

import argparse
import subprocess
import sys
import tempfile
from pathlib import Path

from . import pydocs, scan
from .rules import Violation


def _repo_root() -> Path:
    out = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True
    )
    return Path(out.stdout.strip())


def _diff_files(repo_root: Path, ref: str) -> list[Path]:
    out = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACMR", ref],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=True,
    )
    return [repo_root / line for line in out.stdout.splitlines() if line]


def _all_tracked_files(repo_root: Path) -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files"], cwd=repo_root, capture_output=True, text=True, check=True
    )
    return [repo_root / line for line in out.stdout.splitlines() if line]


def _print_report(violations: list[Violation]) -> None:
    for v in violations:
        print(f"{v.path}:{v.line}: {v.rule}: {v.detail}")
    print(f"{len(violations)} violation(s)" if violations else "no violations")


def main(argv: list[str] | None = None) -> int:
    """Entry point: resolve the file set, run every rule, report, and return the exit code."""
    parser = argparse.ArgumentParser(description="SC-CODEGOV code-authoring lint")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--files", nargs="+", metavar="PATH", help="explicit file list")
    group.add_argument("--diff", metavar="REF", help="lint files changed since REF")
    group.add_argument("--all", action="store_true", help="lint every tracked source file")
    args = parser.parse_args(argv)

    try:
        repo_root = _repo_root()
    except subprocess.CalledProcessError:
        print("codegov-lint: not inside a git repository", file=sys.stderr)
        return 2

    if args.files:
        paths = [Path(f).resolve() for f in args.files]
    elif args.diff:
        paths = _diff_files(repo_root, args.diff)
    else:
        paths = _all_tracked_files(repo_root)
    paths = [p for p in paths if p.is_file()]

    violations = scan.scan_files(paths, repo_root)

    if pydocs.ruff_available():
        with tempfile.TemporaryDirectory() as tmp:
            violations.extend(pydocs.scan_python_docstrings(paths, repo_root, Path(tmp) / "ruff.toml"))
        violations.sort(key=lambda v: (v.path, v.line, v.rule))
    else:
        print("codegov-lint: ruff not found on PATH, skipping Python docstring check", file=sys.stderr)

    _print_report(violations)
    return 1 if violations else 0
