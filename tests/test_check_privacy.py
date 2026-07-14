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
_GHP = "ghp_" + "A" * 36                            # ghp_ + 36 alnum -> matches GitHub token pattern


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


class ScopeAndExemptionAdversarialTests(unittest.TestCase):
    """Adversarial coverage for the frontmatter-scoping + fixture-exemption change.

    Proves the exemption/scoping is classification-only and never widens the
    secret-scan blind spot, and pins the actual behavior of the frontmatter
    detector's edge cases so a regression is caught even where the checker's
    own strictness is debatable.
    """

    def _scan(self, files: dict[str, str]):
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        for rel, content in files.items():
            p = root / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8")
        return cp.scan(root, _CHECKER)

    # --- SAFETY: exemption/scoping must never reduce secret-scan coverage ---

    def test_secret_under_exemption_glob_still_fails(self):
        # rust/frontmatter/tests/* is exempt from CLASSIFICATION only, never secrets.
        failures, _ = self._scan({"rust/frontmatter/tests/fixture.md": f"{_KEY}\nabc\n"})
        self.assertTrue(any("private key" in f for f in failures), msg=str(failures))

    def test_secret_in_non_md_files_still_fails(self):
        failures, _ = self._scan({
            "src/lib.rs": f"const KEY: &str = \"{_AWS}\";\n",
            "config.json": f'{{"token": "{_GHP}"}}\n',
            "tool.py": f"TOKEN = '{_GHP}'\n",
            "leak": f"{_AWS}\n",  # no extension at all
        })
        self.assertEqual(len(failures), 4, msg=str(failures))
        self.assertTrue(any("lib.rs" in f for f in failures), msg=str(failures))
        self.assertTrue(any("config.json" in f for f in failures), msg=str(failures))
        self.assertTrue(any("tool.py" in f for f in failures), msg=str(failures))
        self.assertTrue(any(f.startswith("leak:") for f in failures), msg=str(failures))

    def test_secret_in_md_body_outside_frontmatter_still_fails(self):
        content = f"---\n{_P} public\n{_O} public\n---\n\n{_AWS}\n"
        failures, _ = self._scan({"doc.md": content})
        self.assertTrue(any("AWS" in f for f in failures), msg=str(failures))

    # --- SAFETY: real classification leaks outside the exemption still fail ---

    def test_root_md_privacy_internal_no_public_pair_fails_both_checks(self):
        failures, _ = self._scan({"leak.md": f"---\n{_P} internal\n---\n"})
        self.assertTrue(any("private marker" in f and "privacy" in f for f in failures), msg=str(failures))
        self.assertTrue(any("declares privacy: tag but not privacy:public" in f for f in failures), msg=str(failures))

    def test_owner_datadog_outside_exemption_fails(self):
        failures, _ = self._scan({"notes/secret.md": f"---\n{_O} {_DD}\n{_P} public\n---\n"})
        self.assertTrue(any("private marker" in f and "owner" in f for f in failures), msg=str(failures))

    # --- CORRECTLY-CLEARED: the false positives the frontmatter-scoping change fixes ---

    def test_privacy_owner_as_source_string_literals_pass(self):
        failures, _ = self._scan({
            "lib.rs": f'let s = "{_P}internal owner_lit";\n',
            "mod.py": f'X = "{_O}{_DD}"\n',
        })
        self.assertEqual(failures, [])

    def test_json_schema_enumerating_tag_values_passes(self):
        content = '{"allowed_privacy": ["' + _P + 'internal", "' + _P + 'public"], "allowed_owner": ["' + _O + _DD + '"]}\n'
        failures, _ = self._scan({"schema.json": content})
        self.assertEqual(failures, [])

    def test_exempt_fixture_with_real_private_frontmatter_passes_classification(self):
        content = f"---\n{_P} internal\n{_O} public\n---\n# fixture\n"
        failures, _ = self._scan({"rust/frontmatter/tests/fixture2.md": content})
        self.assertEqual(failures, [])

    # --- EDGE CASES: frontmatter-detector strictness ---

    def test_leading_blank_line_before_frontmatter_still_catches_privacy_internal(self):
        # Regression guard: a leading blank line before the opening `---` must
        # not defeat frontmatter detection -- every markdown renderer still
        # treats this as frontmatter, so the classification check must too.
        content = f"\n---\n{_P} internal\n{_O} public\n---\n"
        failures, _ = self._scan({"sneaky.md": content})
        self.assertTrue(
            any("private marker" in f for f in failures),
            msg="leading blank line must not hide privacy:internal -- " + str(failures),
        )

    def test_bom_before_frontmatter_still_catches_privacy_internal(self):
        # Regression guard: a UTF-8 BOM before the opening `---` must not
        # defeat frontmatter detection either.
        content = f"﻿---\n{_P} internal\n{_O} public\n---\n"
        failures, _ = self._scan({"bom.md": content})
        self.assertTrue(
            any("private marker" in f for f in failures),
            msg="leading BOM must not hide privacy:internal -- " + str(failures),
        )

    def test_trailing_whitespace_on_closing_fence_still_catches_privacy_internal(self):
        # Regression guard: a properly-opened frontmatter block whose closing
        # `---` carries trailing whitespace (a common invisible editor artifact)
        # must still be recognized as frontmatter -- common parsers accept
        # `---\s*`. An exact-match close would treat the block as unclosed and
        # skip classification entirely, hiding privacy:internal.
        content = f"---\n{_P} internal\n{_O} public\n--- \nbody\n"
        failures, _ = self._scan({"trailing.md": content})
        self.assertTrue(
            any("private marker" in f for f in failures),
            msg="trailing space on closing fence must not hide privacy:internal -- " + str(failures),
        )

    def test_indented_opening_fence_is_not_frontmatter(self):
        # An indented `---` is body content, not a frontmatter delimiter (parsers
        # require column 0). Trailing-whitespace tolerance must not slacken this.
        content = f"   ---\n{_P} internal\n{_O} public\n---\n"
        failures, _ = self._scan({"indented.md": content})
        self.assertEqual(failures, [], msg=str(failures))

    def test_privacy_internal_in_body_only_passes_classification(self):
        # Body text is data, not this file's own classification -- confirmed OK.
        content = f"---\n{_P} public\n{_O} public\n---\n\nSee also {_P} internal in the wild.\n"
        failures, _ = self._scan({"doc.md": content})
        self.assertEqual(failures, [], msg=str(failures))


if __name__ == "__main__":
    unittest.main(verbosity=2)
