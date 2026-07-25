"""Rendering the differential's verdict."""
from __future__ import annotations

from collections import Counter

from .diff import Divergence


def summary(divergences: list[Divergence]) -> str:
    if not divergences:
        return "no structural divergence"
    kinds = Counter(d.kind for d in divergences)
    commands = sorted({d.command for d in divergences})
    breakdown = ", ".join(f"{kind} x{count}" for kind, count in sorted(kinds.items()))
    return f"{len(divergences)} structural divergence(s) across {len(commands)} subcommand(s): {breakdown}"


def render(divergences: list[Divergence], pre_ref: str, commands_covered: int, probes_run: int) -> str:
    lines = [
        "# Post-M0 structural differential",
        "",
        f"- pre-M0 artifact: `{pre_ref}`",
        f"- subcommands covered: {commands_covered}",
        f"- probes per binary: {probes_run}",
        f"- verdict: {summary(divergences)}",
        "",
    ]
    if not divergences:
        lines.append("The post-M0 binary's argv grammar, stdin handling, stdout JSON shapes and exit codes are")
        lines.append("identical to the pre-M0 artifact's on every probe.")
        return "\n".join(lines) + "\n"

    lines += ["## Divergences", "", "| subcommand | kind | where | pre-M0 | post-M0 |", "| --- | --- | --- | --- | --- |"]
    for d in divergences:
        lines.append(f"| `{d.command}` | {d.kind} | {d.detail} | `{d.was}` | `{d.now}` |")
    lines.append("")
    return "\n".join(lines) + "\n"
