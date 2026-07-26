#!/usr/bin/env python3
"""Unit tests for codegov-lint: the mechanical code-authoring gate.

The package lives at `tooling/codegov-lint/codegov_lint/` (a hyphenated parent directory,
so it is not importable as a dotted path); that directory is put on `sys.path` here, exactly
as `check.py` does.

Coverage:
    1. Each banned-content rule class (PORT-ARCHAEOLOGY, MILESTONE-ID, DEAD-CODE) fires on a
       planted violation shaped like the real thing it targets.
    2. Prose that opens with a keyword the dead-code rule also matches on ("If", "Return",
       "Use", "For", ...) never trips DEAD-CODE — the regression this suite exists to pin.
    3. Python `#` line comments are scanned for every banned-content rule, not just
       docstrings.
    4. The doc-presence check flags a Go/Rust exported declaration with no doc comment, and
       leaves a documented one alone.
    5. The tool's own source tree is a clean fixture: scanning it end to end reports zero
       violations.
    6. `scan_file` handles a path outside `repo_root` without crashing.
    7. The Python Google-style docstring rule (ruff pydocstyle `D`) flags a planted missing
       docstring and leaves a compliant one clean. Skipped if `ruff` is not on `PATH`, since
       this class runs a real subprocess rather than pure-Python logic.
    8. `cli.main` end to end: `--files` on a clean fixture exits 0, on a planted violation
       exits 1, and `--diff` against an empty-tree-equivalent ref resolves and runs.
"""
from __future__ import annotations

import contextlib
import io
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_PKG_ROOT = Path(__file__).resolve().parent.parent / "tooling" / "codegov-lint"
if str(_PKG_ROOT) not in sys.path:
    sys.path.insert(0, str(_PKG_ROOT))

from codegov_lint import cli, comments, extensions, pydocs, rules, scan  # noqa: E402

_REPO_ROOT = Path(__file__).resolve().parent.parent


def _rule_names(violations) -> set[str]:
    """Return the distinct rule names present in `violations`."""
    return {v.rule for v in violations}


class PortArchaeologyTests(unittest.TestCase):
    """PORT-ARCHAEOLOGY: cross-language source citations in comment text."""

    def test_planted_violations_fire(self):
        """Each citation shape the policy bans is caught."""
        cases = [
            "ported from the python reference implementation",
            "adapted from the rust version of this function",
            "this mirrors the golang implementation exactly",
            "see foo.py:42 for the original logic",
        ]
        for text in cases:
            with self.subTest(text=text):
                violations = rules.scan_comment("f.py", 1, text)
                self.assertIn("PORT-ARCHAEOLOGY", _rule_names(violations))

    def test_ordinary_language_mentions_do_not_fire(self):
        """A language name or the word "written" alone, with no citation shape, is clean."""
        cases = [
            "this module targets Python 3.12 and above",
            "written for readability, not performance",
            "see the design doc for background",
        ]
        for text in cases:
            with self.subTest(text=text):
                violations = rules.scan_comment("f.py", 1, text)
                self.assertNotIn("PORT-ARCHAEOLOGY", _rule_names(violations))


class MilestoneIdTests(unittest.TestCase):
    """MILESTONE-ID: plan/task/spec-criterion ids embedded in comment text."""

    def test_planted_violations_fire(self):
        """Each id shape the policy bans is caught."""
        cases = ["M1.P2.T1 marker left in by mistake", "see Task 7 for context", "SC-DEPPOLICY governs this"]
        for text in cases:
            with self.subTest(text=text):
                violations = rules.scan_comment("f.py", 1, text)
                self.assertIn("MILESTONE-ID", _rule_names(violations))

    def test_ordinary_language_does_not_fire(self):
        """A bare "M" used as a unit/variable name, not an id, is clean."""
        text = "retry up to M times before giving up"
        violations = rules.scan_comment("f.py", 1, text)
        self.assertNotIn("MILESTONE-ID", _rule_names(violations))


class DeadCodeTests(unittest.TestCase):
    """DEAD-CODE: a comment that is actually a line of commented-out code."""

    def test_planted_violations_fire(self):
        """Every code shape the rule targets is caught.

        Covers block openers, terminated statements, calls, imports, and a lone
        closing brace.
        """
        cases = [
            "if x > 5:",
            "for i in range(10):",
            "return compute_total(items);",
            "class Foo:",
            "def foo():",
            "public static void main(String[] args) {",
            "import os, sys",
            "from foo.bar import baz, qux",
            "use std::io;",
            "package main",
            "#include <stdio.h>",
            "console.log(x);",
            "x = compute();",
            "compute()",
            "}",
        ]
        for text in cases:
            with self.subTest(text=text):
                violations = rules.scan_comment("f.py", 1, text)
                self.assertIn("DEAD-CODE", _rule_names(violations), f"expected DEAD-CODE for {text!r}")

    def test_prose_opening_with_a_code_keyword_never_fires(self):
        """A sentence opening with a keyword the rule also matches on stays clean.

        Covers "If", "Return", "Use", "For", and similar openers that read as
        ordinary prose rather than code.
        """
        cases = [
            "If unset, defaults to ten.",
            "For each item in the list, process it.",
            "Return the config value once loaded.",
            "Use the config file at startup.",
            "While the server starts up, requests queue.",
            "Import the config before running the tool.",
            "Class methods below are private to this package.",
            "Public methods are listed below.",
            "With retries exhausted, the call fails.",
            "Static analysis catches this earlier.",
        ]
        for text in cases:
            with self.subTest(text=text):
                violations = rules.scan_comment("f.py", 1, text)
                self.assertNotIn("DEAD-CODE", _rule_names(violations), f"unexpected DEAD-CODE for {text!r}")


