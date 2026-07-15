#!/usr/bin/env python3
"""Unit tests for scripts/check_secrets.py — the security-critical secret-scanner.

The checker is a standalone script (not part of the installed package), so it is loaded
by path via importlib. Tests scan throwaway temp trees, never the real repo.

Trigger literals (secrets) are assembled from fragments so this test's OWN source does
not trip the guardrail when it scans the repo tree.

Coverage (mirrors the guardrail's stated contract):
    1. Each high-confidence secret pattern FAILs.
    2. A clean tree PASSes.
    3. Binary/skip-dir files are never scanned, and a real leak elsewhere still is.
"""
from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

_CHECKER = Path(__file__).resolve().parent.parent / "scripts" / "check_secrets.py"
_spec = importlib.util.spec_from_file_location("check_secrets", _CHECKER)
cs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cs)

_AWS = "AKIA" + "IOSFODNN7" + "EXAMPLE"             # AKIA + exactly 16 -> matches AWS key pattern
_KEY = "-----BEGIN " + "OPENSSH PRIVATE " + "KEY-----"
_GHP = "ghp_" + "A" * 36                            # ghp_ + 36 alnum -> matches GitHub token pattern
_SLACK = "xoxb-" + "1234567890" + "-abcdefghij"     # xoxb- + 10+ alnum/dash -> matches Slack token pattern


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
        return cs.scan(root, _CHECKER)

    def test_clean_tree_passes(self):
        failures = self._scan({"code.py": "print('hello world')\n", "doc.md": "# hi\n"})
        self.assertEqual(failures, [])

    def test_private_key_secret_fails(self):
        failures = self._scan({"leak.txt": f"{_KEY}\nabc\n"})
        self.assertTrue(any("private key" in f for f in failures))

    def test_aws_key_secret_fails(self):
        failures = self._scan({"leak.env": f"AWS_KEY={_AWS}\n"})
        self.assertTrue(any("AWS" in f for f in failures))

    def test_slack_token_secret_fails(self):
        failures = self._scan({"leak.txt": f"token={_SLACK}\n"})
        self.assertTrue(any("Slack" in f for f in failures))

    def test_github_token_secret_fails(self):
        failures = self._scan({"leak.txt": f"token={_GHP}\n"})
        self.assertTrue(any("GitHub" in f for f in failures))

    def test_binary_and_skip_dirs_ignored(self):
        failures = self._scan({
            "img.png": _AWS,            # binary suffix -> not scanned
            ".git/config": _AWS,        # skip dir -> not scanned
        })
        self.assertEqual(failures, [])

    def test_git_worktrees_skipped_but_real_leak_still_caught(self):
        # .git-worktrees holds transient full checkouts (mirrors .gitignore) --
        # a leak inside one must not be scanned, but a real leak elsewhere must
        # still fail. Regression guard for the enumerator/.gitignore drift.
        failures = self._scan({
            ".git-worktrees/claude/wt/some.env": f"AWS_KEY={_AWS}\n",
            "leak.env": f"AWS_KEY={_AWS}\n",
        })
        self.assertFalse(any(".git-worktrees" in f for f in failures), msg=str(failures))
        self.assertTrue(any(f.startswith("leak.env") for f in failures), msg=str(failures))

    def test_secret_scanned_regardless_of_extension(self):
        failures = self._scan({
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

    def test_secret_in_markdown_body_fails(self):
        content = f"# doc\n\n{_AWS}\n"
        failures = self._scan({"doc.md": content})
        self.assertTrue(any("AWS" in f for f in failures), msg=str(failures))

    def test_secret_mid_file_surrounded_by_many_lines_still_caught(self):
        # Boundary/adversarial: secret buried on line 51 of a 100-line file,
        # not at file start/end where a naive line-count-limited scan might look.
        filler = "\n".join(f"line {i} of filler text, nothing to see here" for i in range(50))
        content = f"{filler}\n{_AWS}\n" + "\n".join(f"line {i}" for i in range(50, 100))
        failures = self._scan({"big.log": content})
        self.assertTrue(any("AWS" in f for f in failures), msg=str(failures))

    def test_private_key_block_spanning_multiple_lines_caught(self):
        # The regex only matches the BEGIN header line itself, not the body --
        # confirm that still fires even when real key body lines follow across
        # many lines (the header is what the pattern keys off of).
        body = "\n".join("MIIBogIBAAJBAMabc" + str(i) for i in range(20))
        content = f"{_KEY}\n{body}\n-----END OPENSSH PRIVATE KEY-----\n"
        failures = self._scan({"id_rsa": content})
        self.assertTrue(any("private key" in f for f in failures), msg=str(failures))

    def test_all_github_token_prefixes_caught(self):
        # SE claims "ghp_/gho_/token" coverage broadly -- the pattern is
        # gh[pousr]_, i.e. ghp_/gho_/ghu_/ghs_/ghr_. Prove each fires.
        for prefix in ("ghp_", "gho_", "ghu_", "ghs_", "ghr_"):
            token = prefix + "B" * 36
            failures = self._scan({"leak.txt": f"TOKEN={token}\n"})
            self.assertTrue(any("GitHub" in f for f in failures), msg=f"{prefix}: {failures}")

    def test_clean_dir_with_only_skip_dir_secrets_passes_overall(self):
        # Confirms SKIP_DIRS suppression isn't accidentally partial: a repo
        # whose ONLY secret-shaped content lives entirely under skipped dirs
        # (.git, node_modules) reports zero findings, not just zero for those paths.
        failures = self._scan({
            ".git/COMMIT_EDITMSG": _AWS,
            "node_modules/pkg/leak.js": f"const k = '{_GHP}';",
            "README.md": "nothing sensitive here\n",
        })
        self.assertEqual(failures, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
