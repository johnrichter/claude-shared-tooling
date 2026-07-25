"""CLI: render every roster-derived output in one pass (`generate`), or diff
the current renderings against what's on disk without writing (`check`)."""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

from . import render
from .roster import RosterError, load as load_roster
from .targets import TARGETS, resolve


def _roots(args: argparse.Namespace) -> dict[str, Path]:
    return {"ai-shared-lib": args.ai_shared_lib_root, "marketplace": args.marketplace_root}


def _render_target(target, path: Path, roster: dict, tag: str) -> str:
    if target.kind == "whole":
        return target.render_fn(roster, tag)
    if target.kind == "allowlist":
        existing_ids = None
        if path.exists():
            existing_ids = {
                ln.strip()
                for ln in path.read_text(encoding="utf-8").splitlines()
                if ln.strip() and not ln.lstrip().startswith("#")
            }
        return target.render_fn(roster, tag, existing_ids=existing_ids)
    # "patch"
    if not path.exists():
        raise FileNotFoundError(f"{path} does not exist — patch targets must already exist")
    return target.render_fn(path.read_text(encoding="utf-8"), roster, tag)


def generate(args: argparse.Namespace) -> int:
    """Render every target before writing any of them. A failure partway through
    rendering (e.g. the gate-allowlist narrowing guard) must not leave a
    partial write behind — a mix of regenerated and stale outputs is itself a
    drift, exactly what this generator exists to prevent."""
    roster = load_roster(args.roster)
    roots = _roots(args)
    rendered = []
    for target in TARGETS:
        path = resolve(target, roots)
        content = _render_target(target, path, roster, args.tag)
        rendered.append((path, content))
    for path, content in rendered:
        path.write_text(content, encoding="utf-8")
        print(f"roster-gen: wrote {path}", file=sys.stderr)
    return 0


def check(args: argparse.Namespace) -> int:
    roster = load_roster(args.roster)
    roots = _roots(args)
    failures = []
    for target in TARGETS:
        path = resolve(target, roots)
        if not path.exists():
            failures.append(f"{path}: missing")
            continue
        want = _render_target(target, path, roster, args.tag)
        have = path.read_text(encoding="utf-8")
        if want != have:
            failures.append(f"{path}: drifted from the roster-derived rendering")
    for f in failures:
        print(f"roster-gen: {f}", file=sys.stderr)
    if failures:
        print(f"roster-gen: {len(failures)}/{len(TARGETS)} target(s) drifted or missing", file=sys.stderr)
        return 1
    print(f"roster-gen: all {len(TARGETS)} targets match the roster-derived rendering", file=sys.stderr)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="roster-gen",
        description="Render every roster-derived output from the model roster, in one deterministic pass.",
    )
    parser.add_argument("--roster", type=Path, required=True, help="Path to model-roster.json, checked out at the pinned tag.")
    parser.add_argument("--tag", required=True, help="Tag/ref the roster was resolved at — named in every output's generated-by header.")
    parser.add_argument("--ai-shared-lib-root", type=Path, required=True, help="ai-shared-lib repo root.")
    parser.add_argument("--marketplace-root", type=Path, required=True, help="marketplace repo root.")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("generate", help="Render and write every target.").set_defaults(handler=generate)
    sub.add_parser(
        "check", help="Render every target in memory and diff against disk; exit 1 on drift or an absent target."
    ).set_defaults(handler=check)
    args = parser.parse_args(argv)
    try:
        return args.handler(args)
    except (RosterError, render.NarrowingError, ValueError, FileNotFoundError) as exc:
        print(f"roster-gen: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
