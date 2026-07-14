#!/usr/bin/env python3
"""Capture the live Python frontmatter-gate verdicts into the frozen golden.

M2.P2.T2's one-time cutover no-regression gate compares the Rust validator
against the CURRENT Python gate. This script produces the golden that
comparison reads (`expected_verdicts.json`) -- it is never run by `cargo
test`; the Rust test fails loudly if the golden is absent rather than
regenerating it itself.

Oracle reconciliation: the build plan says "capture check_rules.py
verdicts," but check_rules.py is a coarse rule-authoring meta-lint that
emits only flat "filename: message" strings, not a per-file {code, field}
verdict. The actual per-file oracle is
`audit_helper.frontmatter.validate(rel_path, text) -> ValidationResult`
(`.violations` are `Violation(code, field, message)` NamedTuples). This
script calls that function directly, once per fixture, rather than going
through the package CLI (which also walks scope/refs/metrics -- concerns
outside this differential's scope).

Environment: set AUDIT_HELPER_DIR to the audit-helper package directory,
and run with that package's own venv -- e.g.:

    AUDIT_HELPER_DIR=<path-to-audit-helper> \
        <audit-helper>/.venv/bin/python tests/cutover/capture_python_verdicts.py

Regeneration: re-run this script whenever a fixture or the manifest
changes; it always overwrites `expected_verdicts.json` from scratch. At the
M2.P2.T3 cutover, repoint `CORPUS_ROOT`/`MANIFEST_PATH` below at the live
psa-apm workspace and its full file corpus to run the real no-regression
check, rather than this frozen fixture set.

Exempt-path caveat for the T3 full-corpus repoint: `frontmatter.validate`
does NOT apply the exempt taxonomy -- exemption lives in
`schema.is_doc_exempt` and the real gate applies it at the scan layer
(exempt files are skipped, never validated). The Rust `validate()` mirrors
the real gate by short-circuiting exempt paths internally. So a full-corpus
run MUST filter exempt paths (call `schema.is_doc_exempt` here, or drop them
from the manifest) before capturing; otherwise every exempt file becomes a
false divergence -- the per-function oracle emits violations the real gate
never surfaces. The frozen fixture set contains no exempt paths, so this
does not affect the committed golden.

`period` interval migration (interval-stage-2): the psa-apm pack's `period`
regex moved from the legacy `YYYY-MM-DD..YYYY-MM-DD` form to the ISO 8601
`YYYY-MM-DD/YYYY-MM-DD` form. This script still calls the CURRENT live
Python oracle, which has not moved yet -- the committed golden's `period`
fixture/message text was hand-updated to the post-migration `/` form to
stay internally consistent with the Rust pack's new regex, NOT captured
from a live run. Re-running this script today would regenerate a
`period`-stale golden until the Python gate adopts the same `/` form
(interval-stage-3).
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

# AUDIT_HELPER_DIR must point at the audit-helper package directory (the
# one containing the `audit_helper` module); it is caller-supplied since
# that package lives outside this repo.
_AUDIT_HELPER_DIR_ENV = os.environ.get("AUDIT_HELPER_DIR")
if not _AUDIT_HELPER_DIR_ENV:
    sys.exit(
        "capture_python_verdicts.py: set AUDIT_HELPER_DIR to the audit-helper "
        "package directory before running this script, e.g.:\n"
        "  AUDIT_HELPER_DIR=/path/to/audit-helper python3 "
        "tests/cutover/capture_python_verdicts.py"
    )
AUDIT_HELPER_DIR = Path(_AUDIT_HELPER_DIR_ENV)

TESTS_DIR = Path(__file__).resolve().parent
MANIFEST_PATH = TESTS_DIR / "manifest.json"
FIXTURES_DIR = TESTS_DIR / "fixtures"
GOLDEN_PATH = TESTS_DIR / "expected_verdicts.json"

sys.path.insert(0, str(AUDIT_HELPER_DIR))
from audit_helper import frontmatter  # noqa: E402  (path insert must precede this import)


def capture_one(rel_path: str, fixture_path: Path) -> dict:
    text = fixture_path.read_text()
    result = frontmatter.validate(rel_path, text)
    return {
        "file_class": result.file_class.value,
        "is_valid": result.is_valid,
        "violations": [
            {"code": v.code, "field": v.field, "message": v.message} for v in result.violations
        ],
    }


def main() -> None:
    manifest = json.loads(MANIFEST_PATH.read_text())
    golden: dict[str, dict] = {}
    for entry in manifest:
        fixture_path = FIXTURES_DIR / entry["fixture"]
        golden[entry["rel_path"]] = capture_one(entry["rel_path"], fixture_path)

    GOLDEN_PATH.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")
    print(f"wrote {len(golden)} entries to {GOLDEN_PATH}")


if __name__ == "__main__":
    main()
