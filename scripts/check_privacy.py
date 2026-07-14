#!/usr/bin/env python3
"""Privacy guardrail for a PUBLIC shared-lib repo — stdlib-only, zero setup.

Fails the build if anything that must not appear in a public, owner-public repo
leaks in. Two failure classes plus an advisory class:

  FAIL — private sensitivity/owner markers. A `privacy:internal`, `privacy:confidential`,
         `owner:datadog`, or `owner:personal` tag has no business in a public repo (the
         repo-split routing key says it belongs in a private repo). Also fails a doc whose
         frontmatter declares privacy/owner at all but not the public pair.
         Scope: these checks read ONLY the leading YAML frontmatter block (`---` ... `---`
         at the top of the file) of `.md` files. A `privacy:`/`owner:` token is a
         sensitivity classification only when it is the file's own frontmatter tag —
         everywhere else (Rust source holding the token as a string literal, JSON schemas
         enumerating allowed tag values, non-.md files generally) the same token is DATA,
         not a classification, and is not scanned. A narrow, explicit exemption
         (`EXEMPT_CLASSIFICATION_GLOBS`) also excludes the frontmatter validator's own test
         fixtures, which deliberately carry private tags as sample input to exercise the
         validator.
  FAIL — high-confidence secrets. Private keys, AWS access-key ids, Slack/GitHub
         tokens committed as plaintext. Scanned across the FULL text of EVERY file,
         unconditionally — the frontmatter scoping and fixture exemption above apply
         only to the classification checks and never reduce secret-scan coverage.
  WARN — org mentions (advisory). Bare organisation/workspace names that suggest
         provenance a public artifact should not carry; reported, does not fail
         (avoids false-positives on generic tools that legitimately parse org URLs).
         Scanned across the full text of every file, same as secrets.

Usage:
    python3 scripts/check_privacy.py            # scan repo root (exit 1 on any FAIL)
    python3 scripts/check_privacy.py --root P   # scan a different root
    python3 scripts/check_privacy.py --strict   # WARN also fails the exit code
"""
from __future__ import annotations

import argparse
import fnmatch
import re
import sys
from pathlib import Path

# Directories never scanned (VCS internals, build/venv artifacts).
# ".git-worktrees" mirrors the .gitignore entry — never scan transient worktree checkouts.
SKIP_DIRS = {".git", ".git-worktrees", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", "dist", "build", "target"}
SKIP_SUFFIX_DIRS = (".egg-info",)

# Binary/asset extensions we never text-scan.
BINARY_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".gz", ".whl", ".pyc", ".ico", ".woff", ".woff2"}

# FAIL — private sensitivity/owner tag markers (the repo-split routing key).
PRIVATE_MARKERS = [
    re.compile(r"\bprivacy:\s*(internal|confidential)\b", re.IGNORECASE),
    re.compile(r"\bowner:\s*(datadog|personal)\b", re.IGNORECASE),
]

# FAIL — high-confidence plaintext secrets.
SECRET_PATTERNS = [
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), "private key block"),
    (re.compile(r"\bAKIA[0-9A-Z]{16}\b"), "AWS access-key id"),
    (re.compile(r"\bxox[baprs]-[0-9A-Za-z-]{10,}"), "Slack token"),
    (re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,}"), "GitHub token"),
]

# WARN — advisory org/workspace provenance mentions.
ORG_MENTIONS = [re.compile(r"\bdatadog\b", re.IGNORECASE), re.compile(r"\bpsa-apm\b", re.IGNORECASE)]

# A doc that declares a privacy/owner tag value at all must declare the public pair.
# (Catches non-enumerated values the PRIVATE_MARKERS list above does not name, e.g.
# `privacy: restricted` or `owner: team` — anything that is not explicitly public.)
FM_PAIR_CHECKS = [
    (re.compile(r"\bprivacy:\s*\w+", re.IGNORECASE), re.compile(r"\bprivacy:\s*public\b", re.IGNORECASE), "privacy"),
    (re.compile(r"\bowner:\s*\w+", re.IGNORECASE), re.compile(r"\bowner:\s*public\b", re.IGNORECASE), "owner"),
]

# Repo-relative path globs exempt from the classification checks (PRIVATE_MARKERS,
# FM_PAIR_CHECKS) only — never from SECRET_PATTERNS. These are the frontmatter
# validator's own test fixtures: sample docs that deliberately carry private
# privacy:/owner: tags as input data to exercise the validator, not as the fixture
# file's own sensitivity classification. Matched with fnmatch, which is why `*`
# reaches across `/` — keep entries anchored under a real directory boundary.
EXEMPT_CLASSIFICATION_GLOBS = ("rust/frontmatter/tests/*",)


