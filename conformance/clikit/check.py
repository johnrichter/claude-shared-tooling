#!/usr/bin/env python3
"""clikit conformance - the cross-language byte-identity gate for the CLI output contract.

Drives the shared input set in `inputs/` through every implementation of the contract, then
compares what came out four ways: each language against the recorded goldens in `golden/`,
each language against the other, the recorded records against an independent canonical-JSON
reading of themselves, and every recorded record and exit code against the contract's own
source files in `schemas/clikit/`. Any difference fails the gate and pauses the build for a
human - there is no declared-delta list, no per-case waiver, and no path that rewrites a
golden to make a run pass.

Each implementation renders the suite from its own test, so `go test` and `cargo test`
already fail on drift on their own; this gate adds the two comparisons neither of them can
make alone - the one between the languages, and the one against the contract - and is the
required check.

Usage:
  python3 conformance/clikit/check.py             # run the gate
  python3 conformance/clikit/check.py --record    # rewrite the goldens (maintainer action)
  python3 conformance/clikit/check.py --selftest  # prove the gate catches a planted drift

Exit codes: 0 every implementation emitted identical bytes and identical exit codes; 1 a
divergence was found, a golden is absent, a golden is not canonical, or a golden disagrees
with the contract - pause and have a human judge it; 2 the harness itself could not run (a
toolchain absent, an implementation that would not build).
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

SUITE = Path(__file__).resolve().parent
REPO_ROOT = SUITE.parent.parent

CONTRACT_DIR = REPO_ROOT / "schemas" / "clikit"
RECORD_SCHEMA = CONTRACT_DIR / "result-record.schema.json"
CONTRACT = CONTRACT_DIR / "clikit.contract.json"

ARTIFACT_ENV = "CLIKIT_CONFORMANCE_OUT"
RESULTS = "results.jsonl"
EXIT_CODES = "exit-codes.txt"
CASES = "cases.txt"
GOLDEN_ARTIFACTS = (RESULTS, EXIT_CODES)
ALL_ARTIFACTS = (CASES, *GOLDEN_ARTIFACTS)

_OK = 0
_DIVERGENCE = 1
_HARNESS_ERROR = 2


@dataclass(frozen=True)
class Implementation:
    """One implementation of the contract and the command that renders the suite from it."""

    name: str
    tool: str
    command: tuple[str, ...]
    workdir: Path


IMPLEMENTATIONS = (
    Implementation(
        name="go",
        tool="go",
        command=("go", "test", "-count=1", "-run", "TestConformanceSuiteMatchesGolden", "."),
        workdir=REPO_ROOT / "go" / "clikit",
    ),
    Implementation(
        name="rust",
        tool="cargo",
        command=("cargo", "test", "-p", "clikit", "--test", "conformance"),
        workdir=REPO_ROOT / "rust",
    ),
)


@dataclass(frozen=True)
class Rendering:
    """What one implementation emitted: the artifact contents, plus how its test exited."""

    implementation: str
    artifacts: dict[str, str]
    exit_code: int
    output: str


@dataclass(frozen=True)
class ClassRule:
    """One outcome class as the record schema pins it, read from the schema's own branch.

    Attributes:
        exit_code: The integer this class pairs with.
        required: Fields the class requires beyond the always-required ones.
        forbidden: Fields the class forbids outright.
        governing_pattern: Pattern the governing error's code must match, where the class
            has one (`success` and `caveats` carry no governing error).
    """

    exit_code: int
    required: frozenset[str]
    forbidden: frozenset[str]
    governing_pattern: str | None


class HarnessError(RuntimeError):
    """The gate could not be run at all, as distinct from finding a divergence."""


def _read_json(path: Path) -> dict:
    """Parse one JSON file the gate depends on.

    Raises:
        HarnessError: If the file is absent or is not JSON.
    """
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HarnessError(f"{_display(path)}: {exc}") from exc


def case_paths() -> list[Path]:
    """Every case file in the shared input set, in the order the implementations read them.

    Raises:
        HarnessError: If the input directory is absent or holds no case file.
    """
    inputs = SUITE / "inputs"
    if not inputs.is_dir():
        raise HarnessError(f"{_display(inputs)}: the shared input set is missing")
    paths = sorted(inputs.glob("*.json"))
    if not paths:
        raise HarnessError(f"{_display(inputs)}: the shared input set holds no case file")
    return paths


def case_ids() -> list[str]:
    """Every case id, taken from the input file names."""
    return [path.stem for path in case_paths()]


def load_cases() -> list[dict]:
    """Every case in the shared input set, parsed, in file-name order."""
    return [_read_json(path) for path in case_paths()]


def render(implementation: Implementation, out_root: Path) -> Rendering:
    """Run one implementation's conformance test and collect what it emitted.

    Args:
        implementation: The implementation to run.
        out_root: Directory the implementations write their artifacts under.

    Returns:
        The artifacts it wrote, and its test's exit status.

    Raises:
        HarnessError: If the implementation's toolchain is absent, or it emitted no
            artifacts at all (a build failure rather than a divergence).
    """
    if shutil.which(implementation.tool) is None:
        raise HarnessError(f"{implementation.tool} is not on PATH, so {implementation.name} cannot be checked")

    env = dict(os.environ, **{ARTIFACT_ENV: str(out_root)})
    completed = subprocess.run(
        implementation.command,
        cwd=implementation.workdir,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    output = completed.stdout + completed.stderr

    directory = out_root / implementation.name
    artifacts: dict[str, str] = {}
    for name in ALL_ARTIFACTS:
        path = directory / name
        if path.is_file():
            artifacts[name] = path.read_text(encoding="utf-8")
    if not artifacts:
        raise HarnessError(
            f"{implementation.name} emitted no artifacts - it did not reach the rendering step:\n{output}"
        )
    return Rendering(implementation.name, artifacts, completed.returncode, output)


def compare_languages(renderings: list[Rendering]) -> list[str]:
    """Compare every implementation against the first one, artifact by artifact.

    Args:
        renderings: What each implementation emitted, in run order.

    Returns:
        One finding per difference found.
    """
    findings: list[str] = []
    reference, *others = renderings
    for other in others:
        for name in ALL_ARTIFACTS:
            findings.extend(
                f"{reference.implementation} and {other.implementation} disagree: {finding}"
                for finding in _diff(
                    name,
                    reference.artifacts.get(name),
                    other.artifacts.get(name),
                    reference.implementation,
                    other.implementation,
                )
            )
    return findings


def compare_goldens(renderings: list[Rendering]) -> list[str]:
    """Compare every implementation's rendering against the recorded goldens.

    Args:
        renderings: What each implementation emitted.

    Returns:
        One finding per difference found, including a golden that is absent.
    """
    findings: list[str] = []
    for name in GOLDEN_ARTIFACTS:
        path = SUITE / "golden" / name
        if not path.is_file():
            findings.append(
                f"golden {_display(path)} is absent - record it deliberately; no run creates one"
            )
            continue
        golden = path.read_text(encoding="utf-8")
        for rendering in renderings:
            findings.extend(
                f"{rendering.implementation} diverges from the golden: {finding}"
                for finding in _diff(
                    name, rendering.artifacts.get(name), golden, rendering.implementation, "golden"
                )
            )
    return findings


def check_records_are_canonical(results: str) -> list[str]:
    """Read every record back with an independent canonicalizer.

    Sorted keys and minimal separators are re-derived here rather than trusted, so a record
    that is not canonical JSON fails even when both implementations happen to agree on it.

    Args:
        results: The `results.jsonl` body to read.

    Returns:
        One finding per record whose bytes are not the canonical form of its own value.
    """
    findings: list[str] = []
    for number, line, case in _record_lines(results):
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            findings.append(f"{RESULTS} line {number} ({case}) is not JSON: {exc}")
            continue
        canonical = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        if canonical != line:
            findings.append(
                f"{RESULTS} line {number} ({case}) is not canonical:\n got {line}\nwant {canonical}"
            )
    return findings


def check_against_contract(results: str, exit_codes: str) -> list[str]:
    """Judge every record and exit code against the contract's own source files.

    The rules are read from `result-record.schema.json` and `clikit.contract.json` rather
    than restated here, so this is an independent oracle: a drift both implementations share
    fails here even though they agree with each other. It is also the only check that can
    catch a wrong status/exit-code pairing, which no cross-language comparison can see.

    Args:
        results: The `results.jsonl` body to judge.
        exit_codes: The `exit-codes.txt` body to judge against the same records.

    Returns:
        One finding per rule broken.

    Raises:
        HarnessError: If a contract source file is absent or unreadable.
    """
    schema = _read_json(RECORD_SCHEMA)
    rules = _class_rules(schema)
    root_fields = set(schema["properties"])
    always_required = set(schema["required"])
    diagnostic = schema["$defs"]["error"]
    diagnostic_fields = set(diagnostic["properties"])
    diagnostic_required = set(diagnostic["required"])
    triage_fields = set(schema["$defs"]["triage"]["properties"])
    triage_kinds = set(schema["$defs"]["triage_kind"]["enum"])

    findings = _pairing_agrees_across_contract_files(rules)
    declared_exit_codes = _parse_exit_codes(exit_codes)

    for number, line, case in _record_lines(results):
        where = f"{RESULTS} line {number} ({case})"
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            continue  # already reported by the canonical check

        unknown = sorted(set(record) - root_fields)
        if unknown:
            findings.append(f"{where}: unknown root field(s) {unknown} - the record root is closed")
        missing = sorted(always_required - set(record))
        if missing:
            findings.append(f"{where}: required field(s) {missing} absent")

        status = record.get("status")
        rule = rules.get(status)
        if rule is None:
            findings.append(f"{where}: status {status!r} is not one of the eleven classes")
            continue
        findings.extend(f"{where}: {finding}" for finding in _judge_class(record, rule))
        findings.extend(
            f"{where}: {finding}"
            for finding in _judge_diagnostics(
                record, diagnostic_fields, diagnostic_required, triage_fields, triage_kinds
            )
        )

        declared = declared_exit_codes.get(case)
        if declared is None:
            findings.append(f"{EXIT_CODES}: case {case} carries no exit code")
        elif declared != (status, record.get("exit_code")):
            findings.append(
                f"{EXIT_CODES}: case {case} declares {declared}, the record declares "
                f"{(status, record.get('exit_code'))}"
            )

    extra = sorted(set(declared_exit_codes) - set(case_ids()))
    if extra:
        findings.append(f"{EXIT_CODES}: exit code(s) for unknown case(s) {extra}")
    return findings


def _class_rules(schema: dict) -> dict[str, ClassRule]:
    """Read one rule per class out of the record schema's own status branches.

    Raises:
        HarnessError: If the schema carries no per-status branch to read.
    """
    rules: dict[str, ClassRule] = {}
    for branch in schema.get("allOf", []):
        status = branch.get("if", {}).get("properties", {}).get("status", {}).get("const")
        then = branch.get("then", {})
        properties = then.get("properties", {})
        if status is None or "exit_code" not in properties:
            continue
        # A class that forbids `errors` carries False here rather than a subschema, so it
        # has no governing code to pattern-match.
        errors = properties.get("errors")
        governing = (
            errors.get("prefixItems", [{}])[0].get("properties", {}).get("code", {}).get("pattern")
            if isinstance(errors, dict)
            else None
        )
        rules[status] = ClassRule(
            exit_code=properties["exit_code"]["const"],
            required=frozenset(then.get("required", [])),
            forbidden=frozenset(name for name, value in properties.items() if value is False),
            governing_pattern=governing,
        )
    if len(rules) != len(schema["$defs"]["status"]["enum"]):
        raise HarnessError(
            f"{_display(RECORD_SCHEMA)}: read {len(rules)} class branch(es) for "
            f"{len(schema['$defs']['status']['enum'])} status(es) - the schema's shape has changed"
        )
    return rules


def _pairing_agrees_across_contract_files(rules: dict[str, ClassRule]) -> list[str]:
    """The record schema and the contract table must pin the same status/exit-code pairing.

    A disagreement is a defect in whichever file restates rather than defines, and every
    golden below is judged against both, so it is reported before the records are.
    """
    table = {
        entry["status"]: entry["code"]
        for entry in _read_json(CONTRACT)["exit_taxonomy"]["classes"]
    }
    findings = []
    for status, rule in sorted(rules.items()):
        if table.get(status) != rule.exit_code:
            findings.append(
                f"{_display(CONTRACT)} pairs {status} with {table.get(status)}, "
                f"{_display(RECORD_SCHEMA)} pairs it with {rule.exit_code}"
            )
    for status in sorted(set(table) - set(rules)):
        findings.append(f"{_display(CONTRACT)} declares class {status}, the record schema does not")
    return findings


def _judge_class(record: dict, rule: ClassRule) -> list[str]:
    """Check one record against its own class's branch: pairing, presence, governing code."""
    findings = []
    if record.get("exit_code") != rule.exit_code:
        findings.append(f"exit_code {record.get('exit_code')} does not pair with this status (want {rule.exit_code})")
    for name in sorted(rule.required - set(record)):
        findings.append(f"this class requires {name}")
    for name in sorted(rule.forbidden & set(record)):
        findings.append(f"this class forbids {name}")
    if rule.governing_pattern:
        errors = record.get("errors") or [{}]
        governing = errors[0].get("code", "") if isinstance(errors[0], dict) else ""
        if not re.search(rule.governing_pattern, governing):
            findings.append(f"governing error code {governing!r} is not in this record's own class")
    return findings


