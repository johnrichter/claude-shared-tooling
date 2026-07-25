#!/usr/bin/env python3
"""roster-gen — one deterministic rendering pass from the model roster to every derived output.

The model roster (`ai-shared-lib/schemas/model-roster/model-roster.json`) is this generator's
only input. Every output below is a pure projection of it — never hand-edit an output; edit the
roster and regenerate.

Outputs:
    ai-shared-lib/go/anthropic-specifications.json
    marketplace/.../build-with-team/anthropic-specifications.json
    marketplace/.../product-architect/plan-schema.json          (model.enum + model.$comment only)
    marketplace/.../build-with-team/build-engine.workflow.js     (DEFAULT_RATES only)
    marketplace/.../governance-sub-agents/hooks/model-roster     (whole file)
    marketplace/.../governance-sub-agents/governance-model-tiering.md (capability table only)

Commands:
    generate  Render and write every output.
    check     Render every output in memory and diff against disk; exit 1 on drift or an absent
              target. Use in CI to catch a hand-edited or stale output.

Exit codes: 0 ok, 1 check found drift, 2 a roster/rendering error (e.g. narrowing the gate
allowlist, an unsupported roster schema version, a patch target that doesn't exist yet).

Usage:
    python3 generate.py \\
        --roster path/to/model-roster.json --tag v1.2.3 \\
        --ai-shared-lib-root path/to/ai-shared-lib --marketplace-root path/to/marketplace \\
        generate

    python3 generate.py --roster ... --tag ... --ai-shared-lib-root ... --marketplace-root ... check
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from roster_gen.cli import main  # noqa: E402

if __name__ == "__main__":
    raise SystemExit(main())
