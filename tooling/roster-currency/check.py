#!/usr/bin/env python3
"""roster-currency — CI staleness gate for this repo's roster-derived artifacts.

schemas/model-roster/model-roster.json is the single source of record for the selectable
Claude model set; every artifact this repo owns that's derived from it (today: just
go/anthropic-specifications.json) is roster-gen output, never hand-edited. This script
regenerates each one from the roster at HEAD, using the tag already recorded in its own
generated-by header, and fails on any byte difference from what's committed. That catches
staleness from either direction:

  - a hand-edited artifact: its content no longer matches its own header's tag render.
  - a roster change landed without regeneration: the artifact's recorded tag still reads
    fine, but rendering the *current* roster at that tag no longer reproduces the artifact,
    because a row it projects changed underneath it.

The marketplace-side artifacts (plan-schema.json's model.enum, build-engine.workflow.js's
DEFAULT_RATES, the model-gate allowlist, the tiering doc's capability table) are this same
roster's derived output too, but they live in the marketplace repo and are currency-checked
by that repo's own CI job — this script covers only what's committed here.

Usage: python3 tooling/roster-currency/check.py
Exit codes: 0 every owned artifact matches its own recorded-tag render; 1 one or more
drifted, is missing, or carries no recognizable generated-by tag; 2 the roster itself
failed to load (missing, corrupt, unsupported schema version).
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
_ROSTER_GEN = _REPO_ROOT / "tooling" / "roster-gen"
sys.path.insert(0, str(_ROSTER_GEN))

from roster_gen.roster import RosterError, load as load_roster  # noqa: E402
from roster_gen.targets import TARGETS, resolve  # noqa: E402

_ROSTER_PATH = _REPO_ROOT / "schemas" / "model-roster" / "model-roster.json"

# Matches roster-gen's two on-disk tag phrasings: the JSON/doc "at tag {tag}" form, where
# json.dumps(ensure_ascii=True) escapes the trailing em dash to the literal backslash-u2014
# text (not a real em-dash byte), and the allowlist's "roster-gen:generated (tag={tag})"
# comment form.
_TAG_RE = re.compile(r"(?:at tag |roster-gen:generated \(tag=)(\S+?)(?:[ )]|\\u2014)")


def _recorded_tag(text: str) -> str:
    match = _TAG_RE.search(text)
    if not match:
        raise ValueError("no generated-by tag marker found in the on-disk artifact")
    return match.group(1)


def _render_at_recorded_tag(target, path: Path, roster: dict, have: str, tag: str) -> str:
    """Mirrors roster-gen's own kind dispatch (cli.py), scoped to what an ai-shared-lib-owned
    target can be today ("whole") plus the other two kinds for forward compatibility if this
    repo ever gains a "patch" or "allowlist" target of its own."""
    if target.kind == "whole":
        return target.render_fn(roster, tag)
    if target.kind == "allowlist":
        existing_ids = {ln.strip() for ln in have.splitlines() if ln.strip() and not ln.lstrip().startswith("#")}
        return target.render_fn(roster, tag, existing_ids=existing_ids)
    return target.render_fn(have, roster, tag)  # "patch"


def main() -> int:
    try:
        roster = load_roster(_ROSTER_PATH)
    except RosterError as exc:
        print(f"roster-currency: {exc}", file=sys.stderr)
        return 2

    own_targets = [t for t in TARGETS if t.repo == "ai-shared-lib"]
    roots = {"ai-shared-lib": _REPO_ROOT}
    failures: list[str] = []

    for target in own_targets:
        path = resolve(target, roots)
        if not path.exists():
            failures.append(f"{path}: missing")
            continue
        have = path.read_text(encoding="utf-8")
        try:
            tag = _recorded_tag(have)
        except ValueError as exc:
            failures.append(f"{path}: {exc}")
            continue
        want = _render_at_recorded_tag(target, path, roster, have, tag)
        if want != have:
            failures.append(f"{path}: drifted from the roster-derived rendering at its own recorded tag {tag!r}")

    for f in failures:
        print(f"roster-currency: {f}", file=sys.stderr)
    if failures:
        print(
            f"roster-currency: {len(failures)}/{len(own_targets)} ai-shared-lib roster-derived "
            "artifact(s) drifted or missing — regenerate with tooling/roster-gen/generate.py",
            file=sys.stderr,
        )
        return 1
    print(f"roster-currency: all {len(own_targets)} ai-shared-lib roster-derived artifact(s) current", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
