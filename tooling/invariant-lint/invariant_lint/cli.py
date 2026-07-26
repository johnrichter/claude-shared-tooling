"""Orchestration: load the registry, run rung-1 and rung-3 resolution, report, exit."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from . import paths, symbols, testrun

SELF_DIR = Path(__file__).resolve().parent
# tooling/invariant-lint/invariant_lint/cli.py -> this checkout's root.
SELF_ROOT = SELF_DIR.parent.parent.parent
REGISTRY_PATH = SELF_ROOT / "schemas" / "invariant-registry" / "invariant-registry.json"


class HarnessError(Exception):
    """The lint could not be run at all, as distinct from finding a violation."""


def load_registry(path: Path) -> dict:
    """Read the registry document, raising HarnessError when it cannot be parsed."""
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise HarnessError(f"{path}: {exc}") from exc


def check_rung_1(
    entries: list[dict], repos: dict[str, Path | None], self_root: Path
) -> tuple[list[str], list[str]]:
    """Resolve every shipped rung-1 entry's `fail_fast_symbol`. Returns (violations, unresolved)."""
    violations: list[str] = []
    unresolved: set[str] = set()
    for entry in sorted(
        (e for e in entries if e.get("rung") == 1 and e["status"] == "shipped"),
        key=lambda e: e["id"],
    ):
        qualified, _, symbol = entry["fail_fast_symbol"].rpartition(":")
        target = paths.resolve_path(repos, self_root, qualified)
        if target is None:
            unresolved.add(qualified.partition("/")[0])
            continue
        ok, reason = symbols.resolve_symbol(target, symbol)
        if not ok:
            violations.append(f"{entry['id']}: {reason}")
    return violations, sorted(unresolved)


def check_rung_3(
    entries: list[dict], repos: dict[str, Path | None], self_root: Path
) -> tuple[list[str], list[str]]:
    """Load and run every shipped rung-3 entry's `test_id`. Returns (violations, unresolved)."""
    violations: list[str] = []
    unresolved: set[str] = set()
    for entry in sorted(
        (e for e in entries if e.get("rung") == 3 and e["status"] == "shipped"),
        key=lambda e: e["id"],
    ):
        test_id = entry["test_id"]
        repo_name = test_id.partition("/")[0]
        repo_root = paths.repo_dir(repos, self_root, repo_name)
        if repo_root is None:
            unresolved.add(repo_name)
            continue
        try:
            ok, reason = testrun.check_test_id(test_id, repo_root)
        except testrun.TestIdMalformed:
            violations.append(f"{entry['id']}: test_id {test_id!r} does not parse")
            continue
        if not ok:
            violations.append(f"{entry['id']}: {reason}")
    return violations, sorted(unresolved)


def run(registry_path: Path, overrides: list[str], require_roots: bool) -> int:
    """Run both rung checks and print one line per violation. Returns the exit code."""
    registry = load_registry(registry_path)
    repos = paths.resolve_repos(SELF_ROOT, overrides)

    rung1_violations, rung1_unresolved = check_rung_1(registry["invariants"], repos, SELF_ROOT)
    rung3_violations, rung3_unresolved = check_rung_3(registry["invariants"], repos, SELF_ROOT)
    violations = rung1_violations + rung3_violations

    unresolved = sorted(set(rung1_unresolved) | set(rung3_unresolved))
    if unresolved:
        detail = ", ".join(unresolved)
        if require_roots:
            violations.append(f"checkout not present for: {detail} (--require-roots)")
        else:
            print(
                f"invariant-lint: NOT CHECKED - no checkout present for {detail}; "
                "its entries were skipped, not passed."
            )

    if violations:
        for message in violations:
            print(f"invariant-lint: {message}")
        print(f"FAIL - {len(violations)} violation(s).")
        return 1

    shipped = [e for e in registry["invariants"] if e["status"] == "shipped"]
    rung1_count = sum(1 for e in shipped if e["rung"] == 1)
    rung3_count = sum(1 for e in shipped if e["rung"] == 3)
    print(f"OK - {rung1_count} rung-1 symbol(s) resolved, {rung3_count} rung-3 test(s) run clean.")
    return 0


def main(argv: list[str] | None = None) -> int:
    """Parse arguments and run the lint."""
    parser = argparse.ArgumentParser(
        description="invariant-lint - rung-1 symbol resolution and rung-3 test-id resolution."
    )
    parser.add_argument("--registry", default=None, help="Registry document (default: alongside).")
    parser.add_argument(
        "--repo",
        action="append",
        default=[],
        metavar="NAME=PATH",
        help="Checkout for a repo named in a path (default: a sibling of this checkout).",
    )
    parser.add_argument(
        "--require-roots",
        action="store_true",
        help="Fail instead of reporting when a declared entry's repo is not present.",
    )
    args = parser.parse_args(argv)

    try:
        return run(
            Path(args.registry).resolve() if args.registry else REGISTRY_PATH,
            args.repo,
            args.require_roots,
        )
    except HarnessError as exc:
        print(f"invariant-lint: cannot run - {exc}", file=sys.stderr)
        return 2
