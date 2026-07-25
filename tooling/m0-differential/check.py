#!/usr/bin/env python3
"""m0-differential — structural CLI-surface gate for go/build-helpers.

Builds the pre-M0 artifact and the working tree's build-helpers side by side, runs the same probe
corpus through both, and diffs what a caller can actually observe: argv flag grammar, stdin
handling, stdout JSON field names/types/presence, and exit codes. Free text — error messages,
reason strings, flag and help descriptions — is excluded by construction, so a reword can never
fail this gate and a structural change always does. There is no declared-delta list: any structural
divergence is a pause, to be judged by a human, never waved through by a file in the tree.

Usage:
  python3 tooling/m0-differential/check.py [--pre-ref REF] [--record]
  python3 tooling/m0-differential/check.py --selftest
  python3 tooling/m0-differential/check.py --rollback DIR

Exit codes: 0 no structural divergence (or the selftest/rollback succeeded); 1 a structural
divergence was found (forces a pause); 2 the harness itself could not run.
"""
from __future__ import annotations

import argparse
import json
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from m0_differential import capture, checkout, plants, probes, report  # noqa: E402
from m0_differential.diff import diff  # noqa: E402

EVIDENCE_SUBPATH = "go/build-helpers/testdata/post-m0-differential"


def _build_pair(root: Path, pre_ref: str, scratch: Path) -> tuple[Path, Path, str]:
    pre_tree = checkout.export(pre_ref, scratch / "pre-m0")
    pre_bin = checkout.build(pre_tree, scratch / "build-helpers-pre-m0")
    help_digest = checkout.verify_pre_m0_identity(pre_tree, pre_bin)

    post_tree = checkout.export_worktree(scratch / "post-m0")
    post_bin = checkout.build(post_tree, scratch / "build-helpers-post-m0")
    return pre_bin, post_bin, help_digest


def run_differential(root: Path, pre_ref: str, record: bool) -> int:
    missing, unknown = probes.coverage_gaps(root)
    if missing or unknown:
        print(f"m0-differential: probe corpus does not match the baseline roster: "
              f"unprobed={missing} not-in-baseline={unknown}", file=sys.stderr)
        return 2

    fixtures = root / probes.FIXTURES_SUBPATH
    scratch = Path(tempfile.mkdtemp(prefix="m0-differential-"))
    try:
        pre_bin, post_bin, help_digest = _build_pair(root, pre_ref, scratch)
        pre_surface = capture.capture(pre_bin, fixtures)
        post_surface = capture.capture(post_bin, fixtures)
    except checkout.CheckoutError as exc:
        print(f"m0-differential: {exc}", file=sys.stderr)
        return 2
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

    divergences = diff(pre_surface, post_surface)
    text = report.render(divergences, pre_ref, len(post_surface["commands"]), len(probes.PROBES))

    if record:
        evidence = root / EVIDENCE_SUBPATH
        _write_json(evidence / "structural-surface-pre-m0.json", pre_surface)
        _write_json(evidence / "structural-surface-post-m0.json", post_surface)
        (evidence / "divergence-report.md").write_text(text, encoding="utf-8")

    print(text)
    print(f"m0-differential: pre-M0 artifact {pre_ref} verified (help capture sha256 {help_digest[:16]})",
          file=sys.stderr)
    if divergences:
        print(f"m0-differential: {report.summary(divergences)} — PAUSE: a human must judge every line above",
              file=sys.stderr)
        return 1
    print("m0-differential: structural surface unchanged", file=sys.stderr)
    return 0


def _write_json(path: Path, payload: capture.Surface) -> None:
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def run_rollback(root: Path, pre_ref: str, dest: Path) -> int:
    """Recover the pre-M0 artifact into dest and prove it is the real thing."""
    try:
        tree = checkout.export(pre_ref, dest)
        binary = checkout.build(tree, dest / "build-helpers")
        digest = checkout.verify_pre_m0_identity(tree, binary)
    except checkout.CheckoutError as exc:
        print(f"m0-differential: rollback failed: {exc}", file=sys.stderr)
        return 2
    print(f"m0-differential: pre-M0 artifact recovered at {binary} from {pre_ref} "
          f"(help capture sha256 {digest[:16]})", file=sys.stderr)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--pre-ref", default=checkout.PRE_M0_REF, help="the pre-M0 artifact's commit")
    parser.add_argument("--record", action="store_true", help="rewrite the committed evidence artifacts")
    parser.add_argument("--selftest", action="store_true",
                        help="prove the gate catches every class of structural change and no prose change")
    parser.add_argument("--rollback", metavar="DIR", type=Path,
                        help="recover the pre-M0 artifact into DIR (must not exist) and verify it")
    args = parser.parse_args()

    root = checkout.repo_root()
    if args.rollback:
        return run_rollback(root, args.pre_ref, args.rollback)
    if args.selftest:
        return plants.run_selftest(root)
    return run_differential(root, args.pre_ref, args.record)


if __name__ == "__main__":
    raise SystemExit(main())
