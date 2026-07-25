#!/usr/bin/env python3
"""Unit tests for roster-gen: the one-rendering-pass generator over the model roster.

The package lives at `tooling/roster-gen/roster_gen/` (a hyphenated parent directory, so it
is not importable as a dotted path); that directory is put on `sys.path` here, exactly as
`generate.py` does. Fixture rosters and repo roots are throwaway temp trees -- nothing here
depends on the real committed marketplace repo except the one opportunistic case gated on
ROSTER_GEN_TEST_MARKETPLACE_ROOT (skipped when unset, e.g. in either repo's isolated CI job).

Coverage (mirrors the generator's stated invariants):
    1. Rendering is deterministic and idempotent: re-rendering the same roster, and
       re-running the CLI's `generate`, reproduce the same bytes.
    2. The plan-enum and gate-allowlist projections are distinct sets built from the same
       roster snapshot (new-work-only vs. everything-but-retired).
    3. The gate allowlist may only grow: a roster row moving to `retired` after that ID
       is already on disk fails loudly instead of silently narrowing the allowlist.
    4. The plan-schema enum always carries the `inherit` sentinel, checked against a fixed
       requirement rather than tautologically against the roster's own declared list.
    5. A planted single-field roster change reaches only the outputs that read that field.
    6. Every rendered output carries a generated-by header naming the roster tag.
    7. `check` catches drift (a hand-edit, or an unregenerated roster change) and a missing
       patch target; `generate` never partially writes on a rendering failure.
"""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_TOOLING = Path(__file__).resolve().parent.parent / "tooling" / "roster-gen"
if str(_TOOLING) not in sys.path:
    sys.path.insert(0, str(_TOOLING))

from roster_gen import order, render  # noqa: E402
from roster_gen.roster import RosterError, load as load_roster  # noqa: E402
from roster_gen.targets import TARGETS, resolve  # noqa: E402

_GENERATE_PY = _TOOLING / "generate.py"
_REPO_ROOT = Path(__file__).resolve().parent.parent


def _price(output: float | None) -> dict[str, float] | None:
    if output is None:
        return None
    return {"input": 1.0, "cache_write_5m": 1.25, "cache_write_1h": 2.0, "cache_read": 0.1, "output": output}


def _model(
    *,
    selectable: str = "new-work",
    family: str = "sonnet",
    generation: tuple[int, ...] = (5,),
    cross_family_rank: int | None = 5,
    list_output: float | None = 15.0,
    contract_output: float | None = None,
) -> dict:
    return {
        "batch_discount": 0.5,
        "context_window": 200000,
        "cross_family_rank": cross_family_rank,
        "deprecation_date": None,
        "effort_available": ["low", "medium", "high"],
        "effort_exempt": False,
        "family": family,
        "generation": list(generation),
        "knowledge_cutoff": "2026-01",
        "lifecycle": "active",
        "max_output_tokens": 64000,
        "min_cacheable_prefix": 1024,
        "price": {"contract": _price(contract_output), "list": _price(list_output)},
        "release_date": None,
        "selectable": selectable,
    }


def _roster(models: dict[str, dict], sentinels: tuple[str, ...] = ("inherit",)) -> dict:
    return {
        "_schema_version": 1,
        "_source": "test fixture",
        "_unit": "USD per 1,000,000 tokens",
        "_as_of": "2026-01-01",
        "effort_exempt_sentinels": list(sentinels),
        "models": models,
    }


_BOOTSTRAP_PLAN_SCHEMA = json.dumps(
    {
        "$defs": {
            "task": {
                "properties": {
                    "model": {"type": "string", "enum": ["placeholder"], "description": "old", "$comment": "old"},
                }
            }
        }
    }
)

_BOOTSTRAP_BUILD_ENGINE = "const other = 1\nconst DEFAULT_RATES = {\n  'placeholder': 1.0,\n}\nconst after = 2\n"

_BOOTSTRAP_TIERING_DOC = (
    "# Tiering\n\n"
    "## Current-generation capability snapshot\n\n"
    "| Model | Capability anchor |\n"
    "|---|---|\n"
    "| `placeholder` | n/a |\n"
    "\n## Next section\n"
)


_BOOTSTRAP_TEXT_BY_KIND = {
    "plan-schema.json": _BOOTSTRAP_PLAN_SCHEMA,
    "build-engine.workflow.js": _BOOTSTRAP_BUILD_ENGINE,
    "governance-model-tiering.md": _BOOTSTRAP_TIERING_DOC,
}


