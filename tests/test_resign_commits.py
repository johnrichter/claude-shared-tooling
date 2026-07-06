#!/usr/bin/env python3
"""Unit tests for resign_commits.py — stdlib unittest, no network, no third-party deps.

Hermetic on two axes:
    * ephemeral signing — each test builds a throwaway git repo with its own generated ssh
      signing key (never touches the user's keys or ~/.ssh); signed commits verify as `G`
      via a per-repo allowed-signers file, unsigned commits use `--no-gpg-sign`.
    * isolated config — GIT_CONFIG_GLOBAL/SYSTEM are pointed at os.devnull for the duration,
      so the machine's real global config (which may route ssh signing through an external
      agent such as 1Password) can't interfere; git falls back to plain ssh-keygen signing
      with the repo-local key. Both the test's own git calls and the tool's internal ones
      inherit this via os.environ.

Test strategy (mirrors the tool's acceptance criteria):
    1. Regression — a merge with a *recorded conflict resolution* re-signs cleanly and the
       rebuilt merge tree is byte-identical (proves no re-merge, the bug the tool fixes).
    2. Fidelity — over a repo with merges + interleaved unsigned commits: tip tree identical,
       zero unsigned remain, every rebuilt commit's tree + author/committer/date/message match.
    3. compute_base correctness — chained unsigned and parallel-branch unsigned both covered.
    4. CLI — no-unsigned is a clean no-op; --apply moves the branch and is idempotent.

Run with: python3 -m unittest tests.test_resign_commits
       or: python3 -m unittest discover -s tests -p "test_*.py"
"""
from __future__ import annotations

import contextlib
import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from ai_shared_lib_public import resign_commits


def _run(args, cwd, *, env=None, check=True, inp=None):
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=check, env=env, input=inp
    )


def _env(date: str) -> dict:
    """Full environment plus fixed author/committer dates for reproducible commits."""
    return {**os.environ, "GIT_AUTHOR_DATE": date, "GIT_COMMITTER_DATE": date}


class GitRepo:
    """A throwaway git repo with ephemeral ssh commit-signing wired up."""

    def __init__(self, root: Path):
        # Repo work tree and key material live in separate subdirs so the ephemeral key
        # files never show up as untracked entries in `git status` of the repo.
        self.path = str(root / "wt")
        keydir = root / "keys"
        (root / "wt").mkdir(parents=True, exist_ok=True)
        keydir.mkdir(parents=True, exist_ok=True)
        _run(["init", "-b", "main", "-q"], self.path)
        key = keydir / "sign_ed25519"
        subprocess.run(
            ["ssh-keygen", "-t", "ed25519", "-N", "", "-C", "test@example.com", "-f", str(key)],
            check=True, capture_output=True,
        )
        pub = (keydir / "sign_ed25519.pub").read_text().strip()
        allowed = keydir / "allowed_signers"
        allowed.write_text(f'test@example.com namespaces="git" {pub}\n')
        for k, v in {
            "user.name": "Test User",
            "user.email": "test@example.com",
            "gpg.format": "ssh",
            "user.signingkey": f"{key}.pub",
            "gpg.ssh.allowedSignersFile": str(allowed),
            "commit.gpgsign": "false",
        }.items():
            _run(["config", k, v], self.path)

    def git(self, *args, check=True, inp=None):
        return _run(list(args), self.path, check=check, inp=inp)

    def write(self, name, content):
        (Path(self.path) / name).write_text(content)

    def commit(self, name, content, msg, *, sign, date="2026-01-01T00:00:00"):
        self.write(name, content)
        self.git("add", name)
        return self.raw_commit(msg, sign=sign, date=date)

    def raw_commit(self, msg, *, sign, date):
        """Commit whatever is already staged (used for merge-conflict resolutions)."""
        flag = "-S" if sign else "--no-gpg-sign"
        _run(["commit", flag, "-m", msg], self.path, env=_env(date))
        return self.sha()

    def commit_verbatim(self, name, content, msg_bytes: bytes, *, sign, date="2026-01-01T00:00:00"):
        """Commit with `--cleanup=verbatim` so git does NOT strip trailing whitespace or
        collapse blank lines in the message -- needed to test byte-for-byte preservation."""
        self.write(name, content)
        self.git("add", name)
        flag = "-S" if sign else "--no-gpg-sign"
        subprocess.run(
            ["git", "commit", flag, "--cleanup=verbatim", "-F", "-"],
            cwd=self.path, input=msg_bytes, env=_env(date), check=True, capture_output=True,
        )
        return self.sha()

    def raw_message(self, ref) -> bytes:
        """The exact bytes stored in the commit OBJECT's message (not `log --format=%B`,
        which appends its own trailing newline on top of whatever is actually stored)."""
        raw = subprocess.run(
            ["git", "cat-file", "-p", ref], cwd=self.path, capture_output=True, check=True,
        ).stdout
        return raw[raw.index(b"\n\n") + 2:]

    def merge(self, branch, msg, *, date, sign=True):
        args = ["merge", "-S" if sign else "--no-gpg-sign", "--no-ff", "-m", msg, branch]
        _run(args, self.path, env=_env(date))
        return self.sha()

    def sha(self, ref="HEAD"):
        return self.git("rev-parse", ref).stdout.strip()

    def gflag(self, ref="HEAD"):
        return self.git("log", "-1", "--format=%G?", ref).stdout.strip()

    def tree(self, ref):
        return self.git("rev-parse", f"{ref}^{{tree}}").stdout.strip()

    def read(self, name, ref):
        return self.git("show", f"{ref}:{name}").stdout

    def flags(self, ref):
        return self.git("log", "--format=%G?", ref).stdout.split()


