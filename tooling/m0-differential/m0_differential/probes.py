"""The probe corpus: the fixed set of invocations both binaries answer.

Every subcommand the pre-M0 baseline enumerates gets at least one probe, and the corpus is checked
against that enumeration before a run starts, so coverage can never quietly shrink. Each probe runs
in its own fresh copy of the fixture tree, because several subcommands rewrite their inputs.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

FIXTURES_SUBPATH = "go/build-helpers/testdata/post-m0-differential/fixtures"
BASELINE_SURFACE_SUBPATH = "go/build-helpers/testdata/pre-m0-baseline/surface.json"

# Any token that is not a defined flag of the subcommand under probe; makes Go's flag package dump
# the subcommand's whole flag set, which is where the argv grammar is read from.
UNKNOWN_FLAG = "--m0-differential-unknown-flag"

_AT = "2026-01-01T02:00:00Z"
_SESSION = "11111111-2222-3333-4444-555555555555"
_BAND = ["--floor-model", "claude-sonnet-4-5", "--floor-effort", "low",
         "--ceiling-model", "claude-sonnet-5", "--ceiling-effort", "high"]


@dataclass(frozen=True)
class Probe:
    """One invocation, run identically against both binaries."""

    id: str
    command: str
    argv: list[str]
    stdin: str | None = None
    env: dict[str, str] = field(default_factory=dict)


PROBES: list[Probe] = [
    Probe("render/ok", "render", ["render", "plan.json"]),
    Probe("render/flags", "render", ["render", "plan.json", "--slug", "probe", "--topic", "t", "--updated", _AT]),
    Probe("render/no-positional", "render", ["render"]),

    Probe("diff/ok", "diff", ["diff", "plan.json", "plan-next.json"]),
    Probe("diff/missing-positional", "diff", ["diff", "plan.json"]),

    Probe("check-tiers/ok", "check-tiers", ["check-tiers", "plan.json"]),
    Probe("check-tiers/unavailable-tier", "check-tiers", ["check-tiers", "plan-badtier.json"]),
    Probe("check-tiers/unreadable", "check-tiers", ["check-tiers", "absent.json"]),

    Probe("hash/ok", "hash", ["hash", "plan.json"]),
    Probe("hash/no-positional", "hash", ["hash"]),

    Probe("validate/ok", "validate", ["validate", "plan.json"]),
    Probe("validate/invalid", "validate", ["validate", "plan-invalid.json"]),
    Probe("validate/unreadable", "validate", ["validate", "absent.json"]),

    Probe("classify/ok", "classify", ["classify", "project-dir"]),
    Probe("classify/empty-dir", "classify", ["classify", "probe"]),
    Probe("classify/no-positional", "classify", ["classify"]),

    Probe("escalate/named-trigger", "escalate", ["escalate", "--condition", "failed-task-triage"]),
    Probe("escalate/out-of-set", "escalate", ["escalate", "--condition", "not-a-trigger"]),
    Probe("escalate/design-level", "escalate", ["escalate", "--condition", "failed-task-triage", "--touches-scope"]),
    Probe("escalate/no-condition", "escalate", ["escalate"]),

    Probe("classify-scope/in-scope", "classify-scope", ["classify-scope"]),
    Probe("classify-scope/design-level", "classify-scope", ["classify-scope", "--touches-success-criteria"]),

    Probe("init-exec/ok", "init-exec", ["init-exec", "plan.json", "--slug", "probe", "--at", _AT]),
    Probe("init-exec/full-flags", "init-exec",
          ["init-exec", "plan.json", "--slug", "probe", "--name", "n", "--topic", "t",
           "--design-updated", _AT, "--plan-updated", _AT, "--pause", "task",
           "--budget", "unlimited", "--rates", "list-price", "--at", _AT]),
    Probe("init-exec/no-slug", "init-exec", ["init-exec", "plan.json"]),

    Probe("render-exec/ok", "render-exec", ["render-exec", "execution.json", "plan.json"]),
    Probe("render-exec/missing-positional", "render-exec", ["render-exec", "execution.json"]),

    Probe("next/ready", "next", ["next", "execution.json", "plan.json"]),
    Probe("next/done", "next", ["next", "execution-done.json", "plan.json"]),
    Probe("next/missing-positional", "next", ["next", "execution.json"]),

    Probe("batch/ready", "batch", ["batch", "execution.json", "plan.json"]),
    Probe("batch/capped", "batch", ["batch", "execution.json", "plan.json", "--max", "1"]),
    Probe("batch/done", "batch", ["batch", "execution-done.json", "plan.json"]),
    Probe("batch/missing-positional", "batch", ["batch", "execution.json"]),

    Probe("verify-surface/ok", "verify-surface", ["verify-surface", "plan.json", "M1.P1.T1"]),
    Probe("verify-surface/union", "verify-surface", ["verify-surface", "plan.json", "M1.P1.T1,M1.P1.T2"]),
    Probe("verify-surface/violation", "verify-surface", ["verify-surface", "plan.json", "M1.P1.T1", "--root", "project-dir"]),
    Probe("verify-surface/no-taskid", "verify-surface", ["verify-surface", "plan.json"]),

    Probe("check-changed-surface/ok", "check-changed-surface",
          ["check-changed-surface", "plan.json", "M1.P1.T1", "--changed", "changed-clean.txt"]),
    Probe("check-changed-surface/off-surface", "check-changed-surface",
          ["check-changed-surface", "plan.json", "M1.P1.T1", "--changed", "changed-off-surface.txt"]),
    Probe("check-changed-surface/stdin", "check-changed-surface",
          ["check-changed-surface", "plan.json", "M1.P1.T1", "--changed", "-"], stdin="probe/kept.txt\n"),
    Probe("check-changed-surface/stdin-off-surface", "check-changed-surface",
          ["check-changed-surface", "plan.json", "M1.P1.T1", "--changed", "-"],
          stdin="probe/kept.txt\nprobe/off-surface.txt\n"),
    Probe("check-changed-surface/no-changed", "check-changed-surface",
          ["check-changed-surface", "plan.json", "M1.P1.T1"]),

    Probe("record/in-progress", "record", ["record", "execution.json", "M1.P1.T1", "--status", "in-progress", "--at", _AT]),
    Probe("record/done-with-commit", "record",
          ["record", "execution.json", "M1.P1.T1", "--status", "done", "--commit", "abc1234",
           "--cost", "0.5", "--tokens-out", "10", "--input-tokens", "20", "--cache-write-tokens", "5",
           "--cache-read-tokens", "7", "--usage-turns", "2", "--note", "n", "--run-id", "r", "--at", _AT]),
    # M0.P8.T2's writer-side fail-fast: a done write carrying no commit SHA.
    Probe("record/done-without-commit", "record",
          ["record", "execution.json", "M1.P1.T1", "--status", "done", "--at", _AT]),
    Probe("record/unknown-task", "record", ["record", "execution.json", "M9.P9.T9", "--status", "done", "--commit", "abc1234"]),
    Probe("record/out-of-set-status", "record", ["record", "execution.json", "M1.P1.T1", "--status", "not-a-status"]),

    Probe("log-note/ok", "log-note", ["log-note", "execution.json", "--note", "n", "--at", _AT]),
    Probe("log-note/no-note", "log-note", ["log-note", "execution.json"]),

    Probe("reconcile-exec/ok", "reconcile-exec",
          ["reconcile-exec", "execution.json", "plan.json", "plan-next.json", "--at", _AT]),
    Probe("reconcile-exec/missing-positional", "reconcile-exec", ["reconcile-exec", "execution.json", "plan.json"]),

    Probe("archive/ok", "archive",
          ["archive", "plan.json", "execution-done.json", "archive.json", "--milestone", "M1", "--at", _AT]),
    Probe("archive/refuses-unfinished", "archive",
          ["archive", "plan.json", "execution.json", "archive.json", "--milestone", "M1", "--at", _AT]),
    Probe("archive/no-milestone", "archive", ["archive", "plan.json", "execution-done.json", "archive.json"]),

    Probe("migrate-project/dry-run", "migrate-project", ["migrate-project", "plan.json", "execution.json", "--dry-run"]),
    Probe("migrate-project/write", "migrate-project", ["migrate-project", "plan.json", "execution.json"]),
    Probe("migrate-project/missing-positional", "migrate-project", ["migrate-project", "plan.json"]),

    Probe("usage/ok", "usage", ["usage", "transcript.jsonl"]),
    Probe("usage/unreadable", "usage", ["usage", "absent.jsonl"]),

    Probe("record-usage/ok", "record-usage", ["record-usage", "execution.json", "--transcript", "transcript.jsonl", "--at", _AT]),
    Probe("record-usage/final", "record-usage",
          ["record-usage", "execution.json", "--transcript", "transcript.jsonl", "--final", "--at", _AT]),
    Probe("record-usage/no-transcript", "record-usage", ["record-usage", "execution.json"]),

    Probe("attribute/ok", "attribute", ["attribute", "execution.json", "--transcript", "transcript.jsonl", "--at", _AT]),
    Probe("attribute/scoped", "attribute",
          ["attribute", "execution.json", "--transcript", "transcript.jsonl", "--tasks", "M1.P1.T1", "--at", _AT]),
    Probe("attribute/no-transcript", "attribute", ["attribute", "execution.json"]),

    Probe("retrieve/outline", "retrieve", ["retrieve", "plan.json", "--level", "outline"]),
    Probe("retrieve/milestone", "retrieve", ["retrieve", "plan.json", "--level", "milestone", "--id", "M1"]),
    Probe("retrieve/task", "retrieve", ["retrieve", "plan.json", "--level", "task", "--id", "M1.P1.T1"]),
    Probe("retrieve/field", "retrieve", ["retrieve", "plan.json", "--level", "field", "--id", "M1.P1.T1", "--field", "summary"]),
    Probe("retrieve/bad-level", "retrieve", ["retrieve", "plan.json", "--level", "not-a-level"]),

    Probe("self-check/in-band", "self-check",
          ["self-check", *_BAND, "--transcript", "transcript.jsonl", "--settings", "settings.json"]),
    Probe("self-check/below-floor", "self-check",
          ["self-check", "--floor-model", "claude-sonnet-5", "--floor-effort", "xhigh",
           "--ceiling-model", "claude-sonnet-5", "--ceiling-effort", "xhigh",
           "--transcript", "transcript.jsonl", "--settings", "settings.json"]),
    Probe("self-check/cross-family-band", "self-check",
          ["self-check", "--floor-model", "claude-opus-4-5", "--floor-effort", "low",
           "--ceiling-model", "claude-opus-5", "--ceiling-effort", "high",
           "--transcript", "transcript.jsonl", "--settings", "settings.json"]),
    Probe("self-check/identity-guard", "self-check",
          ["self-check", *_BAND, "--transcript", "transcript.jsonl", "--settings", "settings.json",
           "--session-id", _SESSION]),
    Probe("self-check/identity-mismatch", "self-check",
          ["self-check", *_BAND, "--transcript", "transcript.jsonl", "--settings", "settings.json",
           "--session-id", "99999999-8888-7777-6666-555555555555"]),
    Probe("self-check/no-band", "self-check", ["self-check", "--transcript", "transcript.jsonl"]),

    Probe("resolve-transcript/ok", "resolve-transcript",
          ["resolve-transcript", "--session-id", _SESSION, "--cwd", "/probe/cwd", "--projects-dir", "projects"]),
    Probe("resolve-transcript/absent", "resolve-transcript",
          ["resolve-transcript", "--session-id", "99999999-8888-7777-6666-555555555555",
           "--cwd", "/probe/cwd", "--projects-dir", "projects"]),
    Probe("resolve-transcript/no-id", "resolve-transcript", ["resolve-transcript"]),

    Probe("feedback add/ok", "feedback add",
          ["feedback", "add", "feedback.json", "--title", "t", "--feedback", "f",
           "--impact", "3", "--urgency", "3", "--source-task", "M1.P1.T1",
           "--proposed-solution", "p", "--why-it-matters", "w", "--at", _AT]),
    Probe("feedback add/no-title", "feedback add",
          ["feedback", "add", "feedback.json", "--feedback", "f", "--impact", "3", "--urgency", "3"]),

    Probe("feedback list/ok", "feedback list", ["feedback", "list", "feedback.json"]),
    Probe("feedback list/filtered", "feedback list",
          ["feedback", "list", "feedback.json", "--by-task", "M1.P1.T1", "--min-impact", "2", "--min-urgency", "2"]),
    Probe("feedback list/no-positional", "feedback list", ["feedback", "list"]),

    Probe("feedback gate/ok", "feedback gate",
          ["feedback", "gate", "feedback.json", "--plan", "plan.json", "--threshold", "10"]),
    Probe("feedback gate/all-deferred", "feedback gate",
          ["feedback", "gate", "feedback.json", "--plan", "plan.json", "--threshold", "99"]),
    Probe("feedback gate/no-plan", "feedback gate", ["feedback", "gate", "feedback.json", "--threshold", "10"]),

    Probe("help/long", "help", ["--help"]),
    Probe("help/short", "help", ["-h"]),
    Probe("help/word", "help", ["help"]),
    Probe("help/bare", "help", []),
    Probe("help/unknown-command", "help", ["not-a-command"]),
]


def baseline_commands(root: Path) -> list[str]:
    """The subcommand roster M0.P1.T1 recorded — the coverage target this corpus must meet."""
    import json

    surface = json.loads((root / BASELINE_SURFACE_SUBPATH).read_text(encoding="utf-8"))
    return [c["name"] for c in surface["commands"]]


def coverage_gaps(root: Path) -> tuple[list[str], list[str]]:
    """Subcommands in the baseline with no probe, and probes naming a subcommand the baseline
    never had. Either direction is a corpus defect, not a divergence."""
    baseline = set(baseline_commands(root))
    probed = {p.command for p in PROBES}
    return sorted(baseline - probed), sorted(probed - baseline)
