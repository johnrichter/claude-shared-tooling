#!/usr/bin/env python3
"""dist-guard — the SC-DISTRIBUTION no-committed-binaries guard. Stdlib-only, no install.

CD, not the git tree, is the distribution channel (see the release-*.yml templates in
.github/workflows/): a library release pins a tag with no archive, a CLI release publishes
per-OS/arch archives + checksums. `scan` denies any committed binary outside the one
documented, drift-checked exception in `dist_guard/allowlist.py`.

Commands:
    scan      Fail if any tracked, executable-mode file is binary content and not allowlisted.
    generate  Render allowlist.json from its producer (dist_guard/allowlist.py) and write it.
    check     Render allowlist.json in memory and diff against disk; exit 1 on drift or a
              missing file. Use in CI as the currency gate for the allowlist itself.

Usage:
    python3 tooling/dist-guard/check.py scan [--root .]
    python3 tooling/dist-guard/check.py generate [--root .]
    python3 tooling/dist-guard/check.py check [--root .]

Exit codes: 0 clean/current; 1 a committed binary is not allowlisted, or the allowlist has
drifted from its producer / is missing; 2 usage or allowlist-source error (e.g. more than one
entry in `dist_guard.allowlist.ENTRIES`).
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from dist_guard import allowlist, scanner  # noqa: E402

_ALLOWLIST_PATH = Path(__file__).resolve().parent / "allowlist.json"


def cmd_scan(root: Path) -> int:
    try:
        permitted = allowlist.load(_ALLOWLIST_PATH)
    except (OSError, ValueError, KeyError) as exc:
        print(f"dist-guard: cannot load allowlist {_ALLOWLIST_PATH}: {exc}", file=sys.stderr)
        return 2
    violations = scanner.scan(root, permitted)
    if violations:
        print(f"dist-guard: FAIL — {len(violations)} committed binary(ies) outside the SC-DISTRIBUTION allowlist:", file=sys.stderr)
        for v in violations:
            print(f"  - {v}", file=sys.stderr)
        print("dist-guard: distribute built artifacts through CD (release-*.yml), not the git tree", file=sys.stderr)
        return 1
    print(f"dist-guard: OK — no committed binary outside the allowlist ({len(permitted)} entry allowed) under {root}", file=sys.stderr)
    return 0


def cmd_generate(root: Path) -> int:
    try:
        rendered = allowlist.render()
    except ValueError as exc:
        print(f"dist-guard: {exc}", file=sys.stderr)
        return 2
    (root / "tooling" / "dist-guard" / "allowlist.json").write_text(rendered, encoding="utf-8")
    print(f"dist-guard: wrote {_ALLOWLIST_PATH}", file=sys.stderr)
    return 0


def cmd_check(root: Path) -> int:
    try:
        want = allowlist.render()
    except ValueError as exc:
        print(f"dist-guard: {exc}", file=sys.stderr)
        return 2
    if not _ALLOWLIST_PATH.exists():
        print(f"dist-guard: {_ALLOWLIST_PATH} is missing — run `generate`", file=sys.stderr)
        return 1
    have = _ALLOWLIST_PATH.read_text(encoding="utf-8")
    if have != want:
        print(f"dist-guard: {_ALLOWLIST_PATH} drifted from its producer (dist_guard/allowlist.py) — run `generate`", file=sys.stderr)
        return 1
    print("dist-guard: allowlist.json is current with its producer", file=sys.stderr)
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="SC-DISTRIBUTION no-committed-binaries guard.")
    ap.add_argument("command", choices=["scan", "generate", "check"])
    ap.add_argument("--root", default=".", help="Repo root to operate on (default: cwd).")
    args = ap.parse_args(argv)

    root = Path(args.root).resolve()
    if args.command == "scan":
        return cmd_scan(root)
    if args.command == "generate":
        return cmd_generate(root)
    return cmd_check(root)


if __name__ == "__main__":
    raise SystemExit(main())
