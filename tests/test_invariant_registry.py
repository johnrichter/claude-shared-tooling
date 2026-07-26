#!/usr/bin/env python3
"""Regression for schemas/invariant-registry — the SC-ENFORCE invariant registry.

Two surfaces are exercised: the schema (draft-2020-12 validity, and per-entry acceptance
and rejection of every shape the contract pins), and the lint (restatement detection and
discovery-based completeness). Rung-1 symbol resolution and rung-3 test-id resolution are
NOT exercised here — those belong to tooling/invariant-lint, per the schema's
x-verification-model — and neither is the rung-2 declared-vs-actual firing check.
"""
from __future__ import annotations

import copy
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
_REG_DIR = _REPO_ROOT / "schemas" / "invariant-registry"
_SCHEMA_PATH = _REG_DIR / "invariant-registry.schema.json"
_REGISTRY_PATH = _REG_DIR / "invariant-registry.json"


def _load_check():
    """Load check.py under a unique module name so it cannot collide with a sibling check.py."""
    spec = importlib.util.spec_from_file_location("invariant_registry_check", _REG_DIR / "check.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


check = _load_check()

from jsonschema import Draft202012Validator  # noqa: E402

SCHEMA = json.loads(_SCHEMA_PATH.read_text(encoding="utf-8"))
REGISTRY = json.loads(_REGISTRY_PATH.read_text(encoding="utf-8"))

# A validator for a single entry, so a rejection test asserts on the entry that caused it
# rather than on the whole document.
_ENTRY_SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$defs": SCHEMA["$defs"],
    "$ref": "#/$defs/invariant",
}
_ENTRY_VALIDATOR = Draft202012Validator(_ENTRY_SCHEMA)

_STATEMENT = "The alpha beta gamma delta epsilon invariant holds on every gated call here."
_BLAST = "A false enforcement costs one re-run; a missed one loses unrecoverable committed work."
_REASON = "The stronger rung cannot see the whole tree, so this is the strongest reachable rung."


def _entry(rung: int, **overrides) -> dict:
    """A minimal entry that validates at the given rung, before any override is applied."""
    base = {
        "id": "owner.artifact.case",
        "statement": _STATEMENT,
        "rung": rung,
        "fail_direction": "closed",
        "blast_radius": _BLAST,
        "owner": "owner",
        "status": "shipped",
    }
    consumer = {
        1: {"fail_fast_symbol": "owner/pkg/mod.go:Symbol"},
        2: {"trigger": "PreToolUse:Write on some path scope", "gate_id": "owner.gate"},
        3: {"reason_lower_rung": _REASON, "test_id": "owner/tests/test_x.py::CaseTests"},
        4: {
            "reason_lower_rung": _REASON,
            "compliance_floors": {"claude-opus-5": None},
            "measurement_status": "declared-unmeasured",
            "register_entry_id": "rp-owner-case",
        },
        5: {"reason_lower_rung": _REASON, "doc_path": "owner/docs/policy.md"},
    }[rung]
    base.update(consumer)
    base.update(overrides)
    return base


def _errors(entry: dict) -> list[str]:
    return [e.message for e in _ENTRY_VALIDATOR.iter_errors(entry)]


def _without(entry: dict, *keys: str) -> dict:
    out = copy.deepcopy(entry)
    for key in keys:
        out.pop(key, None)
    return out


class SchemaValidityTests(unittest.TestCase):
    def test_schema_is_valid_draft_2020_12(self):
        Draft202012Validator.check_schema(SCHEMA)

    def test_seed_registry_validates(self):
        errors = sorted(Draft202012Validator(SCHEMA).iter_errors(REGISTRY), key=lambda e: e.message)
        self.assertEqual([], [f"{list(e.absolute_path)}: {e.message}" for e in errors])

    def test_each_minimal_entry_validates(self):
        for rung in range(1, 6):
            with self.subTest(rung=rung):
                self.assertEqual([], _errors(_entry(rung)))


class RequiredCoreFieldTests(unittest.TestCase):
    def test_missing_rung_fails(self):
        self.assertTrue(_errors(_without(_entry(2), "rung")))

    def test_missing_fail_direction_fails(self):
        self.assertTrue(_errors(_without(_entry(2), "fail_direction")))

    def test_missing_blast_radius_fails(self):
        self.assertTrue(_errors(_without(_entry(2), "blast_radius")))

    def test_missing_owner_fails_at_rung_4(self):
        self.assertTrue(_errors(_without(_entry(4), "owner")))

    def test_lower_rung_without_reason_fails(self):
        for rung in (3, 4, 5):
            with self.subTest(rung=rung):
                self.assertTrue(_errors(_without(_entry(rung), "reason_lower_rung")))

    def test_deny_gate_needs_no_reason(self):
        self.assertEqual([], _errors(_without(_entry(2), "reason_lower_rung")))


