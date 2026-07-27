#!/usr/bin/env python3
"""CLI-level regression for tooling/version-guard/check.py — invokes it as a SUBPROCESS
against throwaway fixture repos, exercising the exact path CI hits: argv, exit codes,
and stdout/stderr. Complements test_version_guard.py's import-based unit tests.
"""
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_CHECK = Path(__file__).resolve().parent.parent / "tooling" / "version-guard" / "check.py"
_REPO_ROOT = Path(__file__).resolve().parent.parent


def _run(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(_CHECK), *args],
        capture_output=True, text=True, check=False,
    )


def _write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class CheckTagCliTests(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        self.repo = Path(tmp)
        _write(self.repo / "pyproject.toml", "[project]\nname = 'x'\n")
        _write(self.repo / "go" / "git" / "go.mod", "module git\n")

    def test_accepts_conformant_module_tag(self) -> None:
        result = _run("check-tag", "go/git/v1.2.0", "--repo-root", str(self.repo))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("conforms", result.stdout)

    def test_rejects_mismatched_prefix_tag_fixture(self) -> None:
        """Test-strategy fixture: a tag prefix that names no real module."""
        result = _run("check-tag", "go/nonexistent/v1.2.0", "--repo-root", str(self.repo))
        self.assertEqual(result.returncode, 1)
        self.assertIn("SC-VERSIONING", result.stderr)

    def test_rejects_module_prefix_one_level_short(self) -> None:
        result = _run("check-tag", "go/v1.2.0", "--repo-root", str(self.repo))
        self.assertEqual(result.returncode, 1)

    def test_rejects_malformed_version(self) -> None:
        result = _run("check-tag", "go/git/vX.Y.Z", "--repo-root", str(self.repo))
        self.assertEqual(result.returncode, 1)

    def test_missing_tag_argument_is_usage_error(self) -> None:
        result = _run("check-tag", "--repo-root", str(self.repo))
        self.assertEqual(result.returncode, 2)


class CheckDepsCliTests(unittest.TestCase):
    def test_accepts_conformant_git_tag_dependency_fixture(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        _write(
            tmp / "crate" / "Cargo.toml",
            '[package]\nname = "crate"\n\n[dependencies]\n'
            'other = { git = "https://example.com/other.git", tag = "v1.0.0" }\n',
        )
        result = _run("check-deps", "--repo-root", str(tmp))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no Rust path/relative dependencies", result.stdout)

    def test_rejects_path_dep_cargo_fixture(self) -> None:
        """Test-strategy fixture: a Cargo.toml with a path/relative dependency."""
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        _write(
            tmp / "crate" / "Cargo.toml",
            '[package]\nname = "crate"\n\n[dependencies]\nsibling = { path = "../sibling" }\n',
        )
        result = _run("check-deps", "--repo-root", str(tmp))
        self.assertEqual(result.returncode, 1)
        self.assertIn("sibling", result.stderr)
        self.assertIn("path dependency", result.stderr)

    def test_rejects_relative_dot_dot_path_variants(self) -> None:
        for rel_path in ("../sibling", "../../sibling", "./local"):
            with self.subTest(rel_path=rel_path):
                tmp = Path(tempfile.mkdtemp())
                self.addCleanup(shutil.rmtree, tmp, True)
                _write(
                    tmp / "crate" / "Cargo.toml",
                    f'[dependencies]\nsibling = {{ path = "{rel_path}" }}\n',
                )
                result = _run("check-deps", "--repo-root", str(tmp))
                self.assertEqual(result.returncode, 1, f"path={rel_path!r} not flagged")

    def test_empty_repo_has_no_manifests_and_passes(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        result = _run("check-deps", "--repo-root", str(tmp))
        self.assertEqual(result.returncode, 0)

    def test_unreadable_manifest_is_never_a_silent_pass(self) -> None:
        """An unreadable manifest must not be treated as 'no violations found'.
        DOC MISMATCH: README/check.py docstring promise exit 2 ('unreadable
        repo/manifest' is a usage error), but DepsError is caught by the same
        (TagError, DepsError) branch as a real violation and returns 1 — so this
        asserts the actual (safe, non-silent) contract, not the documented one."""
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        manifest = tmp / "crate" / "Cargo.toml"
        manifest.parent.mkdir(parents=True)
        manifest.mkdir()  # a directory named Cargo.toml: unreadable as a file
        result = _run("check-deps", "--repo-root", str(tmp))
        self.assertNotEqual(result.returncode, 0, "unreadable manifest silently passed")
        self.assertEqual(
            result.returncode, 1,
            "actual exit code for an unreadable manifest; README/docstring document "
            "this case as exit 2 — behavior and documentation disagree",
        )


class CommandsCliTests(unittest.TestCase):
    def test_prints_exact_three_line_command_sequence(self) -> None:
        result = _run("commands", "--module", "go/git", "--version", "1.2.0", "--commit", "abc123")
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.strip("\n").split("\n")
        self.assertEqual(
            lines,
            [
                'git tag -a go/git/v1.2.0 -m "go/git/v1.2.0" abc123',
                "git push origin go/git/v1.2.0",
                'gh release create go/git/v1.2.0 --title "go/git/v1.2.0" --notes "Release go/git/v1.2.0."',
            ],
        )

    def test_missing_version_is_usage_error(self) -> None:
        result = _run("commands", "--module", "go/git")
        self.assertEqual(result.returncode, 2)


class RealRepoRegressionTests(unittest.TestCase):
    """Runs the guard against this checkout's own tree — the surface CI actually
    exercises, not a synthetic fixture. Findings here are real-tree findings."""

    def test_known_module_tags_conform(self) -> None:
        for tag in ("go/git/v1.2.0", "schemas/model-roster/v1.0.0", "v0.2.2"):
            with self.subTest(tag=tag):
                result = _run("check-tag", tag, "--repo-root", str(_REPO_ROOT))
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_workspace_container_directory_rejected_as_module_prefix(self) -> None:
        """FINDING: rust/Cargo.toml is a [workspace] manifest with no [package]
        table; 'rust' is a container directory analogous to 'go' or 'schemas'
        (both correctly rejected below), not a releasable module. The current
        implementation accepts it because it only checks for *a* Cargo.toml file,
        not a [package] table — this assertion currently FAILS."""
        result = _run("check-tag", "rust/v1.2.0", "--repo-root", str(_REPO_ROOT))
        self.assertEqual(
            result.returncode, 1,
            "check-tag accepted 'rust/v1.2.0' (exit 0) — rust/Cargo.toml is a "
            "[workspace]-only manifest, so 'rust' is a container directory, not a "
            "module; compare go/v1.2.0 and schemas/v1.0.0 below, both correctly rejected",
        )

    def test_sibling_container_directories_correctly_rejected(self) -> None:
        for tag in ("go/v1.2.0", "schemas/v1.0.0"):
            with self.subTest(tag=tag):
                result = _run("check-tag", tag, "--repo-root", str(_REPO_ROOT))
                self.assertEqual(result.returncode, 1)

    def test_real_tree_has_no_path_dependencies(self) -> None:
        """rust/frontmatter's former `path = "../facetquery"` dependency (the
        README's one-time "known violation") is now a git-tag dependency
        (`rust/facetquery/v0.1.0`), resolved locally via `[patch]` in
        `rust/Cargo.toml`. Pins that the real tree is clean against check-deps."""
        result = _run("check-deps", "--repo-root", str(_REPO_ROOT))
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