def _judge_diagnostics(
    record: dict,
    fields: set[str],
    required: set[str],
    triage_fields: set[str],
    triage_kinds: set[str],
) -> list[str]:
    """Check every errors and caveats member against the schema's diagnostic shape."""
    findings = []
    for array in ("errors", "caveats"):
        for index, member in enumerate(record.get(array, [])):
            where = f"{array}[{index}]"
            if not isinstance(member, dict):
                findings.append(f"{where} is not an object")
                continue
            for name in sorted(required - set(member)):
                findings.append(f"{where} carries no {name}")
            unknown = sorted(set(member) - fields)
            if unknown:
                findings.append(f"{where} carries unknown field(s) {unknown}")
            triage = member.get("triage")
            if not isinstance(triage, dict):
                findings.append(f"{where}.triage is not an object")
                continue
            if triage.get("kind") not in triage_kinds:
                findings.append(f"{where}.triage kind {triage.get('kind')!r} is not one of the three")
            unknown = sorted(set(triage) - triage_fields)
            if unknown:
                findings.append(f"{where}.triage carries unknown field(s) {unknown}")
    return findings


def check_shape_coverage() -> list[str]:
    """Every outcome class and every diagnostic and triage shape is covered by a case.

    The class set and the triage-kind set are read from the contract, so a class or kind
    added there fails this gate until a golden covers it rather than going unexercised.

    Returns:
        One finding per shape no case in the input set reaches.
    """
    cases = load_cases()
    diagnostics = [
        diagnostic for case in cases for array in ("errors", "caveats") for diagnostic in case.get(array, [])
    ]
    triages = [diagnostic.get("triage", {}) for diagnostic in diagnostics]
    statuses = [entry["status"] for entry in _read_json(CONTRACT)["exit_taxonomy"]["classes"]]
    kinds = _read_json(RECORD_SCHEMA)["$defs"]["triage_kind"]["enum"]

    def directive(kind: str, with_instruction: bool) -> bool:
        """Whether some directive of `kind` does or does not carry an instruction."""
        return any(
            t.get("kind") == kind and ("instruction" in t) == with_instruction for t in triages
        )

    covered = {
        f"a case at status {status}": any(case.get("status") == status for case in cases)
        for status in sorted(statuses)
    }
    covered |= {
        f"a {kind} directive": any(t.get("kind") == kind for t in triages) for kind in sorted(kinds)
    }
    covered |= {
        "a reinvoke carrying after_seconds": any("after_seconds" in t for t in triages),
        "a reinvoke carrying an instruction": directive("reinvoke", True),
        "a reinvoke carrying no instruction": directive("reinvoke", False),
        "a run_tool carrying an instruction": directive("run_tool", True),
        "a run_tool carrying no instruction": directive("run_tool", False),
        "a diagnostic carrying a context": any("context" in d for d in diagnostics),
        "a diagnostic carrying no context": any("context" not in d for d in diagnostics),
        "a record carrying errors alone": any(
            case.get("errors") and not case.get("caveats") for case in cases
        ),
        "a record carrying caveats alone": any(
            case.get("caveats") and not case.get("errors") for case in cases
        ),
        "a record carrying errors and caveats": any(
            case.get("errors") and case.get("caveats") for case in cases
        ),
        "a record carrying data": any("data" in case for case in cases),
        "a record carrying no data": any("data" not in case for case in cases),
    }
    covered |= {
        f"a diagnostic code of {segments} segments": any(
            d.get("code", "").count(".") == segments - 1 for d in diagnostics
        )
        for segments in (2, 3, 4)
    }
    return [f"no case covers {what}" for what, reached in sorted(covered.items()) if not reached]