class PerRungConsumerFieldTests(unittest.TestCase):
    def test_rung_1_without_symbol_fails(self):
        self.assertTrue(_errors(_without(_entry(1), "fail_fast_symbol")))

    def test_rung_2_without_trigger_fails(self):
        self.assertTrue(_errors(_without(_entry(2), "trigger")))

    def test_rung_2_without_gate_id_fails(self):
        self.assertTrue(_errors(_without(_entry(2), "gate_id")))

    def test_rung_3_without_test_id_fails(self):
        self.assertTrue(_errors(_without(_entry(3), "test_id")))

    def test_rung_4_without_floor_fails(self):
        self.assertTrue(_errors(_without(_entry(4), "compliance_floors")))

    def test_rung_5_without_doc_path_fails(self):
        self.assertTrue(_errors(_without(_entry(5), "doc_path")))

    def test_rung_4_and_5_pass_completeness_only(self):
        # Nothing beyond field presence is asserted at these rungs; a complete entry validates.
        self.assertEqual([], _errors(_entry(4)))
        self.assertEqual([], _errors(_entry(5)))

    def test_entry_may_not_claim_a_stronger_rungs_field(self):
        # A rung-1 entry carrying a rung-2 trigger claims a verification it does not have.
        self.assertTrue(_errors(_entry(1, trigger="PreToolUse:Write on some path scope")))
        # A rung-5 doc entry carrying a rung-3 test id, likewise.
        self.assertTrue(_errors(_entry(5, test_id="owner/tests/test_x.py::CaseTests")))

    def test_measured_status_requires_rates_and_nonnull_floor(self):
        measured = _entry(4, measurement_status="measured")
        self.assertTrue(_errors(measured))  # no measured_rates, floor still null
        measured.update(
            compliance_floors={"claude-opus-5": 0.9},
            measured_rates={"claude-opus-5": 0.95},
            measured_at="2026-07-26T00:00:00Z",
        )
        self.assertEqual([], _errors(measured))


class RestatementTests(unittest.TestCase):
    GATES = {"owner.gate": {"path": "marketplace/x/gate.sh", "owner": "owner",
                            "kind": "pretooluse-hook", "status": "active"}}

    def _pair(self, with_reference: bool) -> list[dict]:
        strong = _entry(2, id="owner.gate.case")
        weak = _entry(
            5,
            id="owner.doc.case",
            statement=_STATEMENT + " Restated.",
            doc_path="owner/docs/p.md",
            references=[self.GATES["owner.gate"]["path"]] if with_reference else None,
        )
        if not with_reference:
            weak.pop("references", None)
        return [strong, weak]

    def test_restatement_without_reference_fails(self):
        violations = check.check_restatement(self._pair(with_reference=False), self.GATES)
        self.assertTrue(violations)

    def test_restatement_with_reference_passes(self):
        violations = check.check_restatement(self._pair(with_reference=True), self.GATES)
        self.assertEqual([], violations)

    def test_distinct_statements_do_not_collide(self):
        a = _entry(2, id="owner.gate.a")
        b = _entry(2, id="owner.gate.b",
                   statement="A committed binary is refused unless it is the one documented path.")
        self.assertEqual([], check.check_restatement([a, b], self.GATES))


class CompletenessTests(unittest.TestCase):
    """The class the registry names as this invariant's rung-3 target."""

    def _marketplace(self, owner: str, command: str) -> Path:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        root = Path(td.name)
        hooks_dir = root / "plugins" / owner / "hooks"
        hooks_dir.mkdir(parents=True)
        (hooks_dir / f"{owner}-gate.sh").write_text("#!/bin/sh\n", encoding="utf-8")
        manifest = {"hooks": {"PreToolUse": [{"matcher": "Write|Edit",
                                              "hooks": [{"type": "command", "command": command}]}]}}
        (hooks_dir / "hooks.json").write_text(json.dumps(manifest), encoding="utf-8")
        return root

    def _registry(self, in_scope: list[str]) -> dict:
        return {
            "schema": "invariant-registry@1.0.0",
            "discovery": {"roots": [{"repo": "marketplace", "strategy": "claude-plugin-hooks",
                                     "globs": ["plugins/*/hooks/hooks.json"],
                                     "in_scope_owners": in_scope}]},
            "gates": {},
            "invariants": [],
        }

    def test_in_scope_undeclared_gate_is_a_violation(self):
        root = self._marketplace("foo", "$CLAUDE_PLUGIN_ROOT/hooks/foo-gate.sh")
        repos = {"marketplace": root, "ai-shared-lib": _REPO_ROOT}
        violations, unclaimed, unresolved = check.check_completeness(
            self._registry(["foo"]), repos, _REPO_ROOT)
        self.assertTrue(any("foo-gate.sh" in v for v in violations))
        self.assertEqual([], unclaimed)
        self.assertEqual([], unresolved)

    def test_out_of_scope_gate_is_unclaimed_not_a_violation(self):
        root = self._marketplace("foo", "$CLAUDE_PLUGIN_ROOT/hooks/foo-gate.sh")
        repos = {"marketplace": root, "ai-shared-lib": _REPO_ROOT}
        violations, unclaimed, _ = check.check_completeness(
            self._registry([]), repos, _REPO_ROOT)
        self.assertEqual([], violations)
        self.assertTrue(any("foo-gate.sh" in u for u in unclaimed))

    def test_absent_checkout_is_unresolved_not_a_violation(self):
        repos = {"marketplace": None, "ai-shared-lib": _REPO_ROOT}
        violations, _, unresolved = check.check_completeness(
            self._registry(["foo"]), repos, _REPO_ROOT)
        self.assertEqual([], violations)
        self.assertEqual(["marketplace"], unresolved)


class SeedLintTests(unittest.TestCase):
    def test_seed_registry_passes_the_lint(self):
        # Only this checkout is present; sibling roots report NOT CHECKED, not a failure.
        self.assertEqual(0, check.run(_REGISTRY_PATH, _SCHEMA_PATH, [], require_roots=False))


if __name__ == "__main__":
    unittest.main()
