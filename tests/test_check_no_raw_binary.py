#!/usr/bin/env python3
"""Unit tests for scripts/check_no_raw_binary.py — the content-based no-raw-binary
guard (closes the extensionless-binary hole that `.gitattributes` globs miss).

The checker is a standalone script (not part of the installed package), so it is
loaded by path via importlib. Each test builds a throwaway real git repo (git
commands need an actual `.git` to run `check-attr`/`diff --cached`/`ls-files`
against) — never the real repo tree.

Coverage (acceptance-mapped):
    1. Extensionless binary, over threshold, NOT LFS-routed -> FAIL (the hole
       an extension-based `.gitattributes` glob cannot see).
    2. Text file, even large -> PASS (not binary).
    3. Binary that IS LFS-routed (matched by `.gitattributes`) -> PASS.
    4. Binary under threshold -> PASS.
    5. `--staged` vs `--tracked` candidate-set selection is correct.
"""
from __future__ import annotations

import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path

_CHECKER = Path(__file__).resolve().parent.parent / "scripts" / "check_no_raw_binary.py"
_spec = importlib.util.spec_from_file_location("check_no_raw_binary", _CHECKER)
cnrb = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cnrb)

_THRESHOLD = 1000  # small test threshold; production default is 5 MiB (DEFAULT_MAX_BYTES)


def _run_git(root: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True)


def _init_repo(root: Path) -> None:
    _run_git(root, "init", "-q")
    _run_git(root, "config", "user.email", "test@example.com")
    _run_git(root, "config", "user.name", "Test")


class NoRawBinaryTests(unittest.TestCase):
    def _repo(self) -> Path:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        _init_repo(root)
        return root

    def test_extensionless_binary_over_threshold_not_lfs_fails(self):
        # This is the whole point: no extension for a .gitattributes glob to match.
        root = self._repo()
        blob = root / "blob"
        blob.write_bytes(b"\x00" + b"x" * (_THRESHOLD + 10))
        _run_git(root, "add", "blob")
        candidates = cnrb.staged_candidates(root)
        failures = cnrb.scan(root, candidates, _THRESHOLD)
        self.assertTrue(any("blob" in f for f in failures), msg=str(failures))

    def test_non_utf8_binary_over_threshold_not_lfs_fails(self):
        # Second is-binary branch: no NUL byte, but the prefix fails UTF-8 decode
        # (e.g. a Latin-1 / UTF-16 blob). Distinct code path from the NUL check.
        root = self._repo()
        blob = root / "latin_blob"
        blob.write_bytes(b"\xff\xfe" * (_THRESHOLD + 10))  # invalid UTF-8, contains no NUL
        _run_git(root, "add", "latin_blob")
        candidates = cnrb.staged_candidates(root)
        failures = cnrb.scan(root, candidates, _THRESHOLD)
        self.assertTrue(any("latin_blob" in f for f in failures), msg=str(failures))

    def test_text_file_even_large_passes(self):
        root = self._repo()
        text = root / "big.txt"
        text.write_text("hello world\n" * (_THRESHOLD // 10), encoding="utf-8")
        _run_git(root, "add", "big.txt")
        candidates = cnrb.staged_candidates(root)
        failures = cnrb.scan(root, candidates, _THRESHOLD)
        self.assertEqual(failures, [])

    def test_lfs_routed_binary_passes(self):
        root = self._repo()
        (root / ".gitattributes").write_text("*.bin filter=lfs diff=lfs merge=lfs -text\n", encoding="utf-8")
        probe = root / "probe.bin"
        probe.write_bytes(b"\x00" + b"x" * (_THRESHOLD + 10))
        _run_git(root, "add", ".gitattributes", "probe.bin")
        candidates = cnrb.staged_candidates(root)
        failures = cnrb.scan(root, candidates, _THRESHOLD)
        self.assertEqual(failures, [])

    def test_binary_under_threshold_passes(self):
        root = self._repo()
        small = root / "small_blob"
        small.write_bytes(b"\x00" + b"x" * 10)
        _run_git(root, "add", "small_blob")
        candidates = cnrb.staged_candidates(root)
        failures = cnrb.scan(root, candidates, _THRESHOLD)
        self.assertEqual(failures, [])

    def test_staged_vs_tracked_candidate_selection(self):
        root = self._repo()
        committed = root / "committed.txt"
        committed.write_text("committed\n", encoding="utf-8")
        _run_git(root, "add", "committed.txt")
        _run_git(root, "commit", "-q", "-m", "seed")

        staged_only = root / "staged_only.txt"
        staged_only.write_text("staged\n", encoding="utf-8")
        _run_git(root, "add", "staged_only.txt")

        untracked = root / "untracked.txt"
        untracked.write_text("untracked\n", encoding="utf-8")

        staged = set(cnrb.staged_candidates(root))
        tracked = set(cnrb.tracked_candidates(root))

        # `git ls-files` lists the INDEX (tracked = added-or-committed), so a
        # staged-but-uncommitted file is tracked too — only genuinely untracked
        # content is excluded from --tracked.
        self.assertEqual(staged, {"staged_only.txt"})
        self.assertEqual(tracked, {"committed.txt", "staged_only.txt"})
        self.assertNotIn("untracked.txt", staged)
        self.assertNotIn("untracked.txt", tracked)


if __name__ == "__main__":
    unittest.main(verbosity=2)
