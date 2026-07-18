#!/usr/bin/env python3
"""CLI-level regression for scripts/check_no_raw_binary.py — invokes the checker
as a SUBPROCESS (not via import) against a throwaway real git repo, exercising
the exact path a developer or CI hits: `python3 check_no_raw_binary.py --staged`.

Complements test_check_no_raw_binary.py's import-based unit tests by proving the
argv/exit-code/stdout contract end-to-end, not just the internal `scan()` logic.

Byte-identical across every repo that adopts this guard (the canonical
no-raw-binary artifact) — keep all copies in sync verbatim.
"""
from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_CHECKER = Path(__file__).resolve().parent.parent / "scripts" / "check_no_raw_binary.py"

# Fragment-assembled to avoid an embedded raw-binary-looking literal in this
# source file: one NUL byte followed by filler, built up at runtime.
_NUL = bytes.fromhex("00")
_FILLER = b"x" * 2048


def _run_git(root: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True)


def _run_checker(root: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(_CHECKER), *args, "--root", str(root)],
        capture_output=True, text=True, check=False,
    )


def _init_repo(root: Path) -> None:
    _run_git(root, "init", "-q")
    _run_git(root, "config", "user.email", "test@example.com")
    _run_git(root, "config", "user.name", "Test")


class CliSubprocessTests(unittest.TestCase):
    def _repo(self) -> Path:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        _init_repo(root)
        return root

    def test_extensionless_binary_fails_via_subprocess(self):
        root = self._repo()
        (root / "blob").write_bytes(_NUL + _FILLER)
        _run_git(root, "add", "blob")
        result = _run_checker(root, "--staged", "--max-bytes", "1000")
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("blob", result.stdout)

    def test_lfs_routed_binary_passes_via_subprocess(self):
        root = self._repo()
        (root / ".gitattributes").write_text("*.bin filter=lfs diff=lfs merge=lfs -text\n", encoding="utf-8")
        (root / "probe.bin").write_bytes(_NUL + _FILLER)
        _run_git(root, "add", ".gitattributes", "probe.bin")
        result = _run_checker(root, "--staged", "--max-bytes", "1000")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)

    def test_text_file_passes_via_subprocess(self):
        root = self._repo()
        (root / "big.txt").write_text("hello world\n" * 300, encoding="utf-8")
        _run_git(root, "add", "big.txt")
        result = _run_checker(root, "--staged", "--max-bytes", "1000")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
