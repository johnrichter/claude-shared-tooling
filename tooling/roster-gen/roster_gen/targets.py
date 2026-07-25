"""The output paths this generator owns, and how to render each.

`kind` selects how the CLI drives the render function:
  "whole"     — roster-only input; the file is fully replaced.
  "patch"     — (existing file text, roster) in; the file must already exist.
  "allowlist" — roster-only input, but checked against the existing on-disk ID
                 set first (may only grow, never drop an ID).
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

from . import render


@dataclass(frozen=True)
class Target:
    repo: str  # "ai-shared-lib" | "marketplace"
    rel_path: str
    kind: str
    render_fn: Callable[..., str]


TARGETS: list[Target] = [
    Target("ai-shared-lib", "go/anthropic-specifications.json", "whole", render.render_specs),
    Target(
        "marketplace",
        "plugins/delivery-agent-team/skills/build-with-team/anthropic-specifications.json",
        "whole",
        render.render_specs,
    ),
    Target(
        "marketplace",
        "plugins/delivery-agent-team/agents/product-architect/plan-schema.json",
        "patch",
        render.patch_plan_schema,
    ),
    Target(
        "marketplace",
        "plugins/delivery-agent-team/skills/build-with-team/build-engine.workflow.js",
        "patch",
        render.patch_build_engine,
    ),
    Target(
        "marketplace",
        "plugins/governance-sub-agents/hooks/model-roster",
        "allowlist",
        render.render_gate_allowlist,
    ),
    Target(
        "marketplace",
        "plugins/governance-sub-agents/governance-model-tiering.md",
        "patch",
        render.patch_tiering_doc,
    ),
]


def resolve(target: Target, roots: dict[str, Path]) -> Path:
    return roots[target.repo] / target.rel_path