def _signing_works(root: Path) -> bool:
    try:
        r = GitRepo(root)
        r.commit("probe", "x", "probe", sign=True)
        return r.gflag() in ("G", "U")
    except Exception:
        return False


class SigningTestCase(unittest.TestCase):
    """Isolate global/system git config, verify signing is available, fresh repo per test."""

    _ISO_KEYS = ("GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM")

    @classmethod
    def setUpClass(cls):
        cls._saved_env = {k: os.environ.get(k) for k in cls._ISO_KEYS}
        for k in cls._ISO_KEYS:
            os.environ[k] = os.devnull
        with tempfile.TemporaryDirectory() as td:
            ok = _signing_works(Path(td) / "probe")
        if not ok:
            cls._restore_env()
            raise unittest.SkipTest("git ssh commit-signing unavailable in this environment")

    @classmethod
    def tearDownClass(cls):
        cls._restore_env()

    @classmethod
    def _restore_env(cls):
        for k, v in cls._saved_env.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v

    def setUp(self):
        self._td = tempfile.TemporaryDirectory()
        self.repo = GitRepo(Path(self._td.name))

    def tearDown(self):
        self._td.cleanup()

    @property
    def cwd(self):
        return self.repo.path

    def _verify_map(self, old_tip, new_tip, base, mapping):
        return {n: ok for n, ok, _ in resign_commits.verify(old_tip, new_tip, base, mapping, cwd=self.cwd)}


class TestConflictMergeRegression(SigningTestCase):
    """The bug this tool exists for: re-signing history containing a merge with a recorded
    conflict resolution. `rebase --rebase-merges` re-runs the merge and re-conflicts;
    commit-tree reuses the recorded tree, so it reproduces the resolved content exactly."""

    def test_conflict_merge_resigns_and_preserves_resolved_tree(self):
        r = self.repo
        r.commit("X", "base\n", "c0 root", sign=True)
        r.git("checkout", "-q", "-b", "branchA")
        r.commit("X", "sideA\n", "a1 on branchA (UNSIGNED)", sign=False)
        r.git("checkout", "-q", "main")
        r.commit("X", "sideB\n", "m1 on main", sign=True)
        r.git("merge", "--no-commit", "--no-ff", "branchA", check=False)  # conflicts on X
        r.write("X", "resolved\n")
        r.git("add", "X")
        old_tip = r.raw_commit("merge branchA into main (conflict resolved)", sign=True, date="2026-01-02T00:00:00")
        old_merge_tree = r.tree(old_tip)
        self.assertEqual(r.read("X", old_tip), "resolved\n")

        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        self.assertEqual(len(unsigned), 1)  # only a1 on branchA
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        new_tip, mapping = resign_commits.rebuild(base, old_tip, cwd=self.cwd)

        self.assertEqual(r.tree(new_tip), old_merge_tree)   # merge tree reproduced -> no re-merge
        self.assertEqual(r.read("X", new_tip), "resolved\n")
        results = self._verify_map(old_tip, new_tip, base, mapping)
        self.assertTrue(all(results.values()), msg=str(results))


