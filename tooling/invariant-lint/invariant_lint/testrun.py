"""Rung-3 resolution: does a declared `test_id` exist in the shipped suite, and does it run?

The declared id is loaded and executed in a fresh subprocess, one per entry, so one
entry's test cannot leak import-time or process-global state into the next. A nonexistent
module, class, or method resolves through `unittest.TestLoader` to a placeholder test
(`unittest.loader._FailedTest`) rather than raising, so existence and "did it run" are
both read from the same executed result instead of being inferred from an import outcome.

What counts as enforcement is that the test ran — passed or failed, both are runs. A
failing assertion is a defect the ordinary unit-test job already reports; what this check
adds is the case that job cannot see: a test skipped, filtered out, or renamed away still
shows green there while enforcing nothing.
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

# Runs entirely inside the subprocess: resolves `sys.argv[1]` (a dotted unittest name) and
# reports what it found without ever raising past this script, so the parent always gets a
# JSON verdict on stdout rather than having to distinguish "doesn't exist" from "crashed".
_RUNNER = """
import json
import sys
import unittest

def flatten(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item

try:
    tests = list(flatten(unittest.TestLoader().loadTestsFromName(sys.argv[1])))
except Exception as exc:
    print(json.dumps({"status": "import_error", "detail": str(exc)}))
    raise SystemExit(0)

if not tests:
    print(json.dumps({"status": "empty"}))
    raise SystemExit(0)
if any(type(t).__name__ == "_FailedTest" for t in tests):
    print(json.dumps({"status": "missing"}))
    raise SystemExit(0)

result = unittest.TestResult()
unittest.TestSuite(tests).run(result)
print(json.dumps({
    "status": "ran",
    "count": len(tests),
    "skipped": [t.id() for t, _ in result.skipped],
}))
"""

_TEST_ID = re.compile(
    r"^(?P<path>[a-z0-9][a-z0-9._-]*(?:/[^\s:]+)+)::"
    r"(?P<selector>[A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)?)$"
)


class TestIdMalformed(Exception):
    """`test_id` does not match the registry schema's own pattern."""


def _dotted_spec(repo_relative: Path, selector: str) -> str | None:
    """Module dotted name plus selector, or None if the file isn't a Python test module."""
    if repo_relative.suffix != ".py":
        return None
    module = ".".join(repo_relative.with_suffix("").parts)
    return f"{module}.{selector.replace('::', '.')}"


def check_test_id(test_id: str, repo_root: Path) -> tuple[bool, str]:
    """Resolve and run `test_id`'s file inside `repo_root`. Returns (ok, reason-if-not).

    `repo_root` is the checkout the id's first path segment names; the rest of the path
    is relative to it.
    """
    match = _TEST_ID.match(test_id)
    if not match:
        raise TestIdMalformed(test_id)

    repo_relative = Path(match.group("path").partition("/")[2])
    file_path = repo_root / repo_relative
    if not file_path.is_file():
        return False, f"{match.group('path')} does not exist"

    dotted = _dotted_spec(repo_relative, match.group("selector"))
    if dotted is None:
        return False, f"{file_path}: not a Python test module - no resolver for this suite type"

    proc = subprocess.run(
        [sys.executable, "-c", _RUNNER, dotted],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0 or not proc.stdout.strip():
        detail = proc.stderr.strip() or f"exit {proc.returncode}"
        return False, f"{dotted}: harness could not run it ({detail})"

    outcome = json.loads(proc.stdout.strip().splitlines()[-1])
    status = outcome["status"]
    if status in ("missing", "empty"):
        return False, f"{dotted}: does not exist in the shipped suite"
    if status == "import_error":
        return False, f"{dotted}: {outcome['detail']}"
    if outcome["skipped"]:
        return False, f"{dotted}: skipped - {', '.join(outcome['skipped'])}"
    return True, ""