def check_suite_invariants() -> list[str]:
    """The input set's own rules, which neither implementation can check for the other.

    Returns:
        One finding per case that breaks one.
    """
    findings = []
    for path in case_paths():
        case = _read_json(path)
        if case.get("id") != path.stem:
            findings.append(f"{_display(path)} declares id {case.get('id')!r}, want {path.stem!r}")
        if not case.get("purpose"):
            findings.append(f"{_display(path)} declares no purpose - say what byte this case pins")
        for array in ("errors", "caveats"):
            for index, diagnostic in enumerate(case.get(array, [])):
                if diagnostic.get("triage", {}).get("after_seconds") == 0:
                    findings.append(
                        f"{_display(path)} {array}[{index}] declares after_seconds 0, which the "
                        "contract does not distinguish from absent - see this suite's README"
                    )
    return findings


def check_case_coverage(renderings: list[Rendering]) -> list[str]:
    """Every implementation rendered every case in the shared input set.

    Args:
        renderings: What each implementation emitted.

    Returns:
        One finding per implementation whose case list is not the input set.
    """
    expected = case_ids()
    findings: list[str] = []
    for rendering in renderings:
        rendered = rendering.artifacts.get(CASES, "").splitlines()
        if rendered == expected:
            continue
        missing = sorted(set(expected) - set(rendered))
        extra = sorted(set(rendered) - set(expected))
        detail = f"unrendered={missing} unknown={extra}" if missing or extra else f"order={rendered}"
        findings.append(
            f"{rendering.implementation} did not render the input set's "
            f"{len(expected)} case(s) in order: {detail}"
        )
    return findings


