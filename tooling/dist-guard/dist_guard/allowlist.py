"""The allowlist producer — single source of record for dist-guard's one permitted exception.

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


# Exactly one entry, enforced structurally (see `_assert_single`) rather than left to review
# discipline alone: SC-DISTRIBUTION scopes the darwin-arm64 last resort as a single documented
# path, not a pattern other exceptions can join.
ENTRIES: list[Entry] = [
    {
        "path": "go/.bin/build-helpers-darwin-arm64",
        "reason": (
            "Step-0 last resort (SC-DISTRIBUTION): no arm64-macOS CI runner existed yet to "
            "produce this binary through CD, so it was committed once to unblock a working "
            "pre-commit hook on an Apple-Silicon clone. Superseded once the SC-DISTRIBUTION "
            "CLI release template publishes per-OS/arch archives from a real CI runner; until "
            "then this is the only path a committed binary is allowed to occupy."
        ),
    },
]

_GENERATED_BY = "tooling/dist-guard/dist_guard/allowlist.py"


def _assert_single(entries: list[Entry]) -> None:
    if len(entries) != 1:
        raise ValueError(
            f"dist-guard allowlist must carry exactly one entry, found {len(entries)} — "
            "SC-DISTRIBUTION scopes the darwin-arm64 last resort as a single documented "
            "exception, not a growable list"
        )


def render() -> str:
    """Deterministic JSON rendering of ENTRIES — the file `generate` writes to disk."""
    _assert_single(ENTRIES)
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