class TestFidelity(SigningTestCase):
    def _build_mixed_history(self):
        r = self.repo
        r.commit("a.txt", "0\n", "root", sign=True)
        r.commit("a.txt", "1\n", "c1 (UNSIGNED)", sign=False)
        r.git("checkout", "-q", "-b", "feature")
        r.commit("b.txt", "fa\n", "fa on feature (UNSIGNED)", sign=False)
        r.commit("b.txt", "fb\n", "fb on feature", sign=True)
        r.git("checkout", "-q", "main")
        r.merge("feature", "merge feature", date="2026-01-03T00:00:00")
        r.commit("a.txt", "2\n", "c2 (UNSIGNED)", sign=False)
        return r.sha()

    def test_full_fidelity_over_merge_history(self):
        r = self.repo
        old_tip = self._build_mixed_history()
        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        self.assertEqual(len(unsigned), 3)
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        new_tip, mapping = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        results = self._verify_map(old_tip, new_tip, base, mapping)
        self.assertTrue(all(results.values()), msg=str(results))
        self.assertNotIn("N", r.flags(new_tip))

    def test_author_committer_and_dates_preserved(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True, date="2020-05-05T12:00:00")
        r.commit("f", "1\n", "unsigned one", sign=False, date="2021-06-06T13:00:00")
        old_tip = r.sha()
        fmt = ["log", "-1", "--format=%an|%ae|%ad|%cn|%ce|%cd", "--date=raw"]
        old_ident = r.git(*fmt, old_tip).stdout.strip()
        base = resign_commits.compute_base(resign_commits.find_unsigned("main", cwd=self.cwd), cwd=self.cwd)
        new_tip, _ = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        self.assertEqual(old_ident, r.git(*fmt, new_tip).stdout.strip())
        self.assertIn(r.gflag(new_tip), ("G", "U"))


class TestComputeBase(SigningTestCase):
    def test_chained_unsigned(self):
        r = self.repo
        root = r.commit("f", "0\n", "root", sign=True)
        u1 = r.commit("f", "1\n", "u1 UNSIGNED", sign=False)
        u2 = r.commit("f", "2\n", "u2 UNSIGNED", sign=False)
        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        self.assertEqual(base, root)
        in_range = set(r.git("rev-list", f"{base}..{u2}").stdout.split())
        self.assertTrue({u1, u2}.issubset(in_range))

    def test_parallel_branch_unsigned(self):
        r = self.repo
        r.commit("shared", "0\n", "root", sign=True)
        r.git("checkout", "-q", "-b", "left")
        uL = r.commit("l.txt", "L\n", "left UNSIGNED", sign=False)
        r.git("checkout", "-q", "main")
        r.git("checkout", "-q", "-b", "right")
        uR = r.commit("r.txt", "R\n", "right UNSIGNED", sign=False)
        r.git("checkout", "-q", "main")
        r.merge("left", "merge left", date="2026-02-02T00:00:00")
        r.merge("right", "merge right", date="2026-02-03T00:00:00")
        tip = r.sha()
        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        self.assertEqual(set(unsigned), {uL, uR})
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        in_range = set(r.git("rev-list", f"{base}..{tip}").stdout.split())
        self.assertTrue({uL, uR}.issubset(in_range))
        new_tip, _ = resign_commits.rebuild(base, tip, cwd=self.cwd)
        self.assertNotIn("N", r.flags(new_tip))


