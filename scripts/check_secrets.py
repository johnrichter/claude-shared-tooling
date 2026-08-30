#!/usr/bin/env python3
"""Security guardrail — stdlib-only, zero setup.

Scans committed files for accidentally-committed plaintext secrets: private
keys, cloud/API access-key ids, and chat/VCS-host tokens. Fails the build on
any high-confidence match, scanned across the FULL text of EVERY non-skipped
file. The only exemption is an exact match against an enumerated documentation
placeholder value that provably cannot be a real credential (see
EXACT_EXEMPTIONS).

Usage:
    python3 scripts/check_secrets.py            # scan repo root (exit 1 on any match)
    python3 scripts/check_secrets.py --root P   # scan a different root
"""
from __future__ import annotations

import argparse
import re
from pathlib import Path

# Directories never scanned (VCS internals, build/venv artifacts).
# ".git-worktrees" mirrors the .gitignore entry — never scan transient worktree checkouts.
SKIP_DIRS = {".git", ".git-worktrees", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", "dist", "build", "target"}
SKIP_SUFFIX_DIRS = (".egg-info",)

# Binary/asset extensions we never text-scan.
BINARY_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".gz", ".whl", ".pyc", ".ico", ".woff", ".woff2"}

# Labels carrying an exact-match exemption, each named once because
# matches_pattern dispatches on it: a rename here stays in sync with
# SECRET_PATTERNS by construction, where two string literals would silently
# drift and disable the exemption.
AWS_KEY_LABEL = "AWS access-key id"
SLACK_TOKEN_LABEL = "Slack token"

# High-confidence plaintext secret patterns.
SECRET_PATTERNS = [
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), "private key block"),
    (re.compile(r"\bAKIA[0-9A-Z]{16}\b"), AWS_KEY_LABEL),
    (re.compile(r"\bxox[baprs]-[0-9A-Za-z-]{10,}"), SLACK_TOKEN_LABEL),
    (re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,}"), "GitHub token"),
]

# The exact, enumerated AWS documentation placeholder access-key ids that the
# AWS-access-key-id pattern must never flag -- AWS's key-generation process can
# never produce these exact strings, so an exact match is provably not a real,
# leaked credential. Mirrors awsExampleAccessKeyIDs in go/githooks/secrets.go;
# the two implementations of this pattern set stay behaviorally identical.
# Exempt by exact string only, never by substring or "contains EXAMPLE" heuristic.
# Fragment-assembled: a pre-fix scanner landing this commit has no allowlist
# yet and would otherwise refuse the merge over this source line.
AWS_EXAMPLE_ACCESS_KEY_IDS = frozenset({"AKIAIOSFODNN7" + "EXAMPLE"})

# The exact, enumerated Slack-token-shaped strings the Slack-token pattern must
# never flag -- each one is a documented example format from a third-party
# secret-detection tool's own rule-definition file, illustrating what that
# tool's Slack-token rule matches, not a bot token any Slack workspace ever
# issued. Mirrors slackExampleTokens in go/githooks/secrets.go; the two
# implementations of this pattern set stay behaviorally identical.
# Exempt by exact string only, never by substring or "contains EXAMPLE" heuristic.
# Fragment-assembled for the same reason as AWS_EXAMPLE_ACCESS_KEY_IDS's.
SLACK_EXAMPLE_TOKENS = frozenset({"xoxb-ab59" + "EXAMPLETOKEN"})

# A pattern's label -> its exact-match exemption set. A label absent here has
# no exact-match exemption at all. Mirrors exactExemptions in
# go/githooks/secrets.go.
EXACT_EXEMPTIONS = {
    AWS_KEY_LABEL: AWS_EXAMPLE_ACCESS_KEY_IDS,
    SLACK_TOKEN_LABEL: SLACK_EXAMPLE_TOKENS,
}


def matches_pattern(rx: re.Pattern[str], label: str, text: str) -> bool:
    """Whether text holds a hit for rx not covered by label's exact-match exemption.

    Every occurrence is checked; text is flagged only if at least one occurrence
    is not exempt (see EXACT_EXEMPTIONS).
    """
    exempt = EXACT_EXEMPTIONS.get(label)
    if exempt is None:
        return rx.search(text) is not None
    return any(m.group(0) not in exempt for m in rx.finditer(text))


def iter_files(root: Path, self_path: Path):
    """Yield scannable files under root, skipping VCS/build dirs and this checker."""
    for p in sorted(root.rglob("*")):
        if not p.is_file():
            continue
        if p.resolve() == self_path:
            continue  # the checker holds the pattern literals by design
        parts = set(p.relative_to(root).parts)
        if parts & SKIP_DIRS or any(part.endswith(SKIP_SUFFIX_DIRS) for part in parts):
            continue
        if p.suffix.lower() in BINARY_SUFFIXES:
            continue
        yield p


def scan(root: Path, self_path: Path) -> list[str]:
    """Return failures as human-readable strings."""
    failures: list[str] = []
    for p in iter_files(root, self_path):
        rel = p.relative_to(root)
        try:
            text = p.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue  # unreadable/binary — nothing to leak in text form
        for rx, label in SECRET_PATTERNS:
            if matches_pattern(rx, label, text):
                failures.append(f"{rel}: possible {label}")
    return failures


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Secret-scanner guardrail (stdlib-only).")
    ap.add_argument("--root", default=None, help="Repo root to scan (default: parent of scripts/).")
    args = ap.parse_args(argv)

    self_path = Path(__file__).resolve()
    root = Path(args.root).resolve() if args.root else self_path.parent.parent

    failures = scan(root, self_path)

    if failures:
        print(f"FAIL — {len(failures)} possible secret(s) under {root}:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"OK — no secrets found under {root}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
