#!/usr/bin/env python3
"""Registry lint — the invariant registry is valid, complete, and non-duplicating.

Checks the properties that are internal to the registry document and to the set of
gates a checkout actually runs:

    schema      every entry validates against invariant-registry.schema.json
    ids         entry ids are unique
    restatement no two entries restate one invariant instead of referencing it by path
    resolution  every gate path, reference and doc path of a shipped entry exists
    published   an entry a gate publishes in its own header exists here and agrees
    complete    every discovered in-scope gate is declared here

What it deliberately does NOT check: the rung-1 symbol and rung-3 test id (the
invariant linter resolves those against the source and the running suite), and the
rung-2 declared-vs-actual firing precision (the gate library). Rung-4 and rung-5
entries have no consumer at all and are complete-or-not, nothing more — the schema's
x-verification-model is the normative statement of that limit.

Gates are discovered through the artifact that runs them — a plugin's hooks.json, a CI
workflow file — so a gate that ships without a declaration is named here rather than
being invisible. A root whose checkout is absent is reported unresolved and drives no
verdict in either direction: a sibling repository nobody cloned is a checkout
condition, not a signal.

Usage:
    python3 schemas/invariant-registry/check.py
    python3 schemas/invariant-registry/check.py --repo marketplace=../marketplace
    python3 schemas/invariant-registry/check.py --require-roots   # every root must resolve

Exit codes: 0 clean; 1 one or more violations; 2 the harness itself could not run
(unreadable registry, or the pinned validator not installed — never a silent pass).
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

SELF_DIR = Path(__file__).resolve().parent
REGISTRY_PATH = SELF_DIR / "invariant-registry.json"
SCHEMA_PATH = SELF_DIR / "invariant-registry.schema.json"

# This checkout's own name, as it appears as the first segment of a repo-qualified path.
SELF_REPO = "ai-shared-lib"

# Share of content words above which two statements are taken to be the same invariant
# worded twice. Calibrated by measurement against the seed: the most similar pair of
# genuinely distinct invariants scores 0.40, while restatements of one seed statement score
# 1.00 verbatim and 0.55-0.70 reworded. The threshold sits between those bands. Word overlap
# cannot see meaning, so a heavily reworded duplicate (measured as low as 0.07) still slips
# through to the reviewer - the check removes the easy half and never claims the other half.
RESTATEMENT_SIMILARITY = 0.5

# Suffixes stripped before comparison so a paraphrase that only changes tense or number
# does not read as a different statement.
_SUFFIXES = ("ing", "ed", "es", "s")

_WORD = re.compile(r"[a-z0-9]+")

# Words carrying no distinguishing content: keeping them would let boilerplate phrasing
# push two unrelated statements over the similarity threshold.
_STOPWORDS = frozenset(
    """a an and any are as at be been before by can cannot for from has have in into is it
    its no nor not of on one only or other others that the their them then there these this
    to under until up upon was were what when where which while who whose with within without
    """.split()
)

_HOOK_COMMAND_PATH = re.compile(r"[\w./${}-]*hooks/([A-Za-z0-9_.-]+)")
_CI_GUARD_PATH = re.compile(r"((?:tooling|schemas)/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.py)")
# A gate publishes its declaration between two marker lines of its own comment syntax.
# Anchoring to whole lines keeps a tool that merely mentions the markers - this one -
# from being read as publishing a declaration.
_PUBLISHED_BLOCK = re.compile(
    r"^[^\n\w]*INVARIANT-REGISTRY-BEGIN[ \t]*$(?P<body>.*?)^[^\n\w]*INVARIANT-REGISTRY-END[ \t]*$",
    re.DOTALL | re.MULTILINE,
)


class HarnessError(Exception):
    """The check could not be performed at all, as distinct from finding a violation."""


def load_json(path: Path) -> dict:
    """Read one JSON document, raising HarnessError when it cannot be parsed."""
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise HarnessError(f"{path}: {exc}") from exc


def resolve_repos(self_root: Path, overrides: list[str]) -> dict[str, Path | None]:
    """Map each repo name to its checkout, or to None when it is not present.

    This checkout is located from the script itself. Every other repo defaults to a
    sibling directory of it, which an explicit NAME=PATH override replaces.
    """
    resolved: dict[str, Path | None] = {SELF_REPO: self_root}
    for override in overrides:
        name, _, raw = override.partition("=")
        if not name or not raw:
            raise HarnessError(f"--repo expects NAME=PATH, got {override!r}")
        path = Path(raw).expanduser().resolve()
        resolved[name] = path if path.is_dir() else None
    return resolved


def repo_dir(repos: dict[str, Path | None], self_root: Path, name: str) -> Path | None:
    """Resolve a repo name to a checkout directory, remembering the answer."""
    if name not in repos:
        sibling = self_root.parent / name
        repos[name] = sibling if sibling.is_dir() else None
    return repos[name]


def resolve_path(repos: dict[str, Path | None], self_root: Path, qualified: str) -> Path | None:
    """Resolve a repo-qualified path to a filesystem path, or None if its repo is absent."""
    repo, _, rest = qualified.partition("/")
    root = repo_dir(repos, self_root, repo)
    return None if root is None else root / rest


def stem(word: str) -> str:
    """Strip a plural or tense suffix, leaving a stem of at least four characters."""
    for suffix in _SUFFIXES:
        if word.endswith(suffix) and len(word) - len(suffix) >= 4:
            return word[: -len(suffix)]
    return word


def content_words(statement: str) -> set[str]:
    """Reduce a statement to the set of stemmed words that carry its meaning."""
    return {
        stem(w) for w in _WORD.findall(statement.lower()) if w not in _STOPWORDS and len(w) > 2
    }


def similarity(left: str, right: str) -> float:
    """Overlap of two statements' content words, as a share of their union."""
    a, b = content_words(left), content_words(right)
    if not a or not b:
        return 0.0
    return len(a & b) / len(a | b)


