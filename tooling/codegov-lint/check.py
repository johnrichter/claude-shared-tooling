#!/usr/bin/env python3
"""codegov-lint — code-authoring gate.

Enforces the org's code-comment policy (governance-code-authoring): every exported/public
symbol carries a doc comment (Python: Google-style docstrings, via ruff's pydocstyle rules;
Go/Rust: a presence check backstopping their own per-language doc gates), and no comment
carries cross-language-port archaeology, an embedded milestone/phase/task/spec-criterion id,
or narration built from a line of commented-out code.

Usage:
  python3 tooling/codegov-lint/check.py --diff REF   (CI mode: lint files changed since REF)
  python3 tooling/codegov-lint/check.py --files PATH...
  python3 tooling/codegov-lint/check.py --all         (audit mode, full tracked tree)

Exit codes: 0 no violations; 1 one or more violations found; 2 the harness itself could not
run (not a git repo, bad arguments).
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from codegov_lint.cli import main  # noqa: E402

if __name__ == "__main__":
    raise SystemExit(main())
