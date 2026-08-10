#!/usr/bin/env python3
"""Regression for tooling/invariant-lint — the rung-1/rung-3 registry consumer.

Exercises the resolvers directly (symbol resolution and test-id resolution) against real,
already-committed targets, rather than against the live registry document: the registry's
own entries are expected to change over time, and this suite must not need editing every
time one does.
"""
from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
_TOOL_DIR = _REPO_ROOT / "tooling" / "invariant-lint"

sys.path.insert(0, str(_TOOL_DIR))
from invariant_lint import symbols, testrun  # noqa: E402

_EXECUTION_GO = _REPO_ROOT / "go" / "state" / "record.go"


class CoreResolutionTests(unittest.TestCase):
    """The class this build's own rung-3 entry names as its target."""

    def test_rung1_resolves_a_real_go_func(self):
        ok, reason = symbols.resolve_symbol(_EXECUTION_GO, "RecordTask")
        self.assertTrue(ok, reason)

    def test_rung1_fails_on_a_renamed_symbol(self):
        ok, _ = symbols.resolve_symbol(_EXECUTION_GO, "RecordTaskDoesNotExist")
        self.assertFalse(ok)

    def test_rung1_fails_on_a_deleted_file(self):
        ok, reason = symbols.resolve_symbol(_EXECUTION_GO.with_name("no-such-file.go"), "RecordTask")
        self.assertFalse(ok)
        self.assertIn("does not exist", reason)

    def test_rung1_does_not_match_a_comment_mention(self):
        # A symbol that only appears in a comment is not a definition.
        with tempfile.TemporaryDirectory() as tmp:
            commented = Path(tmp) / "commented.go"
            commented.write_text("// RecordTask is documented elsewhere\n", encoding="utf-8")
            ok, _ = symbols.resolve_symbol(commented, "RecordTask")
        self.assertFalse(ok)

    def test_rung3_resolves_and_runs_a_real_test(self):
        ok, reason = testrun.check_test_id(
            "ai-shared-lib/tests/test_freeze_guard.py::FreezeGuardTests", _REPO_ROOT
        )
        self.assertTrue(ok, reason)

    def test_rung3_fails_on_a_nonexistent_selector(self):
        ok, reason = testrun.check_test_id(
            "ai-shared-lib/tests/test_freeze_guard.py::NoSuchTestClass", _REPO_ROOT
        )
        self.assertFalse(ok)
        self.assertIn("does not exist", reason)

    def test_rung3_fails_on_a_deleted_test_file(self):
        ok, reason = testrun.check_test_id(
            "ai-shared-lib/tests/no_such_test_file.py::Foo", _REPO_ROOT
        )
        self.assertFalse(ok)
        self.assertIn("does not exist", reason)

    def test_rung3_fails_on_a_skipped_test(self):
        # RealRepoByteParityTests is unconditionally skipped in this repo's own checkout
        # (it needs a marketplace sibling to run) — a stable, always-skipped fixture.
        ok, reason = testrun.check_test_id(
            "ai-shared-lib/tests/test_roster_gen.py"
            "::RealRepoByteParityTests::test_every_target_matches_its_own_recorded_tag",
            _REPO_ROOT,
        )
        self.assertFalse(ok)
        self.assertIn("skipped", reason)

    def test_rung3_is_deterministic(self):
        first = testrun.check_test_id(
            "ai-shared-lib/tests/test_freeze_guard.py::FreezeGuardTests", _REPO_ROOT
        )
        second = testrun.check_test_id(
            "ai-shared-lib/tests/test_freeze_guard.py::FreezeGuardTests", _REPO_ROOT
        )
        self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
