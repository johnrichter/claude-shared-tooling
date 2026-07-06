#!/usr/bin/env python3
"""Re-sign unsigned commits on a git ref by rebuilding the commit DAG with `git commit-tree`.

Why this exists
---------------
The obvious repair -- ``git rebase [--rebase-merges] --exec 'git commit --amend --no-edit -S'``
-- REPLAYS commits and RE-RUNS merges. On any range that contains merge commits it replays
the original conflict resolutions and stops with a conflict (empirically observed on
merge-heavy branches). It can also flatten merges into linear history.

This tool instead rebuilds each commit with ``git commit-tree``, reusing that commit's EXACT
original tree object. It never applies a diff and never re-runs a merge, so a rewrite over
merge-heavy history CANNOT conflict, and merge topology is preserved exactly. Each rebuilt
commit is byte-identical to its original except (a) parent pointers remapped to the rebuilt
ancestors and (b) an added signature. Author/committer identity and both timestamps are
preserved verbatim, and the commit message is reproduced byte-for-byte.

Safety model
------------
Default is a DRY RUN. The tool detects unsigned commits, tags a backup of the current tip,
rebuilds the signed history into a PARKED ref (``refs/rewrite/<ref>-signed``), verifies it
exhaustively, and prints a report -- the target ref is NOT moved and nothing is pushed.
Moving the branch requires ``--apply`` (and only if verification passed). The branch move is a
compare-and-swap against the tip observed at detection, so a branch that advanced meanwhile is
refused rather than clobbered. Pushing is left to the operator: force-push is guardrailed by
design, so ``--print-push-cmd`` merely emits the exact ``--force-with-lease`` command to run.

Verification (all must pass before --apply moves anything):
  * tip tree byte-identical to the original (``git diff`` empty)     -- content unchanged
  * commit count and merge count preserved                           -- topology intact
  * zero unsigned (``N``) and zero bad (``B``) signatures remain      -- the actual goal
  * every rebuilt commit's tree + author/committer/date/message match -- per-commit fidelity
  * the boundary commit is still an ancestor of the new tip           -- nothing below moved

Usage:
    python3 resign_commits.py                       # dry run over `main`
    python3 resign_commits.py --ref some-branch     # dry run over another ref
    python3 resign_commits.py --apply               # move the local ref after verify passes
    python3 resign_commits.py --apply --print-push-cmd
    python3 resign_commits.py --no-backup           # skip the backup tag (not advised)

Stdlib-only; shells out to `git`. Requires a working commit-signing config (`git commit -S`).
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
from datetime import datetime, timezone

NUL = "\x00"
# Author/committer identity + both raw dates, NUL-delimited so names may contain any char.
_IDENT_FMT = "%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd"


def git(args: list[str], *, cwd: str, check: bool = True) -> str:
    """Run git and return stdout as a stripped str."""
    r = subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=check
    )
    return r.stdout.strip()


def git_ok(args: list[str], *, cwd: str) -> bool:
    """Run git for its exit status only (0 -> True)."""
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True
    ).returncode == 0


def parents(sha: str, *, cwd: str) -> list[str]:
    """Parent SHAs of `sha`, in order (empty for a root commit)."""
    return git(["rev-list", "--parents", "-n", "1", sha], cwd=cwd).split()[1:]


def raw_message(sha: str, *, cwd: str) -> bytes:
    """Exact message bytes stored in the commit object (everything after the header block).

    Uses `git cat-file commit`, NOT `git log --format=%B`: the latter appends its own
    trailing newline, which would add a spurious blank line to every rebuilt commit. The
    header block ends at the first blank line; a `gpgsig` header's continuation lines are
    space-prefixed, so they never contain a blank line and the split stays correct for
    already-signed commits too.
    """
    raw = subprocess.run(
        ["git", "cat-file", "commit", sha], cwd=cwd, capture_output=True, check=True
    ).stdout
    return raw[raw.index(b"\n\n") + 2:]


def find_unsigned(ref: str, *, cwd: str) -> list[str]:
    """Every commit reachable from `ref` whose signature status is `N` (no signature)."""
    out = git(["log", "--format=%H %G?", ref], cwd=cwd)
    unsigned = []
    for line in out.splitlines():
        sha, _, flag = line.partition(" ")
        if flag == "N":
            unsigned.append(sha)
    return unsigned


def compute_base(unsigned: list[str], *, cwd: str) -> str | None:
    """Boundary commit below which nothing is rewritten.

    Returns the octopus merge-base of the PARENTS of every unsigned commit -- a commit that
    is a proper ancestor of every unsigned commit. Then ``base..tip`` captures all unsigned
    commits (each is a descendant of its parent, which is >= base) and nothing unsigned sits
    at or below base (any such commit would itself be detected and pull the base lower).
    Correct for chained unsigned commits (u1 is ancestor of u2) and for unsigned commits on
    parallel branches that both merge into the tip. Returns None only when an unsigned commit
    is a root commit (no parents) -- the caller then rewrites from the root.

    May over-approximate the range (rewrite some already-signed commits); that is harmless
    because re-signing reuses each tree exactly, so those commits stay byte-identical.
    """
    all_parents: set[str] = set()
    for u in unsigned:
        all_parents.update(parents(u, cwd=cwd))
    if not all_parents:
        return None
    plist = sorted(all_parents)
    if len(plist) == 1:
        return plist[0]
    return git(["merge-base", "--octopus", *plist], cwd=cwd)


def rebuild(base: str | None, tip: str, *, cwd: str, sign: bool = True):
    """Rebuild every commit in ``base..tip`` (or all of ``tip`` if base is None) via
    ``git commit-tree``, reusing each original tree and remapping parents. Returns
    ``(new_tip, mapping)`` where mapping is ``{old_sha: new_sha}``. Does NOT move any ref.
    """
    rev_args = ["rev-list", "--topo-order", "--reverse"]
    rev_args.append(tip if base is None else f"{base}..{tip}")
    order = git(rev_args, cwd=cwd).split()

    mapping: dict[str, str] = {}
    for old in order:
        tree = git(["rev-parse", f"{old}^{{tree}}"], cwd=cwd)
        p_args: list[str] = []
        for p in parents(old, cwd=cwd):
            p_args += ["-p", mapping.get(p, p)]

        an, ae, ad, cn, ce, cd = git(
            ["log", "-1", f"--format={_IDENT_FMT}", "--date=raw", old], cwd=cwd
        ).split(NUL)
        env_overrides = {
            "GIT_AUTHOR_NAME": an, "GIT_AUTHOR_EMAIL": ae, "GIT_AUTHOR_DATE": ad,
            "GIT_COMMITTER_NAME": cn, "GIT_COMMITTER_EMAIL": ce, "GIT_COMMITTER_DATE": cd,
        }
        env = {**os.environ, **env_overrides}

        # Feed the exact stored message bytes to commit-tree via stdin -> byte-for-byte.
        msg = raw_message(old, cwd=cwd)
        ct_args = ["git", "commit-tree"]
        if sign:
            ct_args.append("-S")
        ct_args += p_args + [tree]
        r = subprocess.run(
            ct_args, cwd=cwd, input=msg, capture_output=True, check=True, env=env
        )
        mapping[old] = r.stdout.decode().strip()

    return mapping.get(tip, tip), mapping


def verify(old_tip: str, new_tip: str, base: str | None, mapping: dict[str, str], *, cwd: str):
    """Return a list of ``(check_name, passed, detail)`` tuples. Empty-detail passes stay quiet."""
    results: list[tuple[str, bool, str]] = []

    def check(name: str, ok: bool, detail: str = "") -> None:
        results.append((name, ok, detail))

    ot = git(["rev-parse", f"{old_tip}^{{tree}}"], cwd=cwd)
    nt = git(["rev-parse", f"{new_tip}^{{tree}}"], cwd=cwd)
    check("tip tree identical", ot == nt, f"old={ot} new={nt}")
    diff = git(["diff", "--stat", old_tip, new_tip], cwd=cwd)
    check("tip content diff empty", diff == "", diff.splitlines()[-1] if diff else "")

    oc, nc = git(["rev-list", "--count", old_tip], cwd=cwd), git(["rev-list", "--count", new_tip], cwd=cwd)
    check("commit count preserved", oc == nc, f"old={oc} new={nc}")
    om = git(["rev-list", "--merges", "--count", old_tip], cwd=cwd)
    nm = git(["rev-list", "--merges", "--count", new_tip], cwd=cwd)
    check("merge count preserved", om == nm, f"old={om} new={nm}")

    tally = git(["log", "--format=%G?", new_tip], cwd=cwd).splitlines()
    n_unsigned = sum(1 for f in tally if f == "N")
    n_bad = sum(1 for f in tally if f == "B")
    check("no unsigned (N) commits remain", n_unsigned == 0, f"{n_unsigned} still unsigned")
    check("no bad (B) signatures", n_bad == 0, f"{n_bad} bad signatures")

    tree_ok, meta_ok = True, True
    for old, new in mapping.items():
        if git(["rev-parse", f"{old}^{{tree}}"], cwd=cwd) != git(["rev-parse", f"{new}^{{tree}}"], cwd=cwd):
            tree_ok = False
        o_ident = git(["log", "-1", f"--format={_IDENT_FMT}", "--date=raw", old], cwd=cwd)
        n_ident = git(["log", "-1", f"--format={_IDENT_FMT}", "--date=raw", new], cwd=cwd)
        if o_ident != n_ident:
            meta_ok = False
        # Exact byte compare of the stored message -> catches any trailing-newline drift.
        if raw_message(old, cwd=cwd) != raw_message(new, cwd=cwd):
            meta_ok = False
    check("all rebuilt trees identical to originals", tree_ok)
    check("all author/committer/date/message preserved", meta_ok)

    if base is not None:
        check("boundary is ancestor of new tip", git_ok(["merge-base", "--is-ancestor", base, new_tip], cwd=cwd))

    return results


def _sanitize(ref: str) -> str:
    return ref.replace("/", "-")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Re-sign unsigned commits on a git ref (conflict-proof).")
    ap.add_argument("--ref", default="main", help="Ref to repair (default: main).")
    ap.add_argument("--repo", default=None, help="Repo path (default: enclosing work tree).")
    ap.add_argument("--apply", action="store_true", help="Move the local branch to the re-signed tip after verification passes.")
    ap.add_argument("--print-push-cmd", action="store_true", help="Print the exact --force-with-lease push command (does not push).")
    ap.add_argument("--no-backup", action="store_true", help="Skip creating the backup tag (not advised).")
    args = ap.parse_args(argv)

    cwd = args.repo or git(["rev-parse", "--show-toplevel"], cwd=".")
    tip = git(["rev-parse", args.ref], cwd=cwd)

    unsigned = find_unsigned(args.ref, cwd=cwd)
    if not unsigned:
        print(f"No unsigned commits on {args.ref} ({tip[:12]}). Nothing to do.")
        return 0
    print(f"Found {len(unsigned)} unsigned commit(s) on {args.ref} ({tip[:12]}).")

    base = compute_base(unsigned, cwd=cwd)

    if not args.no_backup:
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = f"backup/{_sanitize(args.ref)}-pre-resign-{stamp}"
        git(["tag", "-f", backup, tip], cwd=cwd)
        print(f"Backup tag: {backup} -> {tip[:12]}")

    new_tip, mapping = rebuild(base, tip, cwd=cwd)
    parked = f"refs/rewrite/{_sanitize(args.ref)}-signed"
    git(["update-ref", parked, new_tip], cwd=cwd)
    print(f"Rebuilt {len(mapping)} commit(s); parked at {parked} -> {new_tip[:12]}")

    results = verify(tip, new_tip, base, mapping, cwd=cwd)
    for name, passed, detail in results:
        suffix = f"  ({detail})" if detail and not passed else ""
        print(f"  [{'PASS' if passed else 'FAIL'}] {name}{suffix}")
    if not all(ok for _, ok, _ in results):
        print("VERIFICATION FAILED — target ref NOT moved. Investigate before applying.", file=sys.stderr)
        return 1
    print("VERIFICATION PASSED.")

    if args.apply:
        if not git_ok(["show-ref", "--verify", "--quiet", f"refs/heads/{args.ref}"], cwd=cwd):
            print(f"--apply skipped: {args.ref} is not a local branch (parked ref left at {parked}).", file=sys.stderr)
            return 1
        # Compare-and-swap: refuse the move if the branch advanced since detection (the
        # rebuild was computed from `tip`; moving to new_tip would otherwise drop any commit
        # added in between). git update-ref with an expected old value is atomic.
        if not git_ok(["update-ref", f"refs/heads/{args.ref}", new_tip, tip], cwd=cwd):
            print(f"--apply aborted: {args.ref} moved since detection (expected {tip[:12]}); nothing changed. Re-run.", file=sys.stderr)
            return 1
        print(f"Applied: refs/heads/{args.ref} -> {new_tip[:12]}  (working tree unchanged; trees identical)")
    else:
        print(f"Dry run — ref not moved. To apply:  git update-ref refs/heads/{args.ref} {new_tip}")

    if args.print_push_cmd:
        print(f"To publish:  git push --force-with-lease={args.ref}:{tip} origin {args.ref}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
