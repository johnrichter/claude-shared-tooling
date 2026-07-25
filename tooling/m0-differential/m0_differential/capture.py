"""Reading a binary's structural CLI surface by running it.

Three prose-free observations per subcommand:

  flags     — the flag package's own dump (name, type, default). Flag descriptions are dropped.
  synopsis  — the usage line's grammar up to the "->" marker: how many positional slots, which
              flags are required vs optional, which flags are alternatives of one another. Only
              tokens that resolve to a defined flag count, so descriptive text contributes nothing.
  probes    — exit code and stdout shape (JSON field paths mapped to their types) for each
              invocation in the corpus. stderr is never read and no string value is ever compared.
"""
from __future__ import annotations

import json
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from .probes import PROBES, UNKNOWN_FLAG, Probe

# A captured structural surface, and the per-subcommand records inside it. Both are written to
# disk verbatim, so they stay plain JSON-shaped data rather than a class hierarchy.
Surface = dict[str, Any]

SCHEMA = "build-helpers-cli-surface/structural-v1"

_FLAG_LINE = re.compile(r"^  -([A-Za-z0-9][A-Za-z0-9_.-]*)(?: (\S+))?$")
_DEFAULT = re.compile(r"\(default (.+)\)$")
_USAGE_LINE = re.compile(r"^  build-helpers (\S+)(.*)$")
_FLAG_TOKEN = re.compile(r"--([A-Za-z0-9][A-Za-z0-9-]*)")
_POSITIONAL = re.compile(r"<[^>]*>")

# A fixed, minimal environment: self-check and the accounting commands read the ambient session's
# model and effort, so an inherited CLAUDE_*/ANTHROPIC_* would make a capture depend on who ran it.
_BASE_ENV = {"TZ": "UTC", "LC_ALL": "C"}


