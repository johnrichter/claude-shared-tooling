"""The allowlist producer — single source of record for dist-guard's permitted exceptions.

Edit ENTRIES here, never `allowlist.json` directly, then run `check.py generate` to re-render
it. `check.py check` regenerates in memory and diffs against disk, so a hand-edit or a source
change landed without regenerating both fail the same way.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import TypedDict


class Entry(TypedDict):
    path: str
    reason: str


# At most one entry, enforced structurally (see `_assert_at_most_one`) rather than left to
# review discipline alone: SC-DISTRIBUTION permits at most one documented committed-binary
# exception, never a growable list. The steady state is zero — no committed binary at all.
ENTRIES: list[Entry] = []

_GENERATED_BY = "tooling/dist-guard/dist_guard/allowlist.py"


def _assert_at_most_one(entries: list[Entry]) -> None:
    if len(entries) > 1:
        raise ValueError(
            f"dist-guard allowlist must carry at most one entry, found {len(entries)} — "
            "SC-DISTRIBUTION permits a single documented exception at most, not a growable list"
        )


def render() -> str:
    """Deterministic JSON rendering of ENTRIES — the file `generate` writes to disk."""
    _assert_at_most_one(ENTRIES)
    doc = {
        "generated_by": f"{_GENERATED_BY} — do not hand-edit; run `python3 tooling/dist-guard/check.py generate`",
        "entries": ENTRIES,
    }
    return json.dumps(doc, indent=2, sort_keys=False) + "\n"


def load(path: Path) -> frozenset[str]:
    """Paths permitted to carry a committed binary, read from the on-disk allowlist."""
    doc = json.loads(path.read_text(encoding="utf-8"))
    entries = doc.get("entries", [])
    return frozenset(e["path"] for e in entries)
