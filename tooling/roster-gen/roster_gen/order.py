"""Ordering rules for roster-derived outputs.

Most outputs order by `capability_order()`, fully derived from the roster's own
`family`/`generation`/`cross_family_rank` fields (mirrors the Go self-check's
`modelRank`, strongest-first).

Two outputs — the plan-schema model enum and the model-gate allowlist — are
historically hand-curated lists with no roster-field formula that reproduces
both simultaneously (their relative orderings of shared IDs conflict under
every derivable sort). `PLAN_ENUM_ORDER` and `GATE_ALLOWLIST_ORDER` pin those
two orders as generator config rather than roster data. `sequence()` renders
a roster's ID set against a fixed order table, appending any ID the table
doesn't mention (a new roster row is placed, never dropped).
"""
from __future__ import annotations

from typing import Any

PLAN_ENUM_ORDER: list[str] = [
    "claude-opus-5",
    "claude-opus-4-8",
    "claude-opus-4-7",
    "claude-opus-4-6",
    "claude-sonnet-5",
    "claude-sonnet-4-6",
    "claude-haiku-4-5",
    "claude-fable-5",
]

GATE_ALLOWLIST_ORDER: list[str] = [
    "claude-fable-5",
    "claude-opus-5",
    "claude-opus-4-8",
    "claude-opus-4-7",
    "claude-opus-4-6",
    "claude-sonnet-5",
    "claude-haiku-4-5",
    "claude-opus-4-5",
    "claude-sonnet-4-6",
    "claude-sonnet-4-5",
]


def sequence(ids: list[str], fixed_order: list[str]) -> list[str]:
    """`ids` rendered in `fixed_order`, with any id `fixed_order` omits appended
    (stably, in `ids`' own order) rather than dropped."""
    id_set = set(ids)
    head = [mid for mid in fixed_order if mid in id_set]
    tail = [mid for mid in ids if mid not in fixed_order]
    return head + tail


def capability_order(ids: list[str], models: dict[str, Any]) -> list[str]:
    """`ids` ranked strongest-first: families ranked by their strongest member's
    `cross_family_rank` (families with no ranked member sort after ranked ones,
    alphabetically among themselves), then within a family by `generation`
    descending (a prefix ranks below its extension)."""

    def family_of(mid: str) -> str:
        return models[mid]["family"]

    families = sorted(set(family_of(mid) for mid in ids))

    def family_rank(fam: str) -> int | None:
        ranks = [models[mid]["cross_family_rank"] for mid in ids if family_of(mid) == fam]
        ranks = [r for r in ranks if r is not None]
        return max(ranks) if ranks else None

    ranked = sorted((f for f in families if family_rank(f) is not None), key=family_rank, reverse=True)
    unranked = sorted(f for f in families if family_rank(f) is None)
    family_order = ranked + unranked

    out: list[str] = []
    for fam in family_order:
        members = [mid for mid in ids if family_of(mid) == fam]
        members.sort(key=lambda mid: models[mid]["generation"], reverse=True)
        out.extend(members)
    return out