def _record_lines(results: str) -> list[tuple[int, str, str]]:
    """Every record line as (line number, line, case id), with the id it belongs to."""
    ids = case_ids()
    return [
        (number, line, ids[number - 1] if number <= len(ids) else "<no case>")
        for number, line in enumerate(results.splitlines(), start=1)
    ]


def _parse_exit_codes(exit_codes: str) -> dict[str, tuple[str, int]]:
    """Read `exit-codes.txt` into {case id: (status, exit code)}."""
    declared: dict[str, tuple[str, int]] = {}
    for line in exit_codes.splitlines():
        parts = line.split()
        if len(parts) == 3 and parts[2].isdigit():
            declared[parts[0]] = (parts[1], int(parts[2]))
    return declared


def _diff(name: str, got: str | None, want: str | None, got_label: str, want_label: str) -> list[str]:
    """First differing line between two artifact bodies, named by case where there is one."""
    if got is None:
        return [f"{name} was not emitted by {got_label}"]
    if want is None:
        return [f"{name} was not emitted by {want_label}"]
    if got == want:
        return []
    got_lines, want_lines = got.split("\n"), want.split("\n")
    ids = case_ids()
    for index in range(max(len(got_lines), len(want_lines))):
        got_line = got_lines[index] if index < len(got_lines) else "<no line>"
        want_line = want_lines[index] if index < len(want_lines) else "<no line>"
        if got_line == want_line:
            continue
        case = f" ({ids[index]})" if name == RESULTS and index < len(ids) else ""
        return [
            f"{name} line {index + 1}{case}:\n  {got_label:<7} {got_line}\n  {want_label:<7} {want_line}"
        ]
    return [f"{name} differs from {want_label} only in trailing bytes"]


