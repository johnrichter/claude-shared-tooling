#!/usr/bin/env python3
"""logkit conformance — the cross-language byte-identity gate for the logging standard.

Drives the shared input set in `inputs/` through every implementation of the standard, then
compares what came out three ways: each language against the recorded goldens in `golden/`,
each language against the other, and the goldens against an independent canonical-JSON
reading of themselves. Any difference fails the gate and pauses the build for a human -
there is no declared-delta list, no per-case waiver, and no path that rewrites a golden to
make a run pass.

Each implementation renders the suite from its own test, so `go test` and `cargo test`
already fail on drift on their own; this gate adds the comparison neither of them can make
alone - the one between the languages - and is the required check.

Usage:
  python3 conformance/logkit/check.py             # run the gate
  python3 conformance/logkit/check.py --record    # rewrite the goldens (maintainer action)
  python3 conformance/logkit/check.py --selftest  # prove the gate catches a planted drift

Exit codes: 0 every implementation emitted identical bytes; 1 a divergence was found, a
golden is absent, or a golden is not canonical - pause and have a human judge it; 2 the
harness itself could not run (a toolchain absent, an implementation that would not build).
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

SUITE = Path(__file__).resolve().parent
REPO_ROOT = SUITE.parent.parent

ARTIFACT_ENV = "LOGKIT_CONFORMANCE_OUT"
RECORDS = "records.jsonl"
HUMAN = "records.human.txt"
CASES = "cases.txt"
GOLDEN_ARTIFACTS = (RECORDS, HUMAN)
ALL_ARTIFACTS = (CASES, *GOLDEN_ARTIFACTS)

_OK = 0
_DIVERGENCE = 1
_HARNESS_ERROR = 2


@dataclass(frozen=True)
class Implementation:
    """One implementation of the standard and the command that renders the suite from it."""

    name: str
    tool: str
    command: tuple[str, ...]
    workdir: Path


IMPLEMENTATIONS = (
    Implementation(
        name="go",
        tool="go",
        command=("go", "test", "-count=1", "-run", "TestConformanceSuiteMatchesGolden", "."),
        workdir=REPO_ROOT / "go" / "logkit",
    ),
    Implementation(
        name="rust",
        tool="cargo",
        command=("cargo", "test", "-p", "logkit", "--test", "conformance"),
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


class HarnessError(RuntimeError):
    """The gate could not be run at all, as distinct from finding a divergence."""


def case_ids() -> list[str]:
    """Every case id in the shared input set, in the order the implementations read them.

    Returns:
        The case ids, taken from the input file names.

    Raises:
        HarnessError: If the input directory is absent or holds no case file.
    """
    inputs = SUITE / "inputs"
    if not inputs.is_dir():
        raise HarnessError(f"{_display(inputs)}: the shared input set is missing")
    ids = sorted(path.stem for path in inputs.glob("*.json"))
    if not ids:
        raise HarnessError(f"{_display(inputs)}: the shared input set holds no case file")
    return ids


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


def check_goldens_are_canonical() -> list[str]:
    """Read the recorded records back with an independent canonicalizer.

    Sorted keys and minimal separators are re-derived here rather than trusted, so a golden
    that is not canonical JSON fails even when both implementations happen to agree on it.

    Returns:
        One finding per record whose bytes are not the canonical form of its own value.
    """
    path = SUITE / "golden" / RECORDS
    if not path.is_file():
        return []
    findings: list[str] = []
    ids = case_ids()
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        case = ids[number - 1] if number <= len(ids) else "<no case>"
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            findings.append(f"golden {RECORDS} line {number} ({case}) is not JSON: {exc}")
            continue
        canonical = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        if canonical != line:
            findings.append(
                f"golden {RECORDS} line {number} ({case}) is not canonical:\n got {line}\nwant {canonical}"
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
        case = f" ({ids[index]})" if name == RECORDS and index < len(ids) else ""
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
    with tempfile.TemporaryDirectory(prefix="logkit-conformance-") as scratch:
        out_root = Path(scratch)
        try:
            renderings = [render(implementation, out_root) for implementation in IMPLEMENTATIONS]
        except HarnessError as exc:
            print(f"logkit-conformance: {exc}", file=sys.stderr)
            return _HARNESS_ERROR

        if not quiet:
            for rendering in renderings:
                status = "ok" if rendering.exit_code == 0 else f"exit {rendering.exit_code}"
                print(
                    f"logkit-conformance: {rendering.implementation} rendered the suite ({status})",
                    file=sys.stderr,
                )

        findings = (
            check_case_coverage(renderings)
            + compare_languages(renderings)
            + compare_goldens(renderings)
            + check_goldens_are_canonical()
        )

    if findings:
        for finding in findings:
            print(f"logkit-conformance: {finding}", file=sys.stderr)
        print(
            f"logkit-conformance: {len(findings)} finding(s) - PAUSE: the implementations of one "
            "record contract are not proven byte-identical, and a human must judge every line above",
            file=sys.stderr,
        )
        return _DIVERGENCE

    failed = [rendering for rendering in renderings if rendering.exit_code != 0]
    if failed:
        for rendering in failed:
            print(f"logkit-conformance: {rendering.implementation} test failed:\n{rendering.output}", file=sys.stderr)
        return _HARNESS_ERROR

    print(
        f"logkit-conformance: {len(case_ids())} case(s), "
        f"{len(IMPLEMENTATIONS)} implementations, byte-identical",
        file=sys.stderr,
    )
    return _OK


def record_goldens() -> int:
    """Rewrite the goldens from the implementations' current output.

    Only ever writes when every implementation already agrees byte for byte, so a recorded
    golden is a cross-language agreement rather than one language's opinion.

    Returns:
        The process exit code.
    """
    with tempfile.TemporaryDirectory(prefix="logkit-conformance-") as scratch:
        out_root = Path(scratch)
        try:
            renderings = [render(implementation, out_root) for implementation in IMPLEMENTATIONS]
        except HarnessError as exc:
            print(f"logkit-conformance: {exc}", file=sys.stderr)
            return _HARNESS_ERROR

        findings = check_case_coverage(renderings) + compare_languages(renderings)
        if findings:
            for finding in findings:
                print(f"logkit-conformance: {finding}", file=sys.stderr)
            print(
                "logkit-conformance: refusing to record - the implementations do not agree, "
                "so there are no bytes to record",
                file=sys.stderr,
            )
            return _DIVERGENCE

        golden_dir = SUITE / "golden"
        golden_dir.mkdir(exist_ok=True)
        for name in GOLDEN_ARTIFACTS:
            (golden_dir / name).write_text(renderings[0].artifacts[name], encoding="utf-8")

    print(f"logkit-conformance: recorded {len(GOLDEN_ARTIFACTS)} golden(s) from an agreed rendering", file=sys.stderr)
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
        name="go emits a level token of its own",
        path=REPO_ROOT / "go" / "logkit" / "level.go",
        before='LevelWarn  Level = "warn"',
        after='LevelWarn  Level = "warning"',
    ),
    Plant(
        name="rust emits a level token of its own",
        path=REPO_ROOT / "rust" / "logkit" / "src" / "level.rs",
        before='Level::Warn => "warn",',
        after='Level::Warn => "warning",',
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
    print("logkit-conformance selftest: clean tree must pass", file=sys.stderr)
    if run_gate(quiet=True) != _OK:
        print("logkit-conformance selftest: the clean tree does not pass, nothing else is meaningful", file=sys.stderr)
        return _HARNESS_ERROR

    for plant in PLANTS:
        print(f"logkit-conformance selftest: plant - {plant.name}", file=sys.stderr)
        try:
            outcome = _with_plant(plant, lambda: run_gate(quiet=True))
        except HarnessError as exc:
            print(f"logkit-conformance selftest: {exc}", file=sys.stderr)
            return _HARNESS_ERROR
        if outcome != _DIVERGENCE:
            print(
                f"logkit-conformance selftest: the gate returned {outcome} for a planted drift, want {_DIVERGENCE}",
                file=sys.stderr,
            )
            return _DIVERGENCE

    print("logkit-conformance selftest: plant - a golden is absent", file=sys.stderr)
    outcome = _with_golden_removed(RECORDS, lambda: run_gate(quiet=True))
    if outcome != _DIVERGENCE:
        print(
            f"logkit-conformance selftest: the gate returned {outcome} for an absent golden, want {_DIVERGENCE}",
            file=sys.stderr,
        )
        return _DIVERGENCE
    if not (SUITE / "golden" / RECORDS).is_file():
        print("logkit-conformance selftest: the gate did not restore the golden it moved aside", file=sys.stderr)
        return _HARNESS_ERROR

    print(f"logkit-conformance selftest: {len(PLANTS) + 1} plant(s) caught, tree restored", file=sys.stderr)
    return _OK


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
            print(f"logkit-conformance: FAILED TO RESTORE {_display(plant.path)}", file=sys.stderr)


def _with_golden_removed(name: str, action) -> int:
    """Move one golden aside, run `action`, and move it back."""
    path = SUITE / "golden" / name
    with tempfile.TemporaryDirectory(prefix="logkit-conformance-golden-") as scratch:
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
        prog="logkit-conformance",
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
        print(f"logkit-conformance: {exc}", file=sys.stderr)
        return _HARNESS_ERROR


if __name__ == "__main__":
    raise SystemExit(main())
