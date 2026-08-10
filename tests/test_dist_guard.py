#!/usr/bin/env python3
"""Regression for tooling/dist-guard — the SC-DISTRIBUTION no-committed-binaries guard.

`scan` runs as a SUBPROCESS against a throwaway real git repo (the exact argv/exit-code
contract CI hits); the allowlist producer's invariants (at-most-one, empty steady state,
byte-for-byte currency) run in-process against the committed artifact.
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_DIST_GUARD = Path(__file__).resolve().parent.parent / "tooling" / "dist-guard"
_CHECK = _DIST_GUARD / "check.py"
_ALLOWLIST_JSON = _DIST_GUARD / "allowlist.json"
sys.path.insert(0, str(_DIST_GUARD))
from dist_guard import allowlist  # noqa: E402

# Assembled at runtime so this source carries no embedded binary-looking literal.
_NUL = bytes.fromhex("00")
_BINARY = _NUL + b"x" * 2048


def _run_git(root: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True)


def _run_guard(root: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(_CHECK), *args, "--root", str(root)],
        capture_output=True, text=True, check=False,
    )


class ScanSubprocessTests(unittest.TestCase):
    def _repo(self) -> Path:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        _run_git(root, "init", "-q")
        _run_git(root, "config", "user.email", "test@example.com")
        _run_git(root, "config", "user.name", "Test")
        _run_git(root, "config", "core.excludesFile", "/dev/null")  # ignore any host global gitignore
        return root

    def _commit_executable(self, root: Path, rel: str, data: bytes) -> None:
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(data)
        p.chmod(0o755)
        _run_git(root, "add", rel)
        _run_git(root, "update-index", "--chmod=+x", rel)  # force 100755 regardless of core.filemode

    def test_planted_binary_rejected(self):
        root = self._repo()
        self._commit_executable(root, "bin/planted", _BINARY)
        result = _run_guard(root, "scan")
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("bin/planted", result.stderr)

    def test_clean_tree_accepted(self):
        # Post-deletion steady state: no committed binary, empty allowlist -> scan exits 0.
        root = self._repo()
        self._commit_executable(root, "bin/run.sh", b"#!/bin/sh\necho hi\n")
        result = _run_guard(root, "scan")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)

    def test_executable_text_ignored(self):
        root = self._repo()
        self._commit_executable(root, "bin/run.sh", b"#!/bin/sh\necho hi\n")
        result = _run_guard(root, "scan")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)

    def test_non_executable_binary_ignored(self):
        # A committed binary asset without the exec bit is check_no_raw_binary.py's concern, not this guard's.
        root = self._repo()
        p = root / "asset.dat"
        p.write_bytes(_BINARY)
        _run_git(root, "add", "asset.dat")
        result = _run_guard(root, "scan")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)


class AllowlistProducerTests(unittest.TestCase):
    def test_render_matches_committed_json_byte_for_byte(self):
        self.assertEqual(allowlist.render(), _ALLOWLIST_JSON.read_text(encoding="utf-8"))

    def test_committed_allowlist_is_empty(self):
        self.assertEqual(allowlist.load(_ALLOWLIST_JSON), frozenset())

    def test_zero_or_one_entry_is_valid(self):
        # At-most-one lower boundary: empty (steady state) and a lone entry both render.
        original = allowlist.ENTRIES
        try:
            allowlist.ENTRIES = []
            allowlist.render()  # must not raise
            allowlist.ENTRIES = [{"path": "go/.bin/one", "reason": "x"}]
            allowlist.render()  # must not raise
        finally:
            allowlist.ENTRIES = original

    def test_more_than_one_entry_is_a_hard_error(self):
        original = allowlist.ENTRIES
        allowlist.ENTRIES = [
            {"path": "go/.bin/one", "reason": "x"},
            {"path": "go/.bin/two", "reason": "y"},
        ]
        try:
            with self.assertRaises(ValueError):
                allowlist.render()
        finally:
            allowlist.ENTRIES = original

    def test_check_reports_committed_allowlist_current(self):
        result = _run_guard(_DIST_GUARD, "check")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