class PythonHashCommentExtractionTests(unittest.TestCase):
    """Python `#` line comments, not just docstrings, feed every banned-content rule."""

    def test_hash_comments_are_scanned_alongside_docstrings(self):
        """A `#` comment inside a function body is extracted next to the module docstring."""
        source = (
            '"""Module docstring, left clean."""\n\n\ndef foo():\n'
            "    # ported from the python reference implementation\n"
            "    return 1\n"
        )
        found = comments.extract(source, "py")
        texts = [c.text for c in found]
        self.assertTrue(any("ported from" in t for t in texts))
        violations = [v for c in found for v in rules.scan_comment("f.py", c.line, c.text)]
        self.assertIn("PORT-ARCHAEOLOGY", _rule_names(violations))

    def test_hash_comment_milestone_and_dead_code_are_scanned(self):
        """A milestone id and a dead-code statement, both in `#` comments, are both caught."""
        source = "# M1.P2.T1 marker\n# x = compute();\nvalue = 1\n"
        found = comments.extract(source, "py")
        violations = [v for c in found for v in rules.scan_comment("f.py", c.line, c.text)]
        self.assertIn("MILESTONE-ID", _rule_names(violations))
        self.assertIn("DEAD-CODE", _rule_names(violations))

    def test_prose_hash_comment_is_clean(self):
        """An ordinary `#` comment sentence produces no violations."""
        source = "# If unset, defaults to ten.\nvalue = 1\n"
        found = comments.extract(source, "py")
        violations = [v for c in found for v in rules.scan_comment("f.py", c.line, c.text)]
        self.assertEqual(violations, [])


class DocPresenceTests(unittest.TestCase):
    """MISSING-API-DOC: exported Go/Rust declarations without a doc comment."""

    def test_go_exported_func_without_doc_is_flagged(self):
        """An exported Go func with no doc comment on the line above it is flagged."""
        lines = ["package foo", "", "func Bar() int {", "\treturn 1", "}"]
        violations = rules.scan_doc_presence("f.go", "go", lines)
        self.assertEqual(_rule_names(violations), {"MISSING-API-DOC"})

    def test_go_exported_func_with_doc_is_clean(self):
        """An exported Go func documented on the line above it is clean."""
        lines = ["package foo", "", "// Bar returns a constant.", "func Bar() int {", "\treturn 1", "}"]
        violations = rules.scan_doc_presence("f.go", "go", lines)
        self.assertEqual(violations, [])

    def test_rust_pub_fn_without_doc_is_flagged(self):
        """A `pub fn` with no rustdoc comment on the line above it is flagged."""
        lines = ["pub fn bar() -> i32 {", "    1", "}"]
        violations = rules.scan_doc_presence("f.rs", "rs", lines)
        self.assertEqual(_rule_names(violations), {"MISSING-API-DOC"})

    def test_rust_pub_fn_with_doc_is_clean(self):
        """A `pub fn` documented with `///` on the line above it is clean."""
        lines = ["/// Returns a constant.", "pub fn bar() -> i32 {", "    1", "}"]
        violations = rules.scan_doc_presence("f.rs", "rs", lines)
        self.assertEqual(violations, [])


class CleanFixtureTests(unittest.TestCase):
    """The tool's own source is the clean scaffold acceptance requires zero false positives on."""

    def test_tool_own_source_is_clean(self):
        """Scanning every module the tool ships reports no violations."""
        pkg_dir = _REPO_ROOT / "tooling" / "codegov-lint"
        paths = sorted(p for p in pkg_dir.rglob("*.py") if "__pycache__" not in p.parts)
        violations = scan.scan_files(paths, _REPO_ROOT)
        self.assertEqual(violations, [], f"expected a clean scaffold, got: {violations}")

    def test_scan_is_deterministic(self):
        """Scanning the same file set twice reproduces the same (empty) result."""
        pkg_dir = _REPO_ROOT / "tooling" / "codegov-lint"
        paths = sorted(p for p in pkg_dir.rglob("*.py") if "__pycache__" not in p.parts)
        first = scan.scan_files(paths, _REPO_ROOT)
        second = scan.scan_files(paths, _REPO_ROOT)
        self.assertEqual(first, second)


