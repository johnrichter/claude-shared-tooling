#!/usr/bin/env python3
"""Unit tests for tooling/version-guard: SC-VERSIONING tag-prefix/module-path parity
(version_guard.tag), the Rust path-dependency scan (version_guard.deps), and the
canonical tag-and-release command rendering (version_guard.commands).

Fixture repos are built under tmp_path so each case controls exactly which manifests
exist — the real checkout's own tree is exercised separately in
test_version_guard_cli.py's real-repo case.
"""
from __future__ import annotations

import sys
import unittest
from pathlib import Path

_VERSION_GUARD_DIR = Path(__file__).resolve().parent.parent / "tooling" / "version-guard"
sys.path.insert(0, str(_VERSION_GUARD_DIR))

from version_guard import commands as commands_mod  # noqa: E402
from version_guard.deps import scan_manifest, scan_repo  # noqa: E402
from version_guard.tag import TagError, check_tag, is_module_root, parse_tag  # noqa: E402


def _write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class ParseTagTests(unittest.TestCase):
    def test_bare_version_parses_with_empty_prefix(self) -> None:
        parsed = parse_tag("v0.2.2")
        self.assertEqual(parsed.prefix, "")
        self.assertEqual(parsed.version, "v0.2.2")

    def test_bare_version_without_v_parses(self) -> None:
        parsed = parse_tag("0.2.2")
        self.assertEqual(parsed.prefix, "")

    def test_module_prefixed_version_parses(self) -> None:
        parsed = parse_tag("go/git/v1.2.0")
        self.assertEqual(parsed.prefix, "go/git")
        self.assertEqual(parsed.version, "v1.2.0")

    def test_version_without_leading_v_parses(self) -> None:
        parsed = parse_tag("go/git/1.2.0")
        self.assertEqual(parsed.version, "1.2.0")

    def test_rejects_no_version_segment(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("nightly")

    def test_rejects_malformed_version_after_slash(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("go/git-v1.2.0")  # no '/' before the version; final segment isn't X.Y.Z

    def test_rejects_two_part_version(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("go/git/v1.2")

    def test_rejects_four_part_version(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("go/git/v1.2.0.0")

    def test_rejects_empty_prefix_with_trailing_slash(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("/v1.2.0")

    def test_rejects_non_numeric_version(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("go/git/vX.Y.Z")

    def test_rejects_empty_string(self) -> None:
        with self.assertRaises(TagError):
            parse_tag("")


class IsModuleRootTests(unittest.TestCase):
    def test_repo_root_is_module_when_pyproject_present(self) -> None:
        repo = self._repo()
        _write(repo / "pyproject.toml", "[project]\nname = 'x'\n")
        self.assertTrue(is_module_root(repo, ""))

    def test_repo_root_is_not_module_without_pyproject(self) -> None:
        repo = self._repo()
        self.assertFalse(is_module_root(repo, ""))

    def test_go_module_recognized_by_go_mod(self) -> None:
        repo = self._repo()
        _write(repo / "go" / "git" / "go.mod", "module git\n")
        self.assertTrue(is_module_root(repo, "go/git"))

    def test_rust_crate_recognized_by_cargo_toml(self) -> None:
        repo = self._repo()
        _write(repo / "rust" / "bm25" / "Cargo.toml", "[package]\nname = 'bm25'\n")
        self.assertTrue(is_module_root(repo, "rust/bm25"))

    def test_schema_module_recognized_as_immediate_child_of_schemas(self) -> None:
        repo = self._repo()
        (repo / "schemas" / "model-roster").mkdir(parents=True)
        self.assertTrue(is_module_root(repo, "schemas/model-roster"))

    def test_schema_module_rejects_nested_grandchild(self) -> None:
        repo = self._repo()
        (repo / "schemas" / "model-roster" / "deep").mkdir(parents=True)
        self.assertFalse(is_module_root(repo, "schemas/model-roster/deep"))

    def test_rejects_nonexistent_directory(self) -> None:
        repo = self._repo()
        self.assertFalse(is_module_root(repo, "go/nope"))

    def test_rejects_container_directory_with_children_but_no_manifest(self) -> None:
        """`go/` itself holds every Go module but carries no manifest of its own —
        it must never be an accepted tag prefix on its own."""
        repo = self._repo()
        _write(repo / "go" / "git" / "go.mod", "module git\n")
        self.assertFalse(is_module_root(repo, "go"))

    def test_workspace_only_cargo_toml_is_not_a_module(self) -> None:
        """A `Cargo.toml` with a `[workspace]` table (no `[package]`) marks a
        multi-crate container, not a releasable module — same category as `go/`.
        Regression for the real tree's `rust/Cargo.toml` (workspace root, no
        `[package]`) being currently misclassified as a module root."""
        repo = self._repo()
        _write(repo / "rust" / "Cargo.toml", "[workspace]\nmembers = ['bm25']\n")
        _write(repo / "rust" / "bm25" / "Cargo.toml", "[package]\nname = 'bm25'\n")
        self.assertFalse(
            is_module_root(repo, "rust"),
            "rust/Cargo.toml is a [workspace] manifest with no [package] table; "
            "'rust' is a container directory, not a module — is_module_root must not "
            "accept it just because *a* Cargo.toml file sits there",
        )

    def _repo(self) -> Path:
        import tempfile

        tmp = tempfile.mkdtemp()
        self.addCleanup(__import__("shutil").rmtree, tmp, True)
        return Path(tmp)


class CheckTagTests(unittest.TestCase):
    def setUp(self) -> None:
        import shutil
        import tempfile

        self._tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self._tmp, True)
        self.repo = Path(self._tmp)
        _write(self.repo / "pyproject.toml", "[project]\nname = 'x'\n")
        _write(self.repo / "go" / "git" / "go.mod", "module git\n")
        (self.repo / "schemas" / "model-roster").mkdir(parents=True)

    def test_accepts_matching_module_prefix(self) -> None:
        check_tag(self.repo, "go/git/v1.2.0")  # must not raise

    def test_accepts_bare_top_level_tag(self) -> None:
        check_tag(self.repo, "v0.2.2")  # must not raise

    def test_accepts_schema_module_tag(self) -> None:
        check_tag(self.repo, "schemas/model-roster/v1.0.0")  # must not raise

    def test_rejects_prefix_naming_no_module(self) -> None:
        with self.assertRaises(TagError):
            check_tag(self.repo, "go/nope/v1.2.0")

    def test_sibling_module_does_not_interfere(self) -> None:
        """A sibling module's own manifest must not affect this module's tag check
        — go/other existing must not change whether go/git/... conforms."""
        _write(self.repo / "go" / "other" / "go.mod", "module other\n")
        check_tag(self.repo, "go/git/v1.2.0")  # must not raise
        with self.assertRaises(TagError):
            check_tag(self.repo, "go/nope/v1.2.0")  # still no module at go/nope

    def test_rejects_partial_prefix(self) -> None:
        with self.assertRaises(TagError):
            check_tag(self.repo, "go/v1.2.0")

    def test_rejects_bare_tag_without_top_level_manifest(self) -> None:
        (self.repo / "pyproject.toml").unlink()
        with self.assertRaises(TagError):
            check_tag(self.repo, "v0.2.2")

    def test_rejects_malformed_tag(self) -> None:
        with self.assertRaises(TagError):
            check_tag(self.repo, "not-a-tag")


class ScanManifestTests(unittest.TestCase):
    def test_no_violations_for_git_tag_dependency(self) -> None:
        manifest = self._manifest(
            """
            [package]
            name = "x"

            [dependencies]
            other = { git = "https://example.com/other.git", tag = "v1.0.0" }
            serde = "1.0"
            """
        )
        self.assertEqual(scan_manifest(manifest), [])

    def test_named_path_dependency_flagged(self) -> None:
        manifest = self._manifest(
            """
            [dependencies]
            facetquery = { path = "../facetquery" }
            """
        )
        violations = scan_manifest(manifest)
        self.assertEqual(len(violations), 1)
        self.assertEqual(violations[0].dependency, "facetquery")
        self.assertEqual(violations[0].path, "../facetquery")

    def test_dev_dependencies_section_flagged(self) -> None:
        manifest = self._manifest(
            """
            [dev-dependencies]
            fixture = { path = "../fixture" }
            """
        )
        self.assertEqual(len(scan_manifest(manifest)), 1)

    def test_build_dependencies_section_flagged(self) -> None:
        manifest = self._manifest(
            """
            [build-dependencies]
            fixture = { path = "../fixture" }
            """
        )
        self.assertEqual(len(scan_manifest(manifest)), 1)

    def test_target_scoped_dependencies_section_flagged(self) -> None:
        manifest = self._manifest(
            """
            [target.'cfg(unix)'.dependencies]
            fixture = { path = "../fixture" }
            """
        )
        self.assertEqual(len(scan_manifest(manifest)), 1)

    def test_dotted_subtable_dependency_flagged(self) -> None:
        """FINDING: `[dependencies.<name>]` + a bare `path = "..."` line is a
        standard, common Cargo path-dependency idiom, and the README explicitly
        claims dotted subtables are covered — but the `key == "path"` branch in
        scan_manifest re-applies `_PATH_KEY_RE` (which expects a full
        `path = "..."` string) to `value` alone (just `"../fixture"`), which can
        never match. This form is currently NOT flagged: a real bypass of the
        no-path-dependency rule via a common, valid TOML shape."""
        manifest = self._manifest(
            """
            [dependencies.fixture]
            path = "../fixture"
            """
        )
        self.assertEqual(
            len(scan_manifest(manifest)), 1,
            "[dependencies.<name>] dotted-subtable path dependency not flagged — "
            "see docstring; scan_manifest's direct-path branch is dead code",
        )

    def test_workspace_dependencies_section_flagged(self) -> None:
        manifest = self._manifest(
            """
            [workspace.dependencies]
            fixture = { path = "../fixture" }
            """
        )
        self.assertEqual(len(scan_manifest(manifest)), 1)

    def test_commented_out_path_dependency_not_flagged(self) -> None:
        manifest = self._manifest(
            """
            [dependencies]
            # fixture = { path = "../fixture" }
            serde = "1.0"
            """
        )
        self.assertEqual(scan_manifest(manifest), [])

    def test_path_key_outside_dependency_section_not_flagged(self) -> None:
        """A `path = "..."` key can legitimately appear in a non-dependency
        section (e.g. `[package]` `path` isn't standard Cargo, but any
        similarly-named key elsewhere must not false-positive)."""
        manifest = self._manifest(
            """
            [package.metadata.docs.rs]
            path = "some/doc/path"
            """
        )
        self.assertEqual(scan_manifest(manifest), [])

    def test_multiple_violations_in_one_manifest_all_reported(self) -> None:
        manifest = self._manifest(
            """
            [dependencies]
            a = { path = "../a" }
            b = { path = "../b" }
            """
        )
        self.assertEqual(len(scan_manifest(manifest)), 2)

    def test_known_gap_multiline_inline_table_not_caught(self) -> None:
        """Documented limitation (README): a path dependency spread across a
        multi-line inline table is NOT caught by the line-oriented scan. This
        pins the current (accepted-risk) behavior so a future change to the
        scan is a deliberate, visible decision, not a silent regression."""
        manifest = self._manifest(
            """
            [dependencies.fixture]
            path =
                "../fixture"
            """
        )
        self.assertEqual(scan_manifest(manifest), [])

    def _manifest(self, text: str) -> Path:
        import shutil
        import tempfile
        import textwrap

        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        path = Path(tmp) / "Cargo.toml"
        path.write_text(textwrap.dedent(text), encoding="utf-8")
        return path


class ScanRepoTests(unittest.TestCase):
    def test_finds_violations_across_multiple_manifests(self) -> None:
        import shutil
        import tempfile

        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        _write(tmp / "a" / "Cargo.toml", "[dependencies]\nx = { path = \"../x\" }\n")
        _write(tmp / "b" / "Cargo.toml", "[dependencies]\nserde = \"1.0\"\n")
        violations = scan_repo(tmp)
        self.assertEqual(len(violations), 1)
        self.assertTrue(str(violations[0].manifest).endswith("a/Cargo.toml"))

    def test_clean_repo_reports_no_violations(self) -> None:
        import shutil
        import tempfile

        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, True)
        _write(tmp / "a" / "Cargo.toml", "[dependencies]\nserde = \"1.0\"\n")
        self.assertEqual(scan_repo(tmp), [])


class CommandsTests(unittest.TestCase):
    def test_module_tag_name(self) -> None:
        self.assertEqual(commands_mod.tag_name("go/git", "1.2.0"), "go/git/v1.2.0")

    def test_top_level_tag_name(self) -> None:
        self.assertEqual(commands_mod.tag_name("", "0.2.2"), "v0.2.2")

    def test_tag_name_normalizes_leading_v_in_version_input(self) -> None:
        self.assertEqual(commands_mod.tag_name("go/git", "v1.2.0"), "go/git/v1.2.0")

    def test_render_produces_exact_three_command_sequence(self) -> None:
        plan = commands_mod.render("go/git", "1.2.0", commit="abc123")
        self.assertEqual(plan.tag, "go/git/v1.2.0")
        self.assertEqual(
            plan.commands,
            (
                'git tag -a go/git/v1.2.0 -m "go/git/v1.2.0" abc123',
                "git push origin go/git/v1.2.0",
                'gh release create go/git/v1.2.0 --title "go/git/v1.2.0" --notes "Release go/git/v1.2.0."',
            ),
        )

    def test_render_defaults_commit_to_head(self) -> None:
        plan = commands_mod.render("go/git", "1.2.0")
        self.assertIn("HEAD", plan.commands[0])

    def test_render_top_level_module(self) -> None:
        plan = commands_mod.render("", "0.2.2")
        self.assertEqual(plan.tag, "v0.2.2")


if __name__ == "__main__":
    unittest.main()