def run_gate(quiet: bool = False) -> int:
    """Render the suite from every implementation and report every difference.

    Args:
        quiet: Suppress the per-implementation progress lines (used by the selftest).

    Returns:
        The process exit code.
    """
    with tempfile.TemporaryDirectory(prefix="clikit-conformance-") as scratch:
        out_root = Path(scratch)
        try:
            renderings = [render(implementation, out_root) for implementation in IMPLEMENTATIONS]
        except HarnessError as exc:
            print(f"clikit-conformance: {exc}", file=sys.stderr)
            return _HARNESS_ERROR

        if not quiet:
            for rendering in renderings:
                status = "ok" if rendering.exit_code == 0 else f"exit {rendering.exit_code}"
                print(
                    f"clikit-conformance: {rendering.implementation} rendered the suite ({status})",
                    file=sys.stderr,
                )

        findings = (
            check_suite_invariants()
            + check_shape_coverage()
            + check_case_coverage(renderings)
            + compare_languages(renderings)
            + compare_goldens(renderings)
            + _judge_goldens()
        )

    if findings:
        for finding in findings:
            print(f"clikit-conformance: {finding}", file=sys.stderr)
        print(
            f"clikit-conformance: {len(findings)} finding(s) - PAUSE: the implementations of one "
            "CLI output contract are not proven byte-identical, and a human must judge every line above",
            file=sys.stderr,
        )
        return _DIVERGENCE

    failed = [rendering for rendering in renderings if rendering.exit_code != 0]
    if failed:
        for rendering in failed:
            print(f"clikit-conformance: {rendering.implementation} test failed:\n{rendering.output}", file=sys.stderr)
        return _HARNESS_ERROR

    print(
        f"clikit-conformance: {len(case_ids())} case(s), {len(IMPLEMENTATIONS)} implementations, "
        "byte-identical records and identical exit codes",
        file=sys.stderr,
    )
    return _OK


