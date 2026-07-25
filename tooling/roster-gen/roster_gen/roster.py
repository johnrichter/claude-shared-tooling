"""Load and lightly validate the model-roster document — this generator's only input."""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Callable

SUPPORTED_SCHEMA_VERSION = 1


class RosterError(Exception):
    """The roster document is missing, unreadable, or declares an unsupported schema version."""


def load(path: Path) -> dict[str, Any]:
    """Parse and minimally validate the roster at `path`.

    Refuses (rather than guesses) a document declaring a schema version newer than this
    generator was built against — the roster's own forward-refusal rule for its
    `_schema_version` (a MAJOR version: any breaking reshape bumps it).
    """
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise RosterError(f"cannot read roster at {path}: {exc}") from exc
    try:
        doc = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RosterError(f"roster at {path} is not valid JSON: {exc}") from exc
    version = doc.get("_schema_version")
    if not isinstance(version, int) or version > SUPPORTED_SCHEMA_VERSION:
        raise RosterError(
            f"roster at {path} declares _schema_version {version!r}; "
            f"this generator supports up to {SUPPORTED_SCHEMA_VERSION}"
        )
    if not doc.get("models"):
        raise RosterError(f"roster at {path} has no models")
    if not doc.get("effort_exempt_sentinels"):
        raise RosterError(f"roster at {path} has no effort_exempt_sentinels")
    return doc


def priced(row: dict[str, Any]) -> dict[str, float] | None:
    """The row's rate table — `price.contract` preferred over `price.list` — or None if neither is sourced."""
    price = row["price"]
    return price["contract"] if price["contract"] is not None else price["list"]


def list_or_contract_output(row: dict[str, Any]) -> float:
    """The row's public output rate — always `price.list`, falling back to `price.contract`
    only if no list price is on file. Distinct from `priced()`: a rate meant for public
    display prefers the published list price even when a contract rate also exists."""
    price = row["price"]
    table = price["list"] if price["list"] is not None else price["contract"]
    return table["output"]


def select(models: dict[str, Any], predicate: Callable[[dict[str, Any]], bool]) -> list[str]:
    """Model IDs (unordered) whose row satisfies `predicate`."""
    return [mid for mid, row in models.items() if predicate(row)]