class TestOctopusBuildBranch(SigningTestCase):
    """Mirrors a CI merge-gate case: a build branch off main that octopus-merges
    several parallel task branches, each carrying an unsigned checkpoint (resilient/unattended
    fallback). The tool must re-sign the whole branch so it is mergeable, preserving the
    octopus merge (>2 parents) exactly."""

    def test_octopus_merge_branch_resigns_and_preserves_topology(self):
        r = self.repo
        r.commit("main.txt", "0\n", "main base", sign=True)
        r.git("checkout", "-q", "-b", "build")
        for t in ("t1", "t2", "t3"):
            r.git("checkout", "-q", "build")
            r.git("checkout", "-q", "-b", t)
            r.commit(f"{t}.txt", "x\n", f"{t} checkpoint (UNSIGNED)", sign=False)
        r.git("checkout", "-q", "build")
        _run(["merge", "-S", "--no-ff", "-m", "octopus merge t1 t2 t3", "t1", "t2", "t3"],
             r.path, env=_env("2026-03-03T00:00:00"))
        old_tip = r.sha()
        self.assertEqual(len(resign_commits.parents(old_tip, cwd=self.cwd)), 4)  # HEAD + 3 tasks
        unsigned = resign_commits.find_unsigned("build", cwd=self.cwd)
        self.assertEqual(len(unsigned), 3)

        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        new_tip, mapping = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        results = self._verify_map(old_tip, new_tip, base, mapping)
        self.assertTrue(all(results.values()), msg=str(results))
        self.assertNotIn("N", r.flags(new_tip))
        self.assertEqual(len(resign_commits.parents(new_tip, cwd=self.cwd)), 4)  # octopus preserved