def _judge_goldens() -> list[str]:
    """Run the canonical and contract checks over the recorded goldens.

    An absent golden is reported by `compare_goldens` and nothing here would add to it, so
    a partial corpus is judged once rather than twice.
    """
    results_path, exit_codes_path = SUITE / "golden" / RESULTS, SUITE / "golden" / EXIT_CODES
    if not (results_path.is_file() and exit_codes_path.is_file()):
        return []
    results = results_path.read_text(encoding="utf-8")
    exit_codes = exit_codes_path.read_text(encoding="utf-8")
    return check_records_are_canonical(results) + check_against_contract(results, exit_codes)


def record_goldens() -> int:
    """Rewrite the goldens from the implementations' current output.

    Only ever writes when every implementation already agrees byte for byte and the agreed
    bytes satisfy the contract, so a recorded golden is a cross-language agreement the
    contract also permits - never one language's opinion, and never a shared drift.

    Returns:
        The process exit code.
    """
    with tempfile.TemporaryDirectory(prefix="clikit-conformance-") as scratch:
        out_root = Path(scratch)
        try:
            renderings = [render(implementation, out_root) for implementation in IMPLEMENTATIONS]
        except HarnessError as exc:
            print(f"clikit-conformance: {exc}", file=sys.stderr)
            return _HARNESS_ERROR

        agreed = renderings[0].artifacts
        findings = (
            check_suite_invariants()
            + check_shape_coverage()
            + check_case_coverage(renderings)
            + compare_languages(renderings)
            + check_records_are_canonical(agreed.get(RESULTS, ""))
            + check_against_contract(agreed.get(RESULTS, ""), agreed.get(EXIT_CODES, ""))
        )
        if findings:
            for finding in findings:
                print(f"clikit-conformance: {finding}", file=sys.stderr)
            print(
                "clikit-conformance: refusing to record - the rendering is not an agreement the "
                "contract permits, so there are no bytes to record",
                file=sys.stderr,
            )
            return _DIVERGENCE

        golden_dir = SUITE / "golden"
        golden_dir.mkdir(exist_ok=True)
        for name in GOLDEN_ARTIFACTS:
            (golden_dir / name).write_text(agreed[name], encoding="utf-8")

    print(f"clikit-conformance: recorded {len(GOLDEN_ARTIFACTS)} golden(s) from an agreed rendering", file=sys.stderr)
    return _OK


