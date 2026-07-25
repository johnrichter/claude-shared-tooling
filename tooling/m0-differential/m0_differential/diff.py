"""Diffing two structural surfaces.

Every difference reported here is a difference a caller can observe programmatically: a flag that
appeared or vanished or changed type or default, a positional slot that came or went, a subcommand
that stopped being reachable, an exit code that changed, a stdout field that was added, removed,
renamed or retyped. Nothing else is compared, so no amount of rewording can produce a finding.
"""
from __future__ import annotations

from dataclasses import dataclass

from .capture import Surface


@dataclass(frozen=True)
class Divergence:
    kind: str
    command: str
    detail: str
    was: str
    now: str

    def line(self) -> str:
        return f"{self.command}: {self.kind}: {self.detail}: was {self.was}, now {self.now}"


def _cmp(out: list[Divergence], kind: str, command: str, detail: str, was: object, now: object) -> None:
    if was != now:
        out.append(Divergence(kind, command, detail, repr(was), repr(now)))


def _diff_flags(out: list[Divergence], command: str, was: Surface, now: Surface) -> None:
    for name in sorted(set(was) - set(now)):
        out.append(Divergence("flag_removed", command, f"--{name}", repr(was[name]), "absent"))
    for name in sorted(set(now) - set(was)):
        out.append(Divergence("flag_added", command, f"--{name}", "absent", repr(now[name])))
    for name in sorted(set(was) & set(now)):
        _cmp(out, "flag_type_changed", command, f"--{name}", was[name]["type"], now[name]["type"])
        _cmp(out, "flag_default_changed", command, f"--{name}", was[name]["default"], now[name]["default"])


def _diff_synopsis(out: list[Divergence], command: str, was: Surface, now: Surface) -> None:
    _cmp(out, "positionals_changed", command, "positional slots", was["positionals"], now["positionals"])
    _cmp(out, "flag_requiredness_changed", command, "required flags", was["required_flags"], now["required_flags"])
    _cmp(out, "flag_requiredness_changed", command, "optional flags", was["optional_flags"], now["optional_flags"])
    _cmp(out, "alternation_changed", command, "mutually exclusive flag groups", was["alternations"], now["alternations"])


def _diff_probe(out: list[Divergence], command: str, probe: str, was: Surface, now: Surface) -> None:
    _cmp(out, "exit_code_changed", command, probe, was["exit"], now["exit"])

    was_out, now_out = was["stdout"], now["stdout"]
    if was_out["format"] != now_out["format"]:
        out.append(Divergence("stdout_format_changed", command, probe, was_out["format"], now_out["format"]))
        return
    was_shape, now_shape = was_out.get("shape", {}), now_out.get("shape", {})
    for path in sorted(set(was_shape) - set(now_shape)):
        out.append(Divergence("stdout_field_removed", command, f"{probe}: {path or '<root>'}", was_shape[path], "absent"))
    for path in sorted(set(now_shape) - set(was_shape)):
        out.append(Divergence("stdout_field_added", command, f"{probe}: {path or '<root>'}", "absent", now_shape[path]))
    for path in sorted(set(was_shape) & set(now_shape)):
        _cmp(out, "stdout_field_retyped", command, f"{probe}: {path or '<root>'}", was_shape[path], now_shape[path])


def diff(was: Surface, now: Surface) -> list[Divergence]:
    out: list[Divergence] = []
    was_cmds, now_cmds = was["commands"], now["commands"]

    for name in sorted(set(was_cmds) - set(now_cmds)):
        out.append(Divergence("command_removed", name, "subcommand", "present", "absent"))
    for name in sorted(set(now_cmds) - set(was_cmds)):
        out.append(Divergence("command_added", name, "subcommand", "absent", "present"))

    for name in sorted(set(was_cmds) & set(now_cmds)):
        old, new = was_cmds[name], now_cmds[name]
        _cmp(out, "documentation_presence_changed", name, "listed in usage", old["documented"], new["documented"])
        _diff_flags(out, name, old["flags"], new["flags"])
        _diff_synopsis(out, name, old["synopsis"], new["synopsis"])
        for probe in sorted(set(old["probes"]) & set(new["probes"])):
            _diff_probe(out, name, probe, old["probes"][probe], new["probes"][probe])

    return out