class TestCli(SigningTestCase):
    def test_no_unsigned_is_noop(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        before = r.sha()
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = resign_commits.main(["--ref", "main", "--repo", self.cwd])
        self.assertEqual(rc, 0)
        self.assertIn("Nothing to do", out.getvalue())
        self.assertEqual(r.sha(), before)

    def test_apply_moves_branch_and_is_idempotent(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        r.commit("f", "1\n", "unsigned", sign=False)
        before = r.sha()
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = resign_commits.main(["--ref", "main", "--repo", self.cwd, "--apply"])
        self.assertEqual(rc, 0)
        self.assertIn("VERIFICATION PASSED", out.getvalue())
        after = r.sha()
        self.assertNotEqual(before, after)
        self.assertNotIn("N", r.flags("main"))
        self.assertEqual(r.git("status", "--porcelain").stdout, "")
        self.assertTrue(r.git("tag", "-l", "backup/main-pre-resign-*").stdout.strip())
        out2 = io.StringIO()
        with contextlib.redirect_stdout(out2):
            rc2 = resign_commits.main(["--ref", "main", "--repo", self.cwd, "--apply"])
        self.assertEqual(rc2, 0)
        self.assertIn("Nothing to do", out2.getvalue())
        self.assertEqual(r.sha(), after)


class TestUnsignedRoot(SigningTestCase):
    """The root commit itself is unsigned -> compute_base has no parent to anchor on and
    must return None, and rebuild must rewrite the WHOLE history (base=None branch)."""

    def test_unsigned_root_rewrites_from_scratch(self):
        r = self.repo
        root = r.commit("f", "0\n", "root (UNSIGNED)", sign=False)
        r.commit("f", "1\n", "second (signed)", sign=True)
        tip = r.sha()

        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        self.assertEqual(unsigned, [root])
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        self.assertIsNone(base)  # root has no parents -> nothing to anchor on

        new_tip, mapping = resign_commits.rebuild(base, tip, cwd=self.cwd)
        self.assertEqual(len(mapping), 2)  # both commits rewritten, not just the root
        self.assertNotIn("N", r.flags(new_tip))
        results = self._verify_map(tip, new_tip, base, mapping)
        self.assertTrue(all(results.values()), msg=str(results))

    def test_unsigned_root_only_commit(self):
        """Degenerate single-commit repo where that commit is both root and tip."""
        r = self.repo
        root = r.commit("f", "0\n", "root (UNSIGNED)", sign=False)
        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        self.assertEqual(unsigned, [root])
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        self.assertIsNone(base)
        new_tip, mapping = resign_commits.rebuild(base, root, cwd=self.cwd)
        self.assertEqual(len(mapping), 1)
        results = self._verify_map(root, new_tip, base, mapping)
        self.assertTrue(all(results.values()), msg=str(results))


class TestMessageFidelity(SigningTestCase):
    """Commit messages with unicode, multiple paragraphs, and trailing whitespace must
    survive rebuild byte-for-byte -- including the edge case of a message stored with no
    final newline (the tool reads exact object bytes and commit-tree adds nothing)."""

    def test_unicode_multiparagraph_trailing_whitespace_preserved(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        msg = (
            "Héllo wörld 日本語 \U0001F389\n"
            "\n"
            "Second paragraph with trailing spaces   \n"
            "and a trailing tab\t\n"
            "\n"
            "\n"
            "Third paragraph after multiple blank lines: café, naïve, Zürich\n"
        ).encode()
        old_tip = r.commit_verbatim("f", "1\n", msg, sign=False)
        old_raw = r.raw_message(old_tip)
        self.assertEqual(old_raw, msg)  # sanity: --cleanup=verbatim really preserved it

        base = resign_commits.compute_base(resign_commits.find_unsigned("main", cwd=self.cwd), cwd=self.cwd)
        new_tip, _ = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        new_raw = r.raw_message(new_tip)
        self.assertEqual(old_raw, new_raw)  # byte-for-byte, including trailing whitespace/tab

    def test_message_without_trailing_newline_preserved_exactly(self):
        """A message stored WITHOUT a final newline is reproduced byte-for-byte -- the tool
        reads the exact object bytes (`git cat-file`, not `git log --format=%B`, which would
        append a newline) and commit-tree does no cleanup, so nothing is added or truncated."""
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        msg = "no trailing newline on this unicode message: ✓".encode()
        old_tip = r.commit_verbatim("f", "1\n", msg, sign=False)
        old_raw = r.raw_message(old_tip)
        self.assertFalse(old_raw.endswith(b"\n"))  # sanity: verbatim really kept it newline-less

        base = resign_commits.compute_base(resign_commits.find_unsigned("main", cwd=self.cwd), cwd=self.cwd)
        new_tip, _ = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        new_raw = r.raw_message(new_tip)
        self.assertEqual(new_raw, old_raw)  # byte-for-byte; no spurious newline appended


class TestVerifyCatchesTampering(SigningTestCase):
    """`verify` must FAIL, not rubber-stamp, when handed a bad rewrite. Each test tampers
    with an otherwise-valid mapping/tip and asserts the specific check(s) that should catch
    it actually flip to failing -- proving the verification has teeth."""

    def _good_history(self):
        r = self.repo
        r.commit("a.txt", "0\n", "root", sign=True)
        r.commit("a.txt", "1\n", "unsigned", sign=False)
        old_tip = r.sha()
        base = resign_commits.compute_base(resign_commits.find_unsigned("main", cwd=self.cwd), cwd=self.cwd)
        new_tip, mapping = resign_commits.rebuild(base, old_tip, cwd=self.cwd)
        return old_tip, new_tip, base, mapping

    def test_tampered_tip_pointing_at_unrelated_tree_fails_verification(self):
        r = self.repo
        old_tip, new_tip, base, mapping = self._good_history()
        # A real, valid, but UNRELATED commit -- simulates a corrupted/wrong new_tip handoff.
        r.git("checkout", "-q", "--orphan", "decoy")
        r.commit("decoy.txt", "unrelated\n", "decoy commit", sign=True)
        decoy_tip = r.sha()
        r.git("checkout", "-q", "main")

        results = self._verify_map(old_tip, decoy_tip, base, mapping)
        self.assertFalse(results["tip tree identical"])
        self.assertFalse(results["tip content diff empty"])
        self.assertFalse(all(results.values()))  # overall verdict must not be a pass

    def test_tampered_mapping_entry_fails_tree_fidelity_check(self):
        r = self.repo
        old_tip, new_tip, base, mapping = self._good_history()
        # Point one mapping entry at a commit with a DIFFERENT tree than its "original".
        r.git("checkout", "-q", "-b", "other")
        r.commit("a.txt", "SOMETHING ELSE\n", "unrelated content", sign=True)
        wrong_sha = r.sha()
        r.git("checkout", "-q", "main")
        tampered = dict(mapping)
        some_old = next(iter(tampered))
        tampered[some_old] = wrong_sha

        results = self._verify_map(old_tip, new_tip, base, tampered)
        self.assertFalse(results["all rebuilt trees identical to originals"])
        self.assertFalse(all(results.values()))

    def test_tampered_base_not_ancestor_fails_boundary_check(self):
        r = self.repo
        old_tip, new_tip, base, mapping = self._good_history()
        r.git("checkout", "-q", "--orphan", "decoy2")
        r.commit("decoy2.txt", "x\n", "decoy root", sign=True)
        decoy_base = r.sha()
        r.git("checkout", "-q", "main")

        results = self._verify_map(old_tip, new_tip, decoy_base, mapping)
        self.assertFalse(results["boundary is ancestor of new tip"])
        self.assertFalse(all(results.values()))


class TestApplyGuardrails(SigningTestCase):
    def test_apply_refuses_when_ref_is_not_a_local_branch(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        r.commit("f", "1\n", "unsigned", sign=False)
        tip = r.sha()
        r.git("tag", "sometag", tip)  # a tag, not a branch
        before_branch_tip = tip

        out = io.StringIO()
        err = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = resign_commits.main(["--ref", "sometag", "--repo", self.cwd, "--apply"])

        self.assertEqual(rc, 1)
        self.assertIn("not a local branch", err.getvalue())
        # Neither the tag nor main moved.
        self.assertEqual(r.sha("sometag"), before_branch_tip)
        self.assertEqual(r.sha("main"), before_branch_tip)
        # The parked ref was still written (verification ran and passed) -- only the
        # branch move was refused.
        parked = r.git("rev-parse", "refs/rewrite/sometag-signed").stdout.strip()
        self.assertNotEqual(parked, "")

    def test_apply_is_compare_and_swap_refuses_if_branch_moved(self):
        """--apply must not clobber commits added after detection. We simulate a concurrent
        advance by moving the branch forward between rebuild and the apply's ref move, using
        the same public entrypoints the tool would; the CAS old-value guard must refuse."""
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        r.commit("f", "1\n", "unsigned", sign=False)
        tip = r.sha()
        # Rebuild + park exactly as main() would, then advance the branch out from under it.
        unsigned = resign_commits.find_unsigned("main", cwd=self.cwd)
        base = resign_commits.compute_base(unsigned, cwd=self.cwd)
        new_tip, _ = resign_commits.rebuild(base, tip, cwd=self.cwd)
        r.commit("f", "2\n", "concurrent commit after detection", sign=True)
        advanced = r.sha()
        # A bare CAS move with the stale expected-old value must fail and leave the branch put.
        ok = resign_commits.git_ok(["update-ref", "refs/heads/main", new_tip, tip], cwd=self.cwd)
        self.assertFalse(ok)
        self.assertEqual(r.sha("main"), advanced)  # branch not clobbered

    def test_print_push_cmd_emits_expected_force_with_lease_string(self):
        r = self.repo
        r.commit("f", "0\n", "root", sign=True)
        r.commit("f", "1\n", "unsigned", sign=False)
        old_tip = r.sha()

        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = resign_commits.main(
                ["--ref", "main", "--repo", self.cwd, "--apply", "--print-push-cmd"]
            )
        self.assertEqual(rc, 0)
        expected = f"To publish:  git push --force-with-lease=main:{old_tip} origin main"
        self.assertIn(expected, out.getvalue())


if __name__ == "__main__":
    unittest.main(verbosity=2)
