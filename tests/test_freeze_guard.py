#!/usr/bin/env python3
"""Unit tests for tooling/freeze-guard/check.py — the SC-FREEZE guardrail.

Enforces the contract: frozen homes (plugin-homes/datadog-{docs,code}-agent and
corpus/datadog-{docs,code}-agent) reject any write via CI/pre-merge; writes
elsewhere pass.

Coverage (mirrors the guardrail's stated contract):
    1. A write to a frozen home path FAILS (exit 1).
    2. A write outside frozen homes PASSES (exit 0).
    3. Nested and sibling paths are correctly classified (prefix matching with
       directory-boundary awareness).
    4. Frozen-home paths (from frozen-homes.json) are documented or checked for
       plausibility in the repo structure.
"""
from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_GUARD = Path(__file__).resolve().parent.parent / "tooling" / "freeze-guard" / "check.py"
_spec = importlib.util.spec_from_file_location("freeze_guard_check", _GUARD)
guard = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(guard)


class FreezeGuardTests(unittest.TestCase):
    """Unit tests for the freeze-guard's core logic."""

    def test_is_under_frozen_home_exact_match(self):
        """An exact match to a frozen home is detected."""
        frozen = ["plugin-homes/datadog-docs-agent", "corpus/datadog-code-agent"]
        self.assertTrue(guard.is_under_frozen_home("plugin-homes/datadog-docs-agent", frozen))
        self.assertTrue(guard.is_under_frozen_home("corpus/datadog-code-agent", frozen))

    def test_is_under_frozen_home_nested_path(self):
        """A path under a frozen home is detected."""
        frozen = ["plugin-homes/datadog-docs-agent"]
        self.assertTrue(guard.is_under_frozen_home("plugin-homes/datadog-docs-agent/README.md", frozen))
        self.assertTrue(guard.is_under_frozen_home("plugin-homes/datadog-docs-agent/src/plugin.py", frozen))

    def test_is_under_frozen_home_deep_nesting(self):
        """Deeply nested paths under frozen homes are detected."""
        frozen = ["corpus/datadog-code-agent"]
        self.assertTrue(guard.is_under_frozen_home(
            "corpus/datadog-code-agent/data/models/v1/spec.json", frozen
        ))

    def test_is_under_frozen_home_prefix_match_requires_boundary(self):
        """Prefix matches without a path separator are NOT frozen (boundary check)."""
        frozen = ["plugin-homes/datadog-docs-agent"]
        # A path that starts with the frozen dir name but lacks the separator
        # should not be considered frozen (e.g., if someone had a sibling dir
        # named "plugin-homes/datadog-docs-agent-backup").
        self.assertFalse(guard.is_under_frozen_home(
            "plugin-homes/datadog-docs-agent-backup", frozen
        ))
        self.assertFalse(guard.is_under_frozen_home(
            "plugin-homes/datadog-docs-agent_alt", frozen
        ))

    def test_is_under_frozen_home_sibling_not_frozen(self):
        """Sibling directories are not frozen."""
        frozen = ["plugin-homes/datadog-docs-agent"]
        self.assertFalse(guard.is_under_frozen_home("plugin-homes/datadog-code-agent", frozen))
        self.assertFalse(guard.is_under_frozen_home("plugin-homes/other-plugin", frozen))

    def test_is_under_frozen_home_parent_not_frozen(self):
        """A parent directory is not frozen if only a child is listed."""
        frozen = ["plugin-homes/datadog-docs-agent"]
        self.assertFalse(guard.is_under_frozen_home("plugin-homes", frozen))

    def test_is_under_frozen_home_writable_locations(self):
        """Writable locations (toolbelt homes, arbitrary paths) are not frozen."""
        frozen = ["plugin-homes/datadog-docs-agent", "corpus/datadog-code-agent"]
        self.assertFalse(guard.is_under_frozen_home("tooling/freeze-guard", frozen))
        self.assertFalse(guard.is_under_frozen_home("scripts/check_secrets.py", frozen))
        self.assertFalse(guard.is_under_frozen_home("tests/test_freeze_guard.py", frozen))
        self.assertFalse(guard.is_under_frozen_home("README.md", frozen))

    def test_is_under_frozen_home_windows_paths(self):
        """Windows path separators are normalized to forward slashes."""
        frozen = ["plugin-homes/datadog-docs-agent"]
        # The function normalizes backslashes to forward slashes.
        self.assertTrue(guard.is_under_frozen_home(
            "plugin-homes\\datadog-docs-agent\\file.py", frozen
        ))

    def test_load_frozen_homes_valid(self):
        """Loading a valid frozen-homes.json succeeds."""
        with tempfile.TemporaryDirectory() as td:
            manifest_path = Path(td) / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({
                    "version": "1.0.0",
                    "frozen_homes": ["plugin-homes/datadog-docs-agent"]
                }),
                encoding="utf-8"
            )
            homes = guard.load_frozen_homes(manifest_path)
            self.assertEqual(homes, ["plugin-homes/datadog-docs-agent"])

    def test_load_frozen_homes_missing_file(self):
        """Loading a missing frozen-homes.json raises RuntimeError."""
        nonexistent = Path("/nonexistent/frozen-homes.json")
        with self.assertRaises(RuntimeError):
            guard.load_frozen_homes(nonexistent)

    def test_load_frozen_homes_invalid_json(self):
        """Loading invalid JSON raises RuntimeError."""
        with tempfile.TemporaryDirectory() as td:
            manifest_path = Path(td) / "frozen-homes.json"
            manifest_path.write_text("{ invalid json", encoding="utf-8")
            with self.assertRaises(RuntimeError):
                guard.load_frozen_homes(manifest_path)

    def test_check_freeze_pass_no_changes(self):
        """No changes means the guard passes."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({"frozen_homes": ["plugin-homes/datadog-docs-agent"]}),
                encoding="utf-8"
            )
            with patch.object(guard, "get_changed_files", return_value=[]):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertTrue(success)
                self.assertEqual(violations, [])

    def test_check_freeze_pass_changes_elsewhere(self):
        """Changes outside frozen homes pass."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({"frozen_homes": ["plugin-homes/datadog-docs-agent"]}),
                encoding="utf-8"
            )
            changed_files = [
                "scripts/check_secrets.py",
                "tests/test_freeze_guard.py",
                "README.md",
            ]
            with patch.object(guard, "get_changed_files", return_value=changed_files):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertTrue(success)
                self.assertEqual(violations, [])

    def test_check_freeze_fail_frozen_home_write(self):
        """A write to a frozen home fails."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({"frozen_homes": ["plugin-homes/datadog-docs-agent"]}),
                encoding="utf-8"
            )
            changed_files = ["plugin-homes/datadog-docs-agent/README.md"]
            with patch.object(guard, "get_changed_files", return_value=changed_files):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertFalse(success)
                self.assertEqual(violations, ["plugin-homes/datadog-docs-agent/README.md"])

    def test_check_freeze_fail_multiple_violations(self):
        """Multiple frozen-home writes all fail."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({
                    "frozen_homes": [
                        "plugin-homes/datadog-docs-agent",
                        "corpus/datadog-code-agent"
                    ]
                }),
                encoding="utf-8"
            )
            changed_files = [
                "plugin-homes/datadog-docs-agent/src/plugin.py",
                "corpus/datadog-code-agent/models/spec.json",
                "tests/test.py",  # This one is OK.
            ]
            with patch.object(guard, "get_changed_files", return_value=changed_files):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertFalse(success)
                self.assertEqual(len(violations), 2)
                self.assertIn("plugin-homes/datadog-docs-agent/src/plugin.py", violations)
                self.assertIn("corpus/datadog-code-agent/models/spec.json", violations)

    def test_check_freeze_fail_corpus_violation(self):
        """A write to corpus homes is also caught."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({"frozen_homes": ["corpus/datadog-docs-agent"]}),
                encoding="utf-8"
            )
            changed_files = ["corpus/datadog-docs-agent/data/config.yaml"]
            with patch.object(guard, "get_changed_files", return_value=changed_files):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertFalse(success)
                self.assertEqual(violations, ["corpus/datadog-docs-agent/data/config.yaml"])

    def test_check_freeze_mixed_violations_and_clean(self):
        """Multiple files: some violate, some don't."""
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            manifest_path = root / "frozen-homes.json"
            manifest_path.write_text(
                json.dumps({
                    "frozen_homes": [
                        "plugin-homes/datadog-docs-agent",
                        "corpus/datadog-code-agent"
                    ]
                }),
                encoding="utf-8"
            )
            changed_files = [
                "plugin-homes/datadog-docs-agent/plugin.py",  # Violation
                "scripts/check_secrets.py",  # OK
                "corpus/datadog-code-agent/data.json",  # Violation
                "README.md",  # OK
                "tests/test.py",  # OK
            ]
            with patch.object(guard, "get_changed_files", return_value=changed_files):
                success, violations = guard.check_freeze(root, manifest_path)
                self.assertFalse(success)
                self.assertEqual(len(violations), 2)
                self.assertIn("plugin-homes/datadog-docs-agent/plugin.py", violations)
                self.assertIn("corpus/datadog-code-agent/data.json", violations)