class OutOfTreePathTests(unittest.TestCase):
    """`scan_file` on a path outside `repo_root` (e.g. an ad hoc `--files` fixture)."""

    def test_scan_file_outside_repo_root_does_not_crash(self):
        """A clean file outside the repo root scans without error."""
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "scratch.py"
            path.write_text('"""Clean docstring."""\n', encoding="utf-8")
            violations = scan.scan_file(path, _REPO_ROOT)
            self.assertEqual(violations, [])

    def test_scan_file_outside_repo_root_still_reports_violations(self):
        """A violation in a file outside the repo root is still reported, not swallowed."""
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "scratch.py"
            path.write_text("# M1.P2.T1 marker\nvalue = 1\n", encoding="utf-8")
            violations = scan.scan_file(path, _REPO_ROOT)
            self.assertIn("MILESTONE-ID", _rule_names(violations))


class ExtensionCoverageTests(unittest.TestCase):
    """Every tracked source extension is either Python or has a comment family."""

    def test_every_source_extension_has_a_comment_family_or_python_handling(self):
        """No extension is treated as source yet left unscanned for lack of a comment family."""
        uncovered = [
            ext
            for ext in extensions.EXTENSIONS
            if ext not in comments._PYTHON_EXTENSIONS and ext not in comments.FAMILY_BY_EXTENSION
        ]
        self.assertEqual(uncovered, [], f"source extensions with no comment coverage: {uncovered}")


@unittest.skipUnless(pydocs.ruff_available(), "ruff not on PATH")
class PythonDocstringRuleTests(unittest.TestCase):
    """MISSING-API-DOC via ruff's pydocstyle rules (Google convention), for real Python source."""

    def test_missing_module_docstring_is_flagged(self):
        """A module with no docstring at all is flagged by ruff's `D` rules."""
        with tempfile.TemporaryDirectory() as tmp:
            repo_root = Path(tmp)
            src = repo_root / "scratch.py"
            src.write_text("def foo():\n    return 1\n", encoding="utf-8")
            with tempfile.TemporaryDirectory() as cfg_dir:
                violations = pydocs.scan_python_docstrings([src], repo_root, Path(cfg_dir) / "ruff.toml")
            self.assertIn("MISSING-API-DOC", _rule_names(violations))

    def test_google_style_docstring_is_clean(self):
        """A module and function both carrying Google-style docstrings pass clean."""
        with tempfile.TemporaryDirectory() as tmp:
            repo_root = Path(tmp)
            src = repo_root / "scratch.py"
            src.write_text(
                '"""Scratch module."""\n\n\ndef foo():\n    """Return one.\n\n    Returns:\n        int: Always 1.\n    """\n    return 1\n',
                encoding="utf-8",
            )
            with tempfile.TemporaryDirectory() as cfg_dir:
                violations = pydocs.scan_python_docstrings([src], repo_root, Path(cfg_dir) / "ruff.toml")
            self.assertEqual(violations, [])


class CliEndToEndTests(unittest.TestCase):
    """`cli.main`: exit codes and file-set resolution, not just the underlying rule functions."""

    def _run(self, argv):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = cli.main(argv)
        return code, out.getvalue()

    def test_files_mode_clean_fixture_exits_zero(self):
        """`--files` on a clean fixture exits 0 and reports no violations."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "scratch.go"
            src.write_text("package foo\n\n// Bar returns a constant.\nfunc Bar() int {\n\treturn 1\n}\n", encoding="utf-8")
            code, output = self._run(["--files", str(src)])
            self.assertEqual(code, 0)
            self.assertIn("no violations", output)

    def test_files_mode_planted_violation_exits_one(self):
        """`--files` on a file carrying a planted violation exits 1 and reports it."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "scratch.py"
            src.write_text("# M1.P2.T1 marker\nvalue = 1\n", encoding="utf-8")
            code, output = self._run(["--files", str(src)])
            self.assertEqual(code, 1)
            self.assertIn("MILESTONE-ID", output)

    def test_diff_mode_against_empty_tree_runs(self):
        """`--diff` against the empty-tree hash (first-push edge case) resolves and runs.

        Uses a throwaway git repo, not this checkout — diffing the real repo against the
        empty tree would surface its entire pre-existing backlog (see the package README's
        "Why --diff, not a full-tree scan" note), not the one planted violation under test.
        """
        empty_tree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
            (repo / "scratch.py").write_text("# M1.P2.T1 marker\nvalue = 1\n", encoding="utf-8")
            subprocess.run(["git", "add", "scratch.py"], cwd=repo, check=True)
            cwd = Path.cwd()
            try:
                os.chdir(repo)
                code, output = self._run(["--diff", empty_tree])
            finally:
                os.chdir(cwd)
            self.assertEqual(code, 1)
            self.assertIn("MILESTONE-ID", output)


if __name__ == "__main__":
    unittest.main()