def _is_exempt_from_classification(rel_posix: str) -> bool:
    return any(fnmatch.fnmatch(rel_posix, pattern) for pattern in EXEMPT_CLASSIFICATION_GLOBS)


def _frontmatter_block(text: str) -> str:
    """Return the leading `---`-delimited YAML block, or "" if there isn't one.

    Tolerates a leading UTF-8 BOM and leading blank/whitespace-only lines
    before the opening `---`, and trailing whitespace on either delimiter
    line — matching common Markdown frontmatter parsers (Jekyll/gray-matter
    accept `---\\s*`). Only BOM/blank lines may precede the opening delimiter
    (an indented `---` is body, not frontmatter); a `---` found deeper in the
    body is not treated as frontmatter. The block is the content between the
    opening delimiter and the next line that is `---` (ignoring trailing
    whitespace). An unclosed block is not frontmatter.
    """
    text = text.lstrip("﻿")
    lines = text.splitlines()
    start = 0
    while start < len(lines) and lines[start].strip() == "":
        start += 1
    if start >= len(lines) or lines[start].rstrip() != "---":
        return ""
    for i, line in enumerate(lines[start + 1 :], start=start + 1):
        if line.rstrip() == "---":
            return "\n".join(lines[start + 1 : i])
    return ""


def iter_files(root: Path, self_path: Path):
    """Yield scannable files under root, skipping VCS/build dirs and this checker."""
    for p in sorted(root.rglob("*")):
        if not p.is_file():
            continue
        if p.resolve() == self_path:
            continue  # the checker holds the marker literals by design
        parts = set(p.relative_to(root).parts)
        if parts & SKIP_DIRS or any(part.endswith(SKIP_SUFFIX_DIRS) for part in parts):
            continue
        if p.suffix.lower() in BINARY_SUFFIXES:
            continue
        yield p


def scan(root: Path, self_path: Path) -> tuple[list[str], list[str]]:
    """Return (failures, warnings) as human-readable strings."""
    failures: list[str] = []
    warnings: list[str] = []
    for p in iter_files(root, self_path):
        rel = p.relative_to(root)
        rel_posix = rel.as_posix()
        try:
            text = p.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue  # unreadable/binary — nothing to leak in text form

        # Classification checks: frontmatter-scoped, .md only, minus the narrow
        # fixture exemption. Outside that scope a privacy:/owner: token is data
        # (a string literal, a schema enum value, a test fixture), not a claim
        # about this file's own sensitivity.
        if p.suffix.lower() == ".md" and not _is_exempt_from_classification(rel_posix):
            frontmatter = _frontmatter_block(text)
            if frontmatter:
                for rx in PRIVATE_MARKERS:
                    for m in rx.finditer(frontmatter):
                        failures.append(f"{rel}: private marker '{m.group(0)}'")
                for any_rx, public_rx, key in FM_PAIR_CHECKS:
                    if any_rx.search(frontmatter) and not public_rx.search(frontmatter):
                        failures.append(f"{rel}: declares {key}: tag but not {key}:public")

        # Secrets scan the full text of every file, unconditionally — no
        # frontmatter scoping, no exemption. Classification exemptions must
        # never widen the secret-leak blind spot.
        for rx, label in SECRET_PATTERNS:
            if rx.search(text):
                failures.append(f"{rel}: possible {label}")
        for rx in ORG_MENTIONS:
            if rx.search(text):
                warnings.append(f"{rel}: org/workspace mention '{rx.pattern}'")
    return failures, warnings


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Public-repo privacy guardrail (stdlib-only).")
    ap.add_argument("--root", default=None, help="Repo root to scan (default: parent of scripts/).")
    ap.add_argument("--strict", action="store_true", help="Warnings also fail the exit code.")
    args = ap.parse_args(argv)

    self_path = Path(__file__).resolve()
    root = Path(args.root).resolve() if args.root else self_path.parent.parent

    failures, warnings = scan(root, self_path)

    if warnings:
        print(f"WARN — {len(warnings)} advisory org/workspace mention(s) under {root}:")
        for w in warnings:
            print(f"  - {w}")
    if failures:
        print(f"FAIL — {len(failures)} privacy violation(s) under {root}:")
        for f in failures:
            print(f"  - {f}")
        return 1
    if args.strict and warnings:
        print("FAIL — --strict: advisory mentions treated as failures.")
        return 1
    print(f"OK — no privacy violations under {root}." + (f" ({len(warnings)} warning(s))" if warnings else ""))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
