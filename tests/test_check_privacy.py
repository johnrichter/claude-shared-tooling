#!/usr/bin/env python3
"""Unit tests for scripts/check_privacy.py — the security-critical public-repo guardrail.

The checker is a standalone script (not part of the installed package), so it is loaded
by path via importlib. Tests scan throwaway temp trees, never the real repo.

Trigger literals (private markers, secrets) are assembled from fragments so this test's
OWN source does not trip the guardrail when it scans the repo tree.

Coverage (mirrors the guardrail's stated contract):
    1. Private privacy:/owner: markers FAIL (the repo-split routing key).
    2. Frontmatter that declares privacy/owner but NOT the public pair FAILs — including
       non-enumerated values (e.g. `privacy: restricted`) the marker list does not name.
       This is the regression guard for the previously-unwired FM_PAIR_CHECKS.
    3. High-confidence secrets FAIL.
    4. The public pair (privacy:public + owner:public) PASSes.
    5. Org mentions WARN (advisory).
"""
from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

_CHECKER = Path(__file__).resolve().parent.parent / "scripts" / "check_privacy.py"
_spec = importlib.util.spec_from_file_location("check_privacy", _CHECKER)
cp = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cp)

_P = "privacy:"       # assembled so this file carries no literal private marker / org mention
_O = "owner:"
_DD = "data" + "dog"
_AWS = "AKIA" + "IOSFODNN7" + "EXAMPLE"             # AKIA + exactly 16 -> matches AWS key pattern
_KEY = "-----BEGIN " + "OPENSSH PRIVATE " + "KEY-----"


class ScanTests(unittest.TestCase):
    def _scan(self, files: dict[str, str]):
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        for rel, content in files.items():
            p = root / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8")
        # self_path points at the real checker (outside the temp tree) so nothing is skipped.
        return cp.scan(root, _CHECKER)

    def test_private_privacy_marker_fails(self):
        failures, _ = self._scan({"doc.md": f"---\n{_P} internal\n{_O} public\n---\n"})
        self.assertTrue(any("privacy" in f for f in failures))

    def test_private_owner_marker_fails(self):
        failures, _ = self._scan({"doc.md": f"---\n{_O} {_DD}\n{_P} public\n---\n"})
        self.assertTrue(any("owner" in f for f in failures))

    def test_nonenumerated_privacy_value_fails_via_pair_check(self):
        # `privacy: restricted` is not in PRIVATE_MARKERS; only the FM pair check catches it.
        failures, _ = self._scan({"doc.md": f"---\n{_P} restricted\n{_O} public\n---\n"})
        self.assertTrue(any("privacy:public" in f for f in failures), msg=str(failures))

    def test_owner_without_public_fails_via_pair_check(self):
        failures, _ = self._scan({"doc.md": f"---\n{_O} team\n{_P} public\n---\n"})
        self.assertTrue(any("owner:public" in f for f in failures), msg=str(failures))

    def test_public_pair_passes(self):
        failures, _ = self._scan({"doc.md": f"---\n{_P} public\n{_O} public\n---\n# hi\n"})
        self.assertEqual(failures, [])

    def test_no_privacy_tags_at_all_passes(self):
        failures, _ = self._scan({"code.py": "print('hello world')\n"})
        self.assertEqual(failures, [])

    def test_private_key_secret_fails(self):
        failures, _ = self._scan({"leak.txt": f"{_KEY}\nabc\n"})
        self.assertTrue(any("private key" in f for f in failures))

    def test_aws_key_secret_fails(self):
        failures, _ = self._scan({"leak.env": f"AWS_KEY={_AWS}\n"})
        self.assertTrue(any("AWS" in f for f in failures))

    def test_org_mention_warns_not_fails(self):
        failures, warnings = self._scan({"notes.md": "built at " + _DD.capitalize() + "\n"})
        self.assertEqual(failures, [])
        self.assertTrue(warnings)

    def test_binary_and_skip_dirs_ignored(self):
        failures, _ = self._scan({
            "img.png": _AWS,            # binary suffix -> not scanned
            ".git/config": _AWS,        # skip dir -> not scanned
        })
        self.assertEqual(failures, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