@dataclass(frozen=True)
class Plant:
    """One deliberate break the gate must catch, as a single-token source edit."""

    name: str
    path: Path
    before: str
    after: str


PLANTS = (
    Plant(
        name="go pairs a class with an exit code of its own",
        path=REPO_ROOT / "go" / "clikit" / "status.go",
        before="{80, logkit.LevelError}",
        after="{81, logkit.LevelError}",
    ),
    Plant(
        name="rust stamps a record version of its own",
        path=REPO_ROOT / "rust" / "clikit" / "src" / "record.rs",
        before="pub const SCHEMA_VERSION: u32 = 1;",
        after="pub const SCHEMA_VERSION: u32 = 2;",
    ),
)


def run_selftest() -> int:
    """Prove the gate passes clean, catches a per-language drift, and refuses a missing golden.

    Every plant is applied to the working tree and reverted from the bytes read before it was
    applied; the revert is verified, so a selftest that cannot restore the tree says so
    instead of leaving a patched checkout behind.

    Returns:
        The process exit code.
    """
    print("clikit-conformance selftest: clean tree must pass", file=sys.stderr)
    if run_gate(quiet=True) != _OK:
        print("clikit-conformance selftest: the clean tree does not pass, nothing else is meaningful", file=sys.stderr)
        return _HARNESS_ERROR

    for plant in PLANTS:
        print(f"clikit-conformance selftest: plant - {plant.name}", file=sys.stderr)
        try:
            outcome = _with_plant(plant, lambda: run_gate(quiet=True))
        except HarnessError as exc:
            print(f"clikit-conformance selftest: {exc}", file=sys.stderr)
            return _HARNESS_ERROR
        if outcome != _DIVERGENCE:
            print(
                f"clikit-conformance selftest: the gate returned {outcome} for a planted drift, want {_DIVERGENCE}",
                file=sys.stderr,
            )
            return _DIVERGENCE

    for name in GOLDEN_ARTIFACTS:
        print(f"clikit-conformance selftest: plant - golden {name} is absent", file=sys.stderr)
        outcome = _with_golden_removed(name, lambda: run_gate(quiet=True))
        if outcome != _DIVERGENCE:
            print(
                f"clikit-conformance selftest: the gate returned {outcome} for an absent golden, want {_DIVERGENCE}",
                file=sys.stderr,
            )
            return _DIVERGENCE
        if not (SUITE / "golden" / name).is_file():
            print("clikit-conformance selftest: the gate did not restore the golden it moved aside", file=sys.stderr)
            return _HARNESS_ERROR

    print(
        "clikit-conformance selftest: plant - a record whose exit code does not pair with its status",
        file=sys.stderr,
    )
    if not _oracle_rejects_a_mispaired_record():
        print(
            "clikit-conformance selftest: the contract oracle accepted a mispaired record",
            file=sys.stderr,
        )
        return _DIVERGENCE

    print(
        f"clikit-conformance selftest: {len(PLANTS) + len(GOLDEN_ARTIFACTS) + 1} plant(s) caught, tree restored",
        file=sys.stderr,
    )
    return _OK


