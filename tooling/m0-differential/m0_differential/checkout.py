"""Materializing and building a build-helpers binary from any commit in this repo.

Both sides of the differential are produced the same way — export a tree, optionally patch it,
compile it — so a divergence can only come from the source, never from how the binary was made.
Exporting the pre-M0 commit is also the recorded rollback: recovering the pre-M0 artifact is the
same operation the differential performs on every run.
"""
from __future__ import annotations

import hashlib
import shutil
import subprocess
import tarfile
from pathlib import Path

# M0.P1.T1's baseline capture. The tree at this commit IS the pre-M0 artifact: the differential
# compares against it and the rollback restores from it. Identity is verified on every export by
# rebuilding it and matching its --help byte-for-byte against the help.txt captured in this commit.
PRE_M0_REF = "dfe52b23aa5d38fe9cd23051c650e06a5125eda9"

MODULE_SUBDIR = "go/build-helpers"
HELP_GOLDEN_SUBPATH = "go/build-helpers/testdata/pre-m0-baseline/help.txt"


class CheckoutError(RuntimeError):
    """A tree could not be exported, patched, or compiled."""


def repo_root() -> Path:
    return Path(__file__).resolve().parents[3]


def export(ref: str, dest: Path) -> Path:
    """Export the tree at ref into dest (which must not already exist) and return it.

    git archive is a pure read of the object database: no worktree is registered, no ref is
    created, and the repo the export runs against is left untouched.
    """
    if dest.exists():
        raise CheckoutError(f"scratch checkout {dest} already exists")
    dest.mkdir(parents=True)
    archive = dest.with_suffix(".tar")
    try:
        with archive.open("wb") as fh:
            subprocess.run(
                ["git", "archive", "--format=tar", ref],
                cwd=repo_root(), stdout=fh, stderr=subprocess.PIPE, check=True,
            )
        with tarfile.open(archive) as tar:
            tar.extractall(dest, filter="data")
    except subprocess.CalledProcessError as exc:
        raise CheckoutError(f"git archive {ref} failed: {exc.stderr.decode(errors='replace')}") from exc
    finally:
        archive.unlink(missing_ok=True)
    return dest


def export_worktree(dest: Path) -> Path:
    """Copy the working tree's go/ modules into dest — the post-M0 side, uncommitted changes
    included, since the gate has to see what is actually about to ship. build-helpers resolves its
    roster dependency through a local replace, so the sibling module comes along."""
    if dest.exists():
        raise CheckoutError(f"scratch checkout {dest} already exists")
    shutil.copytree(repo_root() / "go", dest / "go", ignore=shutil.ignore_patterns("testdata"))
    return dest


def patch(tree: Path, edits: list[tuple[str, str, str]]) -> None:
    """Apply exact-string edits to an exported tree. A find string that is absent, or present more
    than once, aborts: a plant that silently no-ops would prove nothing."""
    for relpath, old, new in edits:
        path = tree / relpath
        text = path.read_text(encoding="utf-8")
        hits = text.count(old)
        if hits != 1:
            raise CheckoutError(f"{relpath}: edit anchor matched {hits} times, want exactly 1: {old!r}")
        path.write_text(text.replace(old, new), encoding="utf-8")


def build(tree: Path, binary: Path) -> Path:
    """Compile the build-helpers module inside an exported tree."""
    module = tree / MODULE_SUBDIR
    if not module.is_dir():
        raise CheckoutError(f"{module} is not a build-helpers module directory")
    result = subprocess.run(
        ["go", "build", "-o", str(binary), "."],
        cwd=module, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise CheckoutError(f"go build in {module} failed:\n{result.stderr}")
    return binary


def verify_pre_m0_identity(tree: Path, binary: Path) -> str:
    """Confirm an exported pre-M0 tree really is the artifact M0.P1.T1 captured, by rebuilding it
    and matching its help output against the help.txt committed alongside the baseline.

    This is the one byte-for-byte comparison the differential keeps, and it is safe to keep: both
    sides are frozen history, so no future wording change can ever move them apart.
    """
    golden = (tree / HELP_GOLDEN_SUBPATH).read_bytes()
    result = subprocess.run([str(binary), "--help"], capture_output=True)
    observed = result.stdout or result.stderr
    if observed != golden:
        raise CheckoutError(
            "the rebuilt pre-M0 binary does not reproduce the pre-M0 help capture — the exported "
            "tree is not the artifact M0.P1.T1 recorded"
        )
    return hashlib.sha256(golden).hexdigest()