def _run(binary: Path, argv: list[str], cwd: Path, stdin: str | None, env: dict[str, str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        [str(binary), *argv],
        cwd=cwd, input=stdin, capture_output=True, text=True,
        env={"PATH": "/usr/bin:/bin", "HOME": str(cwd), **_BASE_ENV, **env},
    )


def json_shape(value: object, prefix: str = "") -> dict[str, str]:
    """Flatten a decoded JSON document to field path -> type. Values are discarded; only names,
    types and presence survive, which is exactly what a consumer can depend on."""
    if isinstance(value, dict):
        out = {prefix: "object"}
        for key, item in value.items():
            out.update(json_shape(item, f"{prefix}.{key}" if prefix else key))
        return out
    if isinstance(value, list):
        out = {prefix or "": "array"}
        for item in value:
            for path, kind in json_shape(item, f"{prefix}[]").items():
                out[path] = kind if out.get(path, kind) == kind else "|".join(sorted({out[path], kind}))
        return out
    kinds = {bool: "boolean", int: "number", float: "number", str: "string", type(None): "null"}
    return {prefix or "": kinds[type(value)]}


def stdout_shape(text: str) -> Surface:
    """Classify stdout as empty, a JSON document (with its field shape), or opaque text. Text
    content is never compared — a Markdown render or a printed path is prose to this gate."""
    if not text.strip():
        return {"format": "empty"}
    try:
        decoded = json.loads(text)
    except json.JSONDecodeError:
        return {"format": "text"}
    return {"format": "json", "shape": json_shape(decoded)}


def flag_grammar(binary: Path, argv: list[str], cwd: Path) -> dict[str, dict[str, str]]:
    """The subcommand's defined flags, read out of the flag package's own usage dump. A subcommand
    that defines no flags never builds a FlagSet and so prints no dump — that is itself the
    structural fact, recorded as an empty set.

    argv must be a well-formed invocation of the subcommand: positionals are consumed before any
    FlagSet is built, so a bare `<cmd> --unknown` would die on the missing positional and never
    reach the parse that produces the dump.
    """
    result = _run(binary, [*argv, UNKNOWN_FLAG], cwd, None, {})
    lines = result.stderr.splitlines()
    for i, line in enumerate(lines):
        if line.startswith("Usage of "):
            lines = lines[i + 1:]
            break
    else:
        return {}

    flags: dict[str, dict[str, str]] = {}
    current = ""
    for line in lines:
        match = _FLAG_LINE.match(line)
        if match:
            current = match.group(1)
            flags[current] = {"type": match.group(2) or "bool", "default": ""}
            continue
        if current:
            default = _DEFAULT.search(line.strip())
            if default:
                flags[current]["default"] = default.group(1)
    return flags


def _usage_lines(binary: Path, cwd: Path) -> dict[str, str]:
    """Each subcommand's usage line, keyed by subcommand name."""
    result = _run(binary, ["--help"], cwd, None, {})
    out: dict[str, str] = {}
    for line in (result.stdout or result.stderr).splitlines():
        match = _USAGE_LINE.match(line)
        if not match:
            continue
        head, rest = match.group(1), match.group(2)
        if head == "feedback":
            sub, _, rest = rest.strip().partition(" ")
            head = f"feedback {sub}"
        out[head] = rest
    return out


def synopsis_grammar(usage_line: str, defined_flags: set[str]) -> Surface:
    """The argv grammar a usage line declares, as structure rather than text.

    Everything after the "->" marker is the human-readable description and is dropped. In what
    remains, `[...]` marks an optional group and `{a | b}` an alternation; positional slots are
    counted and their requiredness kept, but not their placeholder names, which are documentation.
    """
    synopsis = usage_line.split("->", 1)[0]

    positionals: list[str] = []
    required: list[str] = []
    optional: list[str] = []
    groups: list[list[list[str]]] = []

    depth_optional = 0
    depth_alt = 0
    alternatives: list[list[str]] = []
    token = ""

    def flush() -> None:
        nonlocal token
        for match in _FLAG_TOKEN.finditer(token):
            name = match.group(1)
            if name not in defined_flags:
                continue
            (optional if depth_optional else required).append(name)
            if depth_alt:
                alternatives[-1].append(name)
        for _ in _POSITIONAL.finditer(token):
            positionals.append("optional" if depth_optional else "required")
        token = ""

    for char in synopsis:
        if char == "[":
            flush()
            depth_optional += 1
        elif char == "]":
            flush()
            depth_optional = max(0, depth_optional - 1)
        elif char == "{":
            flush()
            depth_alt += 1
            alternatives = [[]]
        elif char == "}":
            flush()
            depth_alt = max(0, depth_alt - 1)
            populated = [sorted(alt) for alt in alternatives if alt]
            if len(populated) > 1:
                groups.append(populated)
            alternatives = []
        elif char == "|" and depth_alt:
            flush()
            alternatives.append([])
        else:
            token += char
    flush()

    return {
        "positionals": positionals,
        "required_flags": sorted(set(required)),
        "optional_flags": sorted(set(optional)),
        "alternations": sorted(groups),
    }


def _stage(fixtures: Path) -> Path:
    """A fresh copy of the fixture tree at a fixed path.

    The path is fixed, not a fresh temp directory, because a few subcommands key their output by
    absolute input path (record-usage's per-transcript ledger, for one). Two captures made at two
    different paths would then differ in their JSON field names for no reason anyone cares about.
    Fixing the path also makes a capture reproducible run to run, which is what lets the recorded
    surfaces be committed as evidence.
    """
    staged = Path(tempfile.gettempdir()) / "m0-differential-probe"
    shutil.rmtree(staged, ignore_errors=True)
    shutil.copytree(fixtures, staged)
    return staged


def run_probe(binary: Path, probe: Probe, fixtures: Path) -> Surface:
    staged = _stage(fixtures)
    try:
        result = _run(binary, probe.argv, staged, probe.stdin, probe.env)
        return {"exit": result.returncode, "stdout": stdout_shape(result.stdout)}
    finally:
        shutil.rmtree(staged, ignore_errors=True)


def capture(binary: Path, fixtures: Path) -> Surface:
    """The whole structural surface of one binary."""
    # The first probe listed for a subcommand is its well-formed invocation, which is what the
    # flag dump has to be derived from.
    canonical: dict[str, list[str]] = {}
    for probe in PROBES:
        canonical.setdefault(probe.command, probe.argv)

    scratch = _stage(fixtures)
    try:
        usage = _usage_lines(binary, scratch)
        commands: dict[str, Surface] = {}
        for name in sorted(canonical):
            flags = {} if name == "help" else flag_grammar(binary, canonical[name], scratch)
            commands[name] = {
                "flags": flags,
                "synopsis": synopsis_grammar(usage.get(name, ""), set(flags)),
                "documented": name in usage,
                "probes": {},
            }
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

    for probe in PROBES:
        commands[probe.command]["probes"][probe.id] = run_probe(binary, probe, fixtures)

    return {"schema": SCHEMA, "commands": commands}