def _seed_patch_targets(roots: dict[str, Path]) -> None:
    """Pre-create every target's parent directory at its real rel_path under the fixture
    roots (targets.py hardcodes those paths, so a fixture must mirror them), and seed a
    "patch" target's existing content — a "whole" or "allowlist" target has no such
    prerequisite, generate.py replaces or creates it outright."""
    for target in TARGETS:
        path = resolve(target, roots)
        path.parent.mkdir(parents=True, exist_ok=True)
        if target.kind == "patch":
            path.write_text(_BOOTSTRAP_TEXT_BY_KIND[path.name], encoding="utf-8")


class RenderSpecsTests(unittest.TestCase):
    def test_idempotent_and_deterministic_order(self) -> None:
        models = {
            "z-model": _model(family="haiku", cross_family_rank=0, generation=(4, 5)),
            "a-model": _model(family="opus", cross_family_rank=9, generation=(5,)),
        }
        roster = _roster(models)
        first = render.render_specs(roster, "v1")
        second = render.render_specs(roster, "v1")
        self.assertEqual(first, second)
        # Rendering re-sorts by capability_order() regardless of the input dict's insertion
        # order, so field order in the input never leaks into the output.
        reordered = render.render_specs(_roster({"a-model": models["a-model"], "z-model": models["z-model"]}), "v1")
        self.assertEqual(first, reordered)
        doc = json.loads(first)
        self.assertEqual(list(doc["model"].keys()), ["a-model", "z-model"])  # opus (rank 9) before haiku (rank 0)

    def test_generated_by_header_names_tag(self) -> None:
        roster = _roster({"m": _model()})
        doc = json.loads(render.render_specs(roster, "schemas/model-roster/v1.2.3"))
        self.assertIn("schemas/model-roster/v1.2.3", doc["_generated_by"])

    def test_unpriced_row_omitted(self) -> None:
        roster = _roster({"priced": _model(), "unpriced": _model(list_output=None)})
        doc = json.loads(render.render_specs(roster, "v1"))
        self.assertIn("priced", doc["model"])
        self.assertNotIn("unpriced", doc["model"])


class PlanSchemaEnumTests(unittest.TestCase):
    def test_enum_is_new_work_plus_sentinels_only(self) -> None:
        models = {
            "claude-sonnet-5": _model(selectable="new-work"),
            "claude-opus-4-5": _model(selectable="legacy-pin-only"),
            "claude-retired-1": _model(selectable="retired"),
        }
        out = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(models), "v1")
        enum = json.loads(out)["$defs"]["task"]["properties"]["model"]["enum"]
        self.assertEqual(enum, ["claude-sonnet-5", "inherit"])

    def test_unlisted_id_appended_not_dropped(self) -> None:
        models = {"claude-sonnet-5": _model(), "claude-new-arrival": _model()}
        out = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(models), "v1")
        enum = json.loads(out)["$defs"]["task"]["properties"]["model"]["enum"]
        self.assertIn("claude-new-arrival", enum)
        self.assertEqual(enum[-1], "inherit")  # sentinel always trails

    def test_missing_required_sentinel_fails_loud(self) -> None:
        """The guard checks a fixed requirement, not the roster's own list parroted back at
        itself: a roster that drops 'inherit' from effort_exempt_sentinels must still fail."""
        roster = _roster({"claude-sonnet-5": _model()}, sentinels=())
        with self.assertRaises(ValueError) as ctx:
            render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, roster, "v1")
        self.assertIn("inherit", str(ctx.exception))

    def test_generated_by_comment_names_tag(self) -> None:
        out = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster({"m": _model()}), "v9.9.9")
        comment = json.loads(out)["$defs"]["task"]["properties"]["model"]["$comment"]
        self.assertIn("v9.9.9", comment)