def validate_schema(registry: dict, schema: dict) -> list[str]:
    """Validate the registry against the published schema, one message per violation."""
    try:
        from jsonschema import Draft202012Validator
    except ImportError as exc:
        raise HarnessError(
            "the pinned validator is not installed - "
            "python3 -m venv .venv && .venv/bin/pip install -r "
            "schemas/invariant-registry/requirements.txt"
        ) from exc

    validator = Draft202012Validator(schema)
    messages = []
    for error in sorted(validator.iter_errors(registry), key=lambda e: list(e.absolute_path)):
        location = "/".join(str(part) for part in error.absolute_path) or "<document>"
        messages.append(f"{location}: {error.message}")
    return messages


def check_ids(entries: list[dict]) -> list[str]:
    """Every entry is keyed by a unique id."""
    seen: set[str] = set()
    violations = []
    for entry in entries:
        entry_id = entry.get("id", "<unnamed>")
        if entry_id in seen:
            violations.append(f"{entry_id}: declared more than once")
        seen.add(entry_id)
    return violations


def entry_targets(entry: dict, gates: dict) -> list[str]:
    """Paths at which this entry's invariant is enforced, for reference comparison."""
    targets = []
    for key in ("doc_path", "fail_fast_symbol", "test_id"):
        value = entry.get(key)
        if value:
            targets.append(re.split(r"[:]{1,2}", value)[0])
    gate = gates.get(entry.get("gate_id", ""))
    if gate:
        targets.append(gate["path"])
    return targets


def check_restatement(entries: list[dict], gates: dict) -> list[str]:
    """No invariant is declared twice: a second entry references the first by path.

    A pair of near-identical statements is legitimate only when one entry points at the
    other's target - through references (a weaker rung deferring to a stronger one) or
    superseded_by (a replacement shipping alongside the incumbent it will retire).
    """
    violations = []
    for i, first in enumerate(entries):
        for second in entries[i + 1 :]:
            score = similarity(first.get("statement", ""), second.get("statement", ""))
            if score < RESTATEMENT_SIMILARITY:
                continue
            links = set()
            for entry, other in ((first, second), (second, first)):
                pointers = set(entry.get("references", []))
                if entry.get("superseded_by"):
                    pointers.add(entry["superseded_by"])
                if pointers & set(entry_targets(other, gates)):
                    links.add(entry["id"])
            if not links:
                violations.append(
                    f"{first['id']} and {second['id']}: statements overlap {score:.2f} "
                    "and neither references the other's target by path - declare the "
                    "invariant once and reference it, never restate it"
                )
    return violations


