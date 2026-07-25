"""Planted changes that prove what the gate does and does not react to.

Each plant is a real edit to a real copy of the source, compiled and probed like any other build.
The structural plants cover every dimension the gate claims to protect; the prose plants reword
text a human reads and nothing else. A structural plant that produces no finding, or a prose plant
that produces one, is a defect in the gate, not in the plant.
"""
from __future__ import annotations

import shutil
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

from . import capture, checkout, probes
from .diff import diff

_MAIN = "go/build-helpers/main.go"
_PLAN = "go/build-helpers/bh/plan.go"
_ACCOUNTING = "go/build-helpers/bh/accounting.go"
_SELFCHECK = "go/build-helpers/bh/selfcheck.go"


@dataclass(frozen=True)
class Plant:
    id: str
    structural: bool
    edits: list[tuple[str, str, str]]
    expect_kinds: tuple[str, ...] = ()


PLANTS: list[Plant] = [
    Plant(
        "stdout-field-removed", True,
        [(_PLAN, '\tWarnings []string `json:"warnings"`', '\tWarnings []string `json:"-"`')],
        ("stdout_field_removed",),
    ),
    Plant(
        "stdout-field-renamed", True,
        [(_PLAN, '\tOK       bool     `json:"ok"`', '\tOK       bool     `json:"valid"`')],
        ("stdout_field_removed", "stdout_field_added"),
    ),
    Plant(
        "stdout-field-retyped", True,
        [(_ACCOUNTING, '\tTurns        int64 `json:"turns"`', '\tTurns        int64 `json:"turns,string"`')],
        ("stdout_field_retyped",),
    ),
    Plant(
        "exit-code-changed", True,
        [(_MAIN,
          '\t\tres := bh.ValidatePlanBytes(readFile(arg(rest, 0, "validate <plan.json>")))\n'
          '\t\tprintJSON(res)\n\t\texitOK(res.OK)',
          '\t\tres := bh.ValidatePlanBytes(readFile(arg(rest, 0, "validate <plan.json>")))\n'
          '\t\tprintJSON(res)\n\t\tif !res.OK {\n\t\t\tos.Exit(7)\n\t\t}')],
        ("exit_code_changed",),
    ),
    Plant(
        "argv-flag-renamed", True,
        [(_MAIN, 'fs.StringVar(&o.Slug, "slug", "", "project slug (required)")',
          'fs.StringVar(&o.Slug, "project-slug", "", "project slug (required)")')],
        ("flag_removed", "flag_added"),
    ),
    Plant(
        "argv-flag-requiredness-changed", True,
        [(_MAIN, '  build-helpers batch          <execution.json> <plan.json> [--max N]',
          '  build-helpers batch          <execution.json> <plan.json> --max N')],
        ("flag_requiredness_changed",),
    ),
    Plant(
        "prose-error-message-reworded", False,
        [(_MAIN, 'die(2, "escalate: --condition is required\\n")',
          'die(2, "escalate: you must supply --condition NAME\\n")')],
    ),
    Plant(
        "prose-help-text-reworded", False,
        [(_MAIN, 'true tokens (whole session incl. subagents)',
          'real token counts for the entire session, subagents included')],
    ),
    Plant(
        "prose-reason-string-reworded", False,
        [(_SELFCHECK, '"within band"', '"inside the requested band"')],
    ),
]


def _capture_planted(root: Path, plant: Plant, fixtures: Path) -> dict:
    scratch = Path(tempfile.mkdtemp(prefix=f"m0-plant-{plant.id}-"))
    try:
        tree = checkout.export_worktree(scratch / "tree")
        checkout.patch(tree, plant.edits)
        binary = checkout.build(tree, scratch / "build-helpers")
        return capture.capture(binary, fixtures)
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def run_selftest(root: Path) -> int:
    """Run every plant against an unplanted build of the working tree."""
    fixtures = root / probes.FIXTURES_SUBPATH
    scratch = Path(tempfile.mkdtemp(prefix="m0-plant-control-"))
    try:
        tree = checkout.export_worktree(scratch / "tree")
        control = capture.capture(checkout.build(tree, scratch / "build-helpers"), fixtures)
    except checkout.CheckoutError as exc:
        print(f"m0-differential: selftest could not build the control: {exc}", file=sys.stderr)
        return 2
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

    failures: list[str] = []
    for plant in PLANTS:
        try:
            planted = _capture_planted(root, plant, fixtures)
        except checkout.CheckoutError as exc:
            failures.append(f"{plant.id}: could not be planted: {exc}")
            continue

        found = diff(control, planted)
        kinds = {d.kind for d in found}
        if plant.structural:
            unmet = [k for k in plant.expect_kinds if k not in kinds]
            verdict = "caught" if found and not unmet else "MISSED"
            if unmet or not found:
                failures.append(f"{plant.id}: structural plant not caught (missing {unmet or 'any finding'})")
        else:
            verdict = "ignored" if not found else "FALSE POSITIVE"
            if found:
                failures.append(f"{plant.id}: prose-only plant produced {len(found)} finding(s): "
                                + "; ".join(d.line() for d in found[:3]))
        print(f"  {plant.id:38s} {'structural' if plant.structural else 'prose-only':11s} "
              f"{verdict:15s} {len(found)} finding(s)", file=sys.stderr)

    for failure in failures:
        print(f"m0-differential: {failure}", file=sys.stderr)
    if failures:
        print(f"m0-differential: selftest FAILED ({len(failures)}/{len(PLANTS)} plants)", file=sys.stderr)
        return 1
    print(f"m0-differential: selftest passed — {sum(p.structural for p in PLANTS)} structural plants caught, "
          f"{sum(not p.structural for p in PLANTS)} prose-only plants ignored", file=sys.stderr)
    return 0