def _oracle_rejects_a_mispaired_record() -> bool:
    """The contract oracle must reject an exit code that does not pair with its status.

    No source plant reaches this check - a single-language drift is caught by comparison
    long before it - so it is exercised directly, on the recorded goldens with one integer
    changed in memory and nothing written.

    Raises:
        HarnessError: If the goldens carry no record for this plant to mispair.
    """
    results = (SUITE / "golden" / RESULTS).read_text(encoding="utf-8")
    exit_codes = (SUITE / "golden" / EXIT_CODES).read_text(encoding="utf-8")
    mispaired = results.replace('"exit_code":90', '"exit_code":91', 1)
    if mispaired == results:
        raise HarnessError(f"golden {RESULTS} carries no class-90 record for this plant to mispair")
    return bool(check_against_contract(mispaired, exit_codes))


def _with_plant(plant: Plant, action) -> int:
    """Apply one plant, run `action`, and restore the file from the bytes read beforehand."""
    original = plant.path.read_text(encoding="utf-8")
    if plant.before not in original:
        raise HarnessError(f"{_display(plant.path)} no longer carries the text this plant patches")
    try:
        plant.path.write_text(original.replace(plant.before, plant.after, 1), encoding="utf-8")
        return action()
    finally:
        plant.path.write_text(original, encoding="utf-8")
        if plant.path.read_text(encoding="utf-8") != original:
            print(f"clikit-conformance: FAILED TO RESTORE {_display(plant.path)}", file=sys.stderr)


def _with_golden_removed(name: str, action) -> int:
    """Move one golden aside, run `action`, and move it back."""
    path = SUITE / "golden" / name
    with tempfile.TemporaryDirectory(prefix="clikit-conformance-golden-") as scratch:
        stashed = Path(scratch) / name
        shutil.move(path, stashed)
        try:
            return action()
        finally:
            shutil.move(stashed, path)


def _display(path: Path) -> str:
    """Path relative to the repository root, for a message a reader can act on."""
    try:
        return str(path.relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


def main(argv: list[str] | None = None) -> int:
    """Parse arguments and run the gate, the recorder or the selftest.

    Args:
        argv: Command-line arguments (default: `sys.argv[1:]`).

    Returns:
        The process exit code.
    """
    parser = argparse.ArgumentParser(
        prog="clikit-conformance",
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--record", action="store_true", help="rewrite the goldens from an agreed rendering")
    mode.add_argument("--selftest", action="store_true", help="prove the gate catches a planted drift")
    args = parser.parse_args(argv)

    try:
        if args.record:
            return record_goldens()
        if args.selftest:
            return run_selftest()
        return run_gate()
    except HarnessError as exc:
        print(f"clikit-conformance: {exc}", file=sys.stderr)
        return _HARNESS_ERROR


if __name__ == "__main__":
    raise SystemExit(main())