def check_resolution(
    registry: dict, repos: dict[str, Path | None], self_root: Path
) -> tuple[list[str], list[str]]:
    """Every declared path of a shipped entry exists; unresolvable repos are reported.

    Returns (violations, unresolved repo names).
    """
    violations = []
    unresolved: set[str] = set()

    def probe(qualified: str, where: str) -> None:
        target = resolve_path(repos, self_root, qualified)
        if target is None:
            unresolved.add(qualified.partition("/")[0])
        elif not target.exists():
            violations.append(f"{where}: {qualified} does not exist")

    declared_gates = registry["gates"]
    for gate_id, gate in declared_gates.items():
        if gate["status"] == "active":
            probe(gate["path"], f"gates/{gate_id}")

    for entry in registry["invariants"]:
        gate_id = entry.get("gate_id")
        if gate_id and gate_id not in declared_gates:
            violations.append(f"{entry['id']}: gate_id {gate_id} is not in the gates map")
        if entry["status"] != "shipped":
            continue
        for reference in entry.get("references", []):
            probe(reference, f"{entry['id']}/references")
        if entry.get("doc_path"):
            probe(entry["doc_path"], f"{entry['id']}/doc_path")

    return violations, sorted(unresolved)


def discover_plugin_hook_gates(root: Path, globs: list[str]) -> dict[str, str]:
    """Find every PreToolUse hook command a plugin registers, keyed by repo-relative path.

    Only PreToolUse can deny, so only PreToolUse hooks are gates; a SessionStart hook
    contributes context and refuses nothing.
    """
    found: dict[str, str] = {}
    for pattern in globs:
        for manifest in sorted(root.glob(pattern)):
            try:
                with manifest.open("r", encoding="utf-8") as handle:
                    hooks = json.load(handle).get("hooks", {})
            except (OSError, json.JSONDecodeError):
                continue
            owner = manifest.relative_to(root).parts[1]
            for matcher in hooks.get("PreToolUse", []):
                for hook in matcher.get("hooks", []):
                    match = _HOOK_COMMAND_PATH.search(hook.get("command", ""))
                    if match:
                        script = manifest.parent / match.group(1)
                        found[str(script.relative_to(root))] = owner
    return found


def discover_ci_wired_guards(root: Path, globs: list[str]) -> dict[str, str]:
    """Find every guard entrypoint a workflow file runs, keyed by repo-relative path.

    Text-level, not YAML-semantic: a path that appears in a workflow is taken as run by
    it. A guard no workflow mentions gates nothing and is not discovered.
    """
    found: dict[str, str] = {}
    for pattern in globs:
        for workflow in sorted(root.glob(pattern)):
            try:
                text = workflow.read_text(encoding="utf-8")
            except OSError:
                continue
            for path in _CI_GUARD_PATH.findall(text):
                if (root / path).is_file():
                    found[path] = SELF_REPO
    return found


def check_completeness(
    registry: dict, repos: dict[str, Path | None], self_root: Path
) -> tuple[list[str], list[str], list[str]]:
    """Every discovered in-scope gate is declared here.

    Returns (violations, unclaimed out-of-scope gates, unresolved roots).
    """
    declared = {gate["path"] for gate in registry["gates"].values()}
    entries_by_gate = {
        entry["gate_id"] for entry in registry["invariants"] if entry.get("gate_id")
    }
    violations: list[str] = []
    unclaimed: list[str] = []
    unresolved: list[str] = []

    for root_spec in registry["discovery"]["roots"]:
        repo = root_spec["repo"]
        root = repo_dir(repos, self_root, repo)
        if root is None:
            unresolved.append(repo)
            continue
        if root_spec["strategy"] == "claude-plugin-hooks":
            discovered = discover_plugin_hook_gates(root, root_spec["globs"])
        else:
            discovered = discover_ci_wired_guards(root, root_spec["globs"])
        in_scope = set(root_spec["in_scope_owners"])
        for relative, owner in sorted(discovered.items()):
            qualified = f"{repo}/{relative}"
            if owner not in in_scope:
                unclaimed.append(qualified)
            elif qualified not in declared:
                violations.append(
                    f"{qualified}: gate is shipped by {owner} and declares no invariant "
                    "- add its entry to the registry"
                )

    for gate_id in registry["gates"]:
        if gate_id not in entries_by_gate:
            violations.append(f"gates/{gate_id}: declared with no invariant entry naming it")

    return violations, unclaimed, unresolved