class FreezeGuardIntegrationTests(unittest.TestCase):
    """Integration tests using the actual frozen-homes.json in the repo."""

    def test_canonical_frozen_homes_loaded(self):
        """The canonical frozen-homes.json loads successfully."""
        manifest_path = Path(__file__).resolve().parent.parent / "tooling" / "freeze-guard" / "frozen-homes.json"
        self.assertTrue(manifest_path.exists(), f"frozen-homes.json not found at {manifest_path}")
        frozen_homes = guard.load_frozen_homes(manifest_path)
        self.assertIsInstance(frozen_homes, list)
        self.assertGreater(len(frozen_homes), 0, "frozen_homes list is empty")

    def test_canonical_frozen_homes_structure(self):
        """The canonical frozen-homes.json has the expected structure."""
        manifest_path = Path(__file__).resolve().parent.parent / "tooling" / "freeze-guard" / "frozen-homes.json"
        with manifest_path.open("r", encoding="utf-8") as f:
            data = json.load(f)
        self.assertIn("frozen_homes", data)
        self.assertIn("datadog-docs-agent", str(data["frozen_homes"]))
        self.assertIn("datadog-code-agent", str(data["frozen_homes"]))

    def test_canonical_frozen_homes_documented(self):
        """The canonical frozen-homes paths correspond to the documented spec.

        The spec lists:
          - plugin-homes/datadog-docs-agent
          - plugin-homes/datadog-code-agent
          - corpus/datadog-docs-agent
          - corpus/datadog-code-agent

        This test verifies the JSON actually contains these paths (or documents
        why a full drift check is not yet possible).
        """
        manifest_path = Path(__file__).resolve().parent.parent / "tooling" / "freeze-guard" / "frozen-homes.json"
        frozen_homes = guard.load_frozen_homes(manifest_path)

        expected_paths = {
            "plugin-homes/datadog-docs-agent",
            "plugin-homes/datadog-code-agent",
            "corpus/datadog-docs-agent",
            "corpus/datadog-code-agent",
        }
        actual_paths = set(frozen_homes)

        # As of the time of writing, the canonical frozen-homes paths are
        # specified in the README but may not yet exist in the repo tree
        # (they will exist once the plugins are released/vendored).
        # This assertion confirms the paths in frozen-homes.json match the spec.
        self.assertEqual(
            actual_paths, expected_paths,
            f"frozen_homes mismatch: expected {expected_paths}, got {actual_paths}"
        )

    def test_frozen_homes_paths_not_yet_in_worktree(self):
        """Documents why a full directory-existence drift check is not yet possible.

        The paths in frozen-homes.json (plugin-homes/datadog-docs-agent, etc.)
        describe locations WHERE the plugins/corpus WILL BE once released/vendored
        into this tree. They do not yet exist (this is a pre-release state).
        A drift check that requires them to exist would block development.
        A best-effort check would verify they match the spec (done above).
        """
        # This test documents the limitation and confirms the spec-based check
        # is the right approach for now.
        worktree_root = Path(__file__).resolve().parent.parent
        frozen_homes = guard.load_frozen_homes(
            worktree_root / "tooling" / "freeze-guard" / "frozen-homes.json"
        )
        # None of these paths are expected to exist yet (they will after release).
        for home in frozen_homes:
            path = worktree_root / home
            # Do NOT assert they don't exist (they might, for testing), but
            # document that a real drift check would need the actual plugin
            # directories to be present in the tree, which they are not yet.
            if not path.exists():
                # Expected case: paths specified in frozen-homes.json but not yet
                # in the tree. The guard is still correct because it will catch
                # writes when these paths do exist.
                pass


if __name__ == "__main__":
    unittest.main(verbosity=2)