class GateAllowlistTests(unittest.TestCase):
    def test_excludes_retired_includes_legacy_pin_only(self) -> None:
        models = {
            "claude-sonnet-5": _model(selectable="new-work"),
            "claude-opus-4-5": _model(selectable="legacy-pin-only"),
            "claude-retired-1": _model(selectable="retired"),
        }
        out = render.render_gate_allowlist(_roster(models), "v1")
        ids = {ln for ln in out.splitlines() if ln and not ln.startswith("#")}
        self.assertEqual(ids, {"claude-sonnet-5", "claude-opus-4-5"})

    def test_plan_enum_and_gate_allowlist_are_distinct_projections(self) -> None:
        """AC2's point: new-work-only vs. everything-but-retired must be allowed to disagree."""
        models = {
            "claude-sonnet-5": _model(selectable="new-work"),
            "claude-opus-4-5": _model(selectable="legacy-pin-only"),
        }
        roster = _roster(models)
        enum = json.loads(render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, roster, "v1"))
        enum_ids = set(enum["$defs"]["task"]["properties"]["model"]["enum"])
        allowlist_ids = {
            ln for ln in render.render_gate_allowlist(roster, "v1").splitlines() if ln and not ln.startswith("#")
        }
        self.assertNotEqual(enum_ids, allowlist_ids)
        self.assertIn("claude-opus-4-5", allowlist_ids)
        self.assertNotIn("claude-opus-4-5", enum_ids)  # legacy-pin-only, not new-work

    def test_narrowing_an_already_allowed_id_fails(self) -> None:
        roster = _roster({"claude-sonnet-5": _model(selectable="retired")})
        with self.assertRaises(render.NarrowingError) as ctx:
            render.render_gate_allowlist(roster, "v1", existing_ids={"claude-sonnet-5", "claude-opus-5"})
        self.assertIn("claude-sonnet-5", str(ctx.exception))

    def test_growing_the_allowlist_succeeds(self) -> None:
        roster = _roster({"claude-sonnet-5": _model(), "claude-new-model": _model()})
        out = render.render_gate_allowlist(roster, "v1", existing_ids={"claude-sonnet-5"})
        ids = {ln for ln in out.splitlines() if ln and not ln.startswith("#")}
        self.assertEqual(ids, {"claude-sonnet-5", "claude-new-model"})


class BuildEngineAndTieringDocTests(unittest.TestCase):
    def test_build_engine_bootstrap_then_idempotent_regenerate(self) -> None:
        roster = _roster({"claude-sonnet-5": _model(list_output=15.0)})
        first = render.patch_build_engine(_BOOTSTRAP_BUILD_ENGINE, roster, "v1")
        self.assertIn("const other = 1", first)  # untouched surrounding code preserved
        self.assertIn("const after = 2", first)
        self.assertIn("'claude-sonnet-5': 15.0", first)
        second = render.patch_build_engine(first, roster, "v1")
        self.assertEqual(first, second)

    def test_tiering_doc_bootstrap_then_idempotent_regenerate(self) -> None:
        roster = _roster({"claude-sonnet-5": _model(selectable="new-work")})
        first = render.patch_tiering_doc(_BOOTSTRAP_TIERING_DOC, roster, "v1")
        self.assertIn("## Next section", first)  # untouched surrounding text preserved
        self.assertIn("`claude-sonnet-5`", first)
        second = render.patch_tiering_doc(first, roster, "v1")
        self.assertEqual(first, second)


class PlantedRowChangeScopingTests(unittest.TestCase):
    """A single-field roster edit must reach only the outputs that read that field."""

    def test_price_change_reaches_specs_and_rates_not_enum_or_allowlist(self) -> None:
        base_models = {"claude-sonnet-5": _model(list_output=15.0)}
        changed_models = {"claude-sonnet-5": _model(list_output=99.0)}

        specs_before = render.render_specs(_roster(base_models), "v1")
        specs_after = render.render_specs(_roster(changed_models), "v1")
        self.assertNotEqual(specs_before, specs_after)

        rates_before = render.patch_build_engine(_BOOTSTRAP_BUILD_ENGINE, _roster(base_models), "v1")
        rates_after = render.patch_build_engine(_BOOTSTRAP_BUILD_ENGINE, _roster(changed_models), "v1")
        self.assertNotEqual(rates_before, rates_after)

        enum_before = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(base_models), "v1")
        enum_after = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(changed_models), "v1")
        self.assertEqual(enum_before, enum_after)  # price is not a plan-enum input

        allow_before = render.render_gate_allowlist(_roster(base_models), "v1")
        allow_after = render.render_gate_allowlist(_roster(changed_models), "v1")
        self.assertEqual(allow_before, allow_after)  # price is not a gate-allowlist input

    def test_selectable_change_reaches_enum_and_tiering_doc_not_specs_or_rates(self) -> None:
        base_models = {"claude-sonnet-5": _model(selectable="new-work")}
        changed_models = {"claude-sonnet-5": _model(selectable="legacy-pin-only")}

        enum_before = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(base_models), "v1")
        enum_after = render.patch_plan_schema(_BOOTSTRAP_PLAN_SCHEMA, _roster(changed_models), "v1")
        self.assertNotEqual(enum_before, enum_after)

        doc_before = render.patch_tiering_doc(_BOOTSTRAP_TIERING_DOC, _roster(base_models), "v1")
        doc_after = render.patch_tiering_doc(_BOOTSTRAP_TIERING_DOC, _roster(changed_models), "v1")
        self.assertNotEqual(doc_before, doc_after)

        specs_before = render.render_specs(_roster(base_models), "v1")
        specs_after = render.render_specs(_roster(changed_models), "v1")
        self.assertEqual(specs_before, specs_after)  # selectable is not a specs input

        rates_before = render.patch_build_engine(_BOOTSTRAP_BUILD_ENGINE, _roster(base_models), "v1")
        rates_after = render.patch_build_engine(_BOOTSTRAP_BUILD_ENGINE, _roster(changed_models), "v1")
        self.assertEqual(rates_before, rates_after)  # selectable is not a build-engine input