def check_published_blocks(
    registry: dict, repos: dict[str, Path | None], self_root: Path
) -> list[str]:
    """A gate that publishes its own declaration agrees with what the registry carries.

    A gate is free to state its rung and fail direction in its own header where a reader
    of that gate will see it; the registry is where those statements are aggregated. Two
    copies means the pair can drift, so the copies are compared.
    """
    by_id = {entry["id"]: entry for entry in registry["invariants"]}
    violations = []
    for gate_id, gate in registry["gates"].items():
        target = resolve_path(repos, self_root, gate["path"])
        if target is None or not target.is_file():
            continue
        try:
            text = target.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        block = _PUBLISHED_BLOCK.search(text)
        if not block:
            continue
        stripped = "\n".join(
            re.sub(r"^\s*(#|//|--)\s?", "", line) for line in block.group("body").splitlines()
        )
        try:
            published = json.loads(stripped)
        except json.JSONDecodeError as exc:
            violations.append(f"{gate['path']}: published declaration is not valid JSON ({exc})")
            continue
        for item in published:
            entry = by_id.get(item.get("id", ""))
            if entry is None:
                violations.append(
                    f"{gate['path']}: publishes {item.get('id')!r}, which the registry "
                    "does not carry"
                )
                continue
            for field in ("statement", "rung", "fail_direction", "blast_radius"):
                if item.get(field) != entry.get(field):
                    violations.append(
                        f"{entry['id']}: {field} in {gate['path']} differs from the registry"
                    )
            if item.get("gate_id", gate_id) != entry.get("gate_id"):
                violations.append(f"{entry['id']}: gate_id in {gate['path']} differs")
    return violations


def run(registry_path: Path, schema_path: Path, overrides: list[str], require_roots: bool) -> int:
    """Run every check and print one line per violation. Returns the exit code."""
    registry = load_json(registry_path)
    schema = load_json(schema_path)
    self_root = SELF_DIR.parent.parent
    repos = resolve_repos(self_root, overrides)

    schema_violations = validate_schema(registry, schema)
    if schema_violations:
        for message in schema_violations:
            print(f"invariant-registry: {message}")
        print(f"FAIL - {len(schema_violations)} schema violation(s); later checks not run.")
        return 1

    violations = check_ids(registry["invariants"])
    violations += check_restatement(registry["invariants"], registry["gates"])
    resolution_violations, unresolved_paths = check_resolution(registry, repos, self_root)
    violations += resolution_violations
    completeness, unclaimed, unresolved_roots = check_completeness(registry, repos, self_root)
    violations += completeness
    violations += check_published_blocks(registry, repos, self_root)

    unresolved = sorted(set(unresolved_paths) | set(unresolved_roots))
    if unresolved:
        detail = ", ".join(unresolved)
        if require_roots:
            violations.append(f"checkout not present for: {detail} (--require-roots)")
        else:
            print(
                f"invariant-registry: NOT CHECKED - no checkout present for {detail}; "
                "paths and gate discovery in those repos were skipped, not passed."
            )
    for gate in unclaimed:
        print(f"invariant-registry: unclaimed - {gate} ships a gate outside the declared scope.")

    entries = registry["invariants"]
    counted = {rung: sum(1 for e in entries if e["rung"] == rung) for rung in range(1, 6)}
    summary = ", ".join(f"rung {rung}: {count}" for rung, count in counted.items())

    if violations:
        for message in violations:
            print(f"invariant-registry: {message}")
        print(f"FAIL - {len(violations)} violation(s) across {len(entries)} entries ({summary}).")
        return 1

    machine_checked = sum(1 for e in entries if e["rung"] <= 3 and e["status"] == "shipped")
    print(
        f"OK - {len(entries)} entries ({summary}). "
        f"{machine_checked} carry a consumer that asserts them; the rest are "
        "completeness-only by rung or unasserted until they ship."
    )
    return 0


def main(argv: list[str] | None = None) -> int:
    """Parse arguments and run the registry lint."""
    parser = argparse.ArgumentParser(
        description="Invariant-registry lint - validity, completeness and non-duplication."
    )
    parser.add_argument("--registry", default=None, help="Registry document (default: alongside).")
    parser.add_argument("--schema", default=None, help="Registry schema (default: alongside).")
    parser.add_argument(
        "--repo",
        action="append",
        default=[],
        metavar="NAME=PATH",
        help="Checkout for a repo named in a path (default: a sibling of this checkout).",
    )
    parser.add_argument(
        "--require-roots",
        action="store_true",
        help="Fail instead of reporting when a declared discovery root is not present.",
    )
    args = parser.parse_args(argv)

    try:
        return run(
            Path(args.registry).resolve() if args.registry else REGISTRY_PATH,
            Path(args.schema).resolve() if args.schema else SCHEMA_PATH,
            args.repo,
            args.require_roots,
        )
    except HarnessError as exc:
        print(f"invariant-registry: cannot run - {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
