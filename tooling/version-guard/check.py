#!/usr/bin/env python3
"""version-guard — SC-VERSIONING enforcement: tag-prefix/module-path parity, and Rust
git-tag-only cross-module dependencies. Stdlib-only, no install: run it from a checkout.

Commands:
    check-tag   Reject a tag whose path prefix isn't a real module path in this tree.
    check-deps  Reject any Rust `path = ...` dependency; only a git-tag dependency is allowed.
    commands    Print the exact tag-and-release command set for a module cut.

Exit codes: 0 the tag/dependency set conforms (or the commands were printed); 1 a
violation was found, or a manifest could not be read (never a silent pass); 2 usage
error (bad arguments).

Usage:
    python3 tooling/version-guard/check.py check-tag go/git/v1.2.0
    python3 tooling/version-guard/check.py check-deps
    python3 tooling/version-guard/check.py commands --module go/git --version 1.2.0
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from version_guard import commands as commands_mod
from version_guard.deps import DepsError, scan_repo
from version_guard.tag import TagError, check_tag

_VIOLATION = 1
_USAGE_ERROR = 2


def main(argv: list[str] | None = None) -> int:
    """Run the guard.

    Args:
        argv: Command-line arguments (default: `sys.argv[1:]`).

    Returns:
        The process exit code.
    """
    parser = _parser()
    args = parser.parse_args(argv)
    try:
        return args.handler(args)
    except (TagError, DepsError) as exc:
        print(f"version-guard: {exc}", file=sys.stderr)
        return _VIOLATION


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="version-guard", description="Enforce SC-VERSIONING.")
    subcommands = parser.add_subparsers(dest="command", required=True)

    check_tag_cmd = subcommands.add_parser("check-tag", help="Reject a tag whose prefix isn't a real module path.")
    check_tag_cmd.add_argument("tag", help="The tag to check, e.g. go/git/v1.2.0 or v0.2.2.")
    check_tag_cmd.add_argument("--repo-root", type=Path, default=Path.cwd(), help="Repository root (default: cwd).")
    check_tag_cmd.set_defaults(handler=_check_tag)

    check_deps_cmd = subcommands.add_parser("check-deps", help="Reject any Rust path/relative dependency.")
    check_deps_cmd.add_argument("--repo-root", type=Path, default=Path.cwd(), help="Repository root (default: cwd).")
    check_deps_cmd.set_defaults(handler=_check_deps)

    commands_cmd = subcommands.add_parser("commands", help="Print the exact tag-and-release command set.")
    commands_cmd.add_argument("--module", default="", help="Module path, empty for the top-level module (default: '').")
    commands_cmd.add_argument("--version", required=True, help="Version being released, e.g. 1.2.0.")
    commands_cmd.add_argument("--commit", default="HEAD", help="Commit-ish to tag (default: HEAD).")
    commands_cmd.set_defaults(handler=_commands)

    return parser


def _check_tag(args: argparse.Namespace) -> int:
    check_tag(args.repo_root.resolve(), args.tag)
    print(f"version-guard: {args.tag!r} conforms to SC-VERSIONING")
    return 0


def _check_deps(args: argparse.Namespace) -> int:
    violations = scan_repo(args.repo_root.resolve())
    if violations:
        for violation in violations:
            print(f"version-guard: {violation}", file=sys.stderr)
        print(f"version-guard: {len(violations)} Rust path/relative dependency(ies) found; require a git-tag dependency instead", file=sys.stderr)
        return _VIOLATION
    print("version-guard: no Rust path/relative dependencies found")
    return 0


def _commands(args: argparse.Namespace) -> int:
    plan = commands_mod.render(args.module, args.version, commit=args.commit)
    for command in plan.commands:
        print(command)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