class RosterLoadTests(unittest.TestCase):
    def _write(self, tmp: Path, document: dict) -> Path:
        path = tmp / "model-roster.json"
        path.write_text(json.dumps(document), encoding="utf-8")
        return path

    def test_forward_schema_version_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = self._write(Path(tmp), _roster({"m": _model()}) | {"_schema_version": 999})
            with self.assertRaises(RosterError):
                load_roster(path)

    def test_missing_effort_exempt_sentinels_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            doc = _roster({"m": _model()})
            doc["effort_exempt_sentinels"] = []
            path = self._write(Path(tmp), doc)
            with self.assertRaises(RosterError):
                load_roster(path)

    def test_missing_roster_file_refused(self) -> None:
        with self.assertRaises(RosterError):
            load_roster(Path("/nonexistent/model-roster.json"))


class CliIntegrationTests(unittest.TestCase):
    """Drives generate.py as a subprocess against throwaway two-root temp trees, exactly as
    a real invocation would (README's --ai-shared-lib-root / --marketplace-root split)."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.ai_shared_lib_root = self.tmp / "ai-shared-lib"
        self.marketplace_root = self.tmp / "marketplace"
        self.ai_shared_lib_root.mkdir()
        self.marketplace_root.mkdir()
        self.roots = {"ai-shared-lib": self.ai_shared_lib_root, "marketplace": self.marketplace_root}
        _seed_patch_targets(self.roots)
        self.roster_path = self.tmp / "model-roster.json"

    def _write_roster(self, models: dict[str, dict]) -> None:
        self.roster_path.write_text(json.dumps(_roster(models)), encoding="utf-8")

    def _run(self, command: str, tag: str = "v1") -> subprocess.CompletedProcess:
        return subprocess.run(
            [
                sys.executable,
                str(_GENERATE_PY),
                "--roster",
                str(self.roster_path),
                "--tag",
                tag,
                "--ai-shared-lib-root",
                str(self.ai_shared_lib_root),
                "--marketplace-root",
                str(self.marketplace_root),
                command,
            ],
            capture_output=True,
            text=True,
        )

    def _all_target_texts(self) -> dict[str, str]:
        return {f"{t.repo}:{t.rel_path}": resolve(t, self.roots).read_text(encoding="utf-8") for t in TARGETS}

    def _path_ending(self, suffix: str) -> Path:
        (target,) = (t for t in TARGETS if t.rel_path.endswith(suffix))
        return resolve(target, self.roots)

    def test_generate_twice_is_byte_identical(self) -> None:
        # First run establishes hooks/model-roster's on-disk allowlist under an empty root, so
        # nothing narrows against a prior state on this first pass.
        self._write_roster({"claude-sonnet-5": _model()})
        first_run = self._run("generate")
        self.assertEqual(first_run.returncode, 0, first_run.stderr)
        first_texts = self._all_target_texts()

        second_run = self._run("generate")
        self.assertEqual(second_run.returncode, 0, second_run.stderr)
        second_texts = self._all_target_texts()

        self.assertEqual(first_texts, second_texts)

    def test_generated_by_header_names_tag_in_every_target(self) -> None:
        self._write_roster({"claude-sonnet-5": _model()})
        result = self._run("generate", tag="schemas/model-roster/v7.0.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        for name, text in self._all_target_texts().items():
            self.assertIn("schemas/model-roster/v7.0.0", text, f"{name} missing generated-by tag")

    def test_check_passes_on_a_clean_tree_then_catches_a_hand_edit(self) -> None:
        self._write_roster({"claude-sonnet-5": _model()})
        self.assertEqual(self._run("generate").returncode, 0)
        self.assertEqual(self._run("check").returncode, 0)

        # Hand-edit a "whole" target (not the allowlist -- an allowlist edit that adds an
        # ID is indistinguishable from narrowing-on-regenerate and exits 2, covered
        # separately by test_retiring_an_allowed_id_fails_generate_with_no_partial_write).
        hand_edited = self._path_ending("go/anthropic-specifications.json")
        hand_edited.write_text(hand_edited.read_text(encoding="utf-8") + " ", encoding="utf-8")
        drifted = self._run("check")
        self.assertEqual(drifted.returncode, 1)
        self.assertIn("drifted", drifted.stderr)

    def test_check_fails_when_a_patch_target_is_absent(self) -> None:
        self._write_roster({"claude-sonnet-5": _model()})
        self.assertEqual(self._run("generate").returncode, 0)
        self._path_ending("plan-schema.json").unlink()
        result = self._run("check")
        self.assertEqual(result.returncode, 1)
        self.assertIn("missing", result.stderr)

    def test_roster_change_with_no_regeneration_fails_check(self) -> None:
        self._write_roster({"claude-sonnet-5": _model(list_output=15.0)})
        self.assertEqual(self._run("generate").returncode, 0)
        self._write_roster({"claude-sonnet-5": _model(list_output=99.0)})
        result = self._run("check")
        self.assertEqual(result.returncode, 1)

    def test_retiring_an_allowed_id_fails_generate_with_no_partial_write(self) -> None:
        self._write_roster({"claude-sonnet-5": _model(selectable="legacy-pin-only")})
        self.assertEqual(self._run("generate").returncode, 0)
        before = self._all_target_texts()

        self._write_roster({"claude-sonnet-5": _model(selectable="retired")})
        result = self._run("generate")
        self.assertEqual(result.returncode, 2)
        self.assertIn("claude-sonnet-5", result.stderr)

        # A failure partway through rendering must not leave a mix of regenerated and stale
        # outputs: since rendering happens before any write, nothing on disk moved.
        self.assertEqual(self._all_target_texts(), before)


@unittest.skipUnless(
    os.environ.get("ROSTER_GEN_TEST_MARKETPLACE_ROOT"),
    "cross-repo byte-parity check needs a marketplace checkout beside this one; set "
    "ROSTER_GEN_TEST_MARKETPLACE_ROOT to run it (each repo's own CI checks out only itself)",
)
class RealRepoByteParityTests(unittest.TestCase):
    """Opportunistic integration check: with both repos checked out side by side (this
    generator's real execution context per its README), every committed output must already
    equal its roster-derived rendering at its own recorded tag."""

    def test_every_target_matches_its_own_recorded_tag(self) -> None:
        marketplace_root = Path(os.environ["ROSTER_GEN_TEST_MARKETPLACE_ROOT"]).resolve()
        roster = load_roster(_REPO_ROOT / "schemas" / "model-roster" / "model-roster.json")
        roots = {"ai-shared-lib": _REPO_ROOT, "marketplace": marketplace_root}
        for target in TARGETS:
            path = resolve(target, roots)
            have = path.read_text(encoding="utf-8")
            tag = _extract_tag(have)
            if target.kind == "whole":
                want = target.render_fn(roster, tag)
            elif target.kind == "allowlist":
                want = target.render_fn(roster, tag, existing_ids=None)
            else:
                want = target.render_fn(have, roster, tag)
            self.assertEqual(want, have, f"{path} has drifted from its own recorded tag {tag!r}")


_TAG_RE = re.compile(r"(?:at tag |roster-gen:generated \(tag=)(\S+?)(?:[ )]|\\u2014)")


def _extract_tag(text: str) -> str:
    match = _TAG_RE.search(text)
    if not match:
        raise ValueError(f"no generated-by tag marker found in: {text[:200]!r}")
    return match.group(1)


if __name__ == "__main__":
    unittest.main()
