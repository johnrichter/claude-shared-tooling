#!/usr/bin/env python3
"""invariant-lint — the machine consumer for rung 1 and rung 3 of the invariant registry.

Two of the registry's five rungs carry a machine check; this is it. Rung 1 (`fail_fast_symbol`)
is asserted by resolving the named symbol in its source file — a rename, a move to a different
file, or a deletion all fail. Rung 3 (`test_id`) is asserted by loading the named test from the
shipped suite and running it — a test id that resolves to nothing fails, and a test that resolves
but is skipped or filtered out also fails, because a skipped check enforces nothing.

What this deliberately does NOT check: registry validity, restatement, gate-path resolution and
gate completeness belong to schemas/invariant-registry/check.py. This tool only resolves the two
machine-checked consumer fields against the tree and the running suite.

Usage:
    python3 tooling/invariant-lint/check.py
    python3 tooling/invariant-lint/check.py --repo marketplace=../marketplace
    python3 tooling/invariant-lint/check.py --require-roots

Exit codes: 0 every shipped rung-1/rung-3 entry resolves and runs; 1 one or more do not;
2 the harness itself could not run (unreadable registry, bad arguments — never a silent pass).
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from invariant_lint.cli import main  # noqa: E402

if __name__ == "__main__":
    raise SystemExit(main())
