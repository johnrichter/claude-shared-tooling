"""Resolve a release record's evidence and reach one transaction verdict.

Every enumerator the contract declares lands on exactly one of four statuses -- satisfied,
not-applicable, missing, unsatisfied -- and two of those fail the gate with the enumerator
named. Nothing is inferred: an enumerator that does not apply to a module is a declared
waiver in the record, drawn from the closed reason set the contract allows for that
enumerator, so a waiver is visible in a diff and a non-waivable enumerator cannot be
argued away.

The evidence kinds live in a dispatch table keyed by the name the contract gives each kind,
so the enumerator set stays in the contract and this module only knows how to look things
up.

Gate direction is deliberately opposite to the runtime's: a host that cannot verify a
signature at provisioning time falls open to the raw OS tool, but a release gate that
cannot verify a signature FAILS -- an unverifiable artifact is not a releasable one.
"""
from __future__ import annotations

import json
import os
import re
from collections.abc import Callable
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any

from . import gitstate, provisioning
from .contract import (
    Binding,
    Contract,
    EvidenceKind,
    Record,
    RecordError,
    render_template,
    resolve_pointer,
)


class Status(str, Enum):
    """Status of one enumerator in a release."""

    SATISFIED = "satisfied"
    NOT_APPLICABLE = "not-applicable"
    MISSING = "missing"
    UNSATISFIED = "unsatisfied"

    @property
    def fails(self) -> bool:
        """True if this status makes the transaction partial."""
        return self in (Status.MISSING, Status.UNSATISFIED)


@dataclass(frozen=True)
class Result:
    """One enumerator's outcome, with the message the contract renders for it."""

    enumerator_id: str
    status: Status
    code: str
    detail: str
    message: str


@dataclass(frozen=True)
class Pause:
    """An open compliance defect pausing this module's next release."""

    defect_id: str
    owner: str
    owner_kind: str
    invariant_id: str
    message: str


@dataclass(frozen=True)
class Transaction:
    """The verdict on one release."""

    subject: str
    version: str
    results: tuple[Result, ...]
    pauses: tuple[Pause, ...]
    written_by: str

    @property
    def failures(self) -> tuple[Result, ...]:
        """Results that make the release partial, in contract order."""
        return tuple(result for result in self.results if result.status.fails)

    @property
    def verdict(self) -> str:
        """`fail`, `paused`, or `pass`."""
        if self.failures:
            return "fail"
        if self.pauses:
            return "paused"
        return "pass"

    @property
    def exit_code(self) -> int:
        """Process exit code for this verdict, per the contract's declared codes."""
        return {"fail": 1, "paused": 4, "pass": 0}[self.verdict]


@dataclass(frozen=True)
class _Context:
    """Everything an evidence resolver needs to look up one enumerator's evidence."""

    contract: Contract
    record: Record
    root: Path
    enumerator_id: str
    params: dict[str, Any]
    verifier: Callable[[Path, Path, Path], bool | None]

    @property
    def version(self) -> str:
        """The version being released."""
        return self.record.version

    def path(self, value: str) -> Path:
        """Resolve a record path against the interpreter root (absolute paths pass through)."""
        candidate = Path(value)
        return candidate if candidate.is_absolute() else self.root / candidate


def evaluate(
    contract: Contract,
    record: Record,
    root: Path,
    *,
    pause_register: Path | None = None,
    verifier: Callable[[Path, Path, Path], bool | None] = provisioning.openssl_verifier,
) -> Transaction:
    """Resolve every enumerator the contract declares and reach a verdict.

    Args:
        contract: The loaded contract.
        record: The release record.
        root: Directory the record's paths resolve against.
        pause_register: Release-pause register to consult; None consults none.
        verifier: Signature verifier for the artifact evidence.

    Returns:
        The transaction, its results in the contract's declared enumerator order.

    Raises:
        RecordError: the pause register exists but is malformed -- a register that cannot be
            read is never treated as an absence of defects.
        ContractError: the contract and this interpreter disagree about an evidence kind.
    """
    results = [_resolve(contract, record, root, enumerator.id, verifier) for enumerator in contract.enumerators]
    pauses = _pauses(contract, record, pause_register)
    written_by = contract.hooks.get("release_pause", {}).get("written_by", "")
    return Transaction(
        subject=record.subject.name,
        version=record.version,
        results=tuple(results),
        pauses=tuple(pauses),
        written_by=written_by,
    )


def _resolve(
    contract: Contract,
    record: Record,
    root: Path,
    enumerator_id: str,
    verifier: Callable[[Path, Path, Path], bool | None],
) -> Result:
    """Resolve one enumerator to a status and a rendered message."""
    enumerator = contract.enumerator(enumerator_id)
    binding = record.bindings.get(enumerator_id)
    if binding is None:
        return _result(contract, enumerator_id, Status.MISSING, "binding-absent", enumerator.missing_message)
    if binding.is_waiver:
        return _waiver_result(contract, enumerator_id, binding)

    kind = contract.evidence_kind(enumerator.evidence_kind)
    context = _Context(
        contract=contract,
        record=record,
        root=root,
        enumerator_id=enumerator_id,
        params=_with_defaults(kind, binding, record),
        verifier=verifier,
    )
    code, detail = _RESOLVERS[kind.name](context)
    if code is None:
        return _result(contract, enumerator_id, Status.SATISFIED, "verified", detail)
    return _result(contract, enumerator_id, Status.UNSATISFIED, code, detail)


def _waiver_result(contract: Contract, enumerator_id: str, binding: Binding) -> Result:
    """Accept a declared waiver, or reject a reason the contract does not allow here."""
    enumerator = contract.enumerator(enumerator_id)
    waiver = binding.waiver
    assert waiver is not None  # guaranteed by Binding.is_waiver
    allowed = enumerator.not_applicable_reasons
    if not allowed:
        return _result(
            contract,
            enumerator_id,
            Status.UNSATISFIED,
            "waiver-not-permitted",
            f"enumerator '{enumerator_id}' can never be declared not-applicable, and this release declares it so with reason '{waiver.reason}'",
        )
    if waiver.reason not in allowed:
        return _result(
            contract,
            enumerator_id,
            Status.UNSATISFIED,
            "waiver-not-declared",
            f"reason '{waiver.reason}' is not declared for enumerator '{enumerator_id}' (allowed: {', '.join(sorted(allowed))})",
        )
    message = contract.message("not_applicable", id=enumerator_id, reason=waiver.reason, detail=waiver.detail)
    return Result(enumerator_id=enumerator_id, status=Status.NOT_APPLICABLE, code=waiver.reason, detail=waiver.detail, message=message)


def _result(contract: Contract, enumerator_id: str, status: Status, code: str, detail: str) -> Result:
    """Build a result with the contract's message template for its status."""
    key = {Status.SATISFIED: "satisfied", Status.MISSING: "missing", Status.UNSATISFIED: "unsatisfied"}[status]
    message = contract.message(key, id=enumerator_id, code=code, detail=detail)
    return Result(enumerator_id=enumerator_id, status=status, code=code, detail=detail, message=message)


def _substitutions(record: Record) -> dict[str, str]:
    """Values a contract-declared default template may reference."""
    subject = record.subject
    return {
        "subject_name": subject.name,
        "subject_kind": subject.kind,
        "module_name": Path(subject.module_path).name,
        "module_path": subject.module_path,
        "marketplace": subject.marketplace or "",
        "version": record.version,
        "version_regex": re.escape(record.version),
    }


def _with_defaults(kind: EvidenceKind, binding: Binding, record: Record) -> dict[str, Any]:
    """Apply the contract's declared literal defaults to a binding's parameters.

    An optional parameter declared with a `default` is filled in, its template substituted
    from the subject, the version, and the parameters the binding already supplies. A
    parameter carrying only a `note` has no literal default and is derived by the resolver.
    """
    params = dict(binding.params or {})
    substitutions = _substitutions(record)
    substitutions.update({name: value for name, value in params.items() if isinstance(value, str)})
    for name, declaration in kind.optional.items():
        if name in params or not isinstance(declaration, dict) or "default" not in declaration:
            continue
        default = declaration["default"]
        params[name] = render_template(default, substitutions) if isinstance(default, str) else default
    return params


def _json_field_equals_version(context: _Context) -> tuple[str | None, str]:
    """The bound JSON field states the released version."""
    path = context.path(context.params["path"])
    document, failure = _read_json(path)
    if failure is not None:
        return failure
    found, value = resolve_pointer(document, context.params["pointer"])
    if not found:
        return "pointer-absent", f"{path}: {context.params['pointer']} resolves to nothing"
    if value != context.version:
        return "value-mismatch", f"{path}: {context.params['pointer']} is {value!r}, not the released version {context.version!r}"
    return None, f"{path} states version {context.version}"


def _catalog_entry(context: _Context) -> tuple[str | None, str]:
    """The catalog entry for this module carries the released version."""
    path = context.path(context.params["path"])
    document, failure = _read_json(path)
    if failure is not None:
        return failure
    pointer = context.params["entries_pointer"]
    found, entries = resolve_pointer(document, pointer)
    if not found or not isinstance(entries, list):
        return "entries-absent", f"{path}: {pointer} does not resolve to an array of entries"

    name_field, version_field = context.params["name_field"], context.params["version_field"]
    entry_name = context.params["entry_name"]
    for entry in entries:
        if isinstance(entry, dict) and entry.get(name_field) == entry_name:
            if version_field not in entry:
                return "version-field-absent", f"{path}: entry {entry_name!r} carries no {version_field!r} field, so the catalog cannot agree with the module about a version"
            if entry[version_field] != context.version:
                return "value-mismatch", f"{path}: entry {entry_name!r} is at {entry[version_field]!r}, not the released version {context.version!r}"
            return None, f"{path} publishes {entry_name} at {context.version}"
    return "entry-absent", f"{path}: no entry whose {name_field!r} is {entry_name!r}"


def _enable_state(context: _Context) -> tuple[str | None, str]:
    """The consumer settings mirror this module at its expected enable state."""
    key = context.params["key"]
    if key.endswith("@"):
        return "binding-invalid", "the default enable-state key needs subject.marketplace, which the record does not declare -- supply an explicit key"
    path = context.path(context.params["path"])
    document, failure = _read_json(path)
    if failure is not None:
        return failure
    pointer = context.params["pointer"]
    found, states = resolve_pointer(document, pointer)
    if not found or not isinstance(states, dict):
        return "pointer-absent", f"{path}: {pointer} does not resolve to an enable-state map"
    if key not in states:
        return "key-absent", f"{path}: {pointer} carries no key {key!r}"
    expected = context.params["expected"]
    if states[key] != expected:
        return "value-mismatch", f"{path}: {key!r} is {states[key]!r}, expected {expected!r}"
    return None, f"{path} enables {key}"


def _git_tag(context: _Context) -> tuple[str | None, str]:
    """A conventional tag exists and points at the module in its released state."""
    tag = context.params["tag"]
    repo = context.path(context.record.subject.repo)
    substitutions = _substitutions(context.record)
    templates = context.contract.changed_module_rule.get("tag_templates", ())
    conventional = [render_template(template, substitutions) for template in templates]
    if tag not in conventional:
        return "tag-off-convention", f"tag {tag!r} matches none of the conventional names for this module and version: {', '.join(conventional)}"
    if not gitstate.is_repo(repo):
        return "git-unavailable", f"{repo} is not a git working tree"
    if not gitstate.tag_exists(repo, tag):
        return "tag-absent", f"{repo}: no tag {tag!r}"

    manifest_path, manifest_pointer = _tag_manifest_binding(context)
    if manifest_path is None or manifest_pointer is None:
        return "manifest-binding-absent", f"tag {tag!r} exists, but no manifest binding says what the module's version should be at it"
    relative = os.path.relpath(context.path(manifest_path), repo)
    blob = gitstate.file_at_ref(repo, tag, relative)
    if blob is None:
        return "tag-tree-mismatch", f"tag {tag!r} carries no {relative}"
    try:
        document = json.loads(blob)
    except json.JSONDecodeError as exc:
        return "tag-tree-mismatch", f"tag {tag!r}: {relative} is not valid JSON ({exc})"
    found, value = resolve_pointer(document, manifest_pointer)
    if not found or value != context.version:
        return "tag-tree-mismatch", f"tag {tag!r}: {relative} states {value!r}, not the released version {context.version!r} -- the tag and the version are not one transaction"
    return None, f"{repo}: tag {tag} points at {relative} @ {context.version}"


def _tag_manifest_binding(context: _Context) -> tuple[str | None, str | None]:
    """The manifest path/pointer to compare against a tag's tree, explicit or borrowed.

    The contract declares the borrow: with no explicit `manifest_path`/`manifest_pointer`,
    the tag is checked against whatever the version enumerator binds, so one manifest
    location serves both and the two cannot drift apart. A record that binds no version
    evidence therefore fails at `tag` as well -- correct either way, since neither
    enumerator is waivable and a release with no stated version is not a release.
    """
    path = context.params.get("manifest_path")
    pointer = context.params.get("manifest_pointer")
    if path and pointer:
        return path, pointer
    for enumerator in context.contract.enumerators:
        if enumerator.evidence_kind != "json_field_equals_version":
            continue
        binding = context.record.bindings.get(enumerator.id)
        if binding is not None and binding.params:
            return path or binding.params.get("path"), pointer or binding.params.get("pointer")
    return path, pointer


def _signed_manifest(context: _Context) -> tuple[str | None, str]:
    """The published manifest is canonical, authentic, and matches every artifact's bytes."""
    manifest_path = context.path(context.params["manifest"])
    if not manifest_path.is_file():
        return "manifest-absent", f"no manifest at {manifest_path}"
    try:
        text = manifest_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        return "manifest-unparsable", f"{manifest_path}: {exc}"
    try:
        provisioning.parse_manifest(text)
    except provisioning.ManifestError as exc:
        return "manifest-unparsable", f"{manifest_path}: {exc}"
    if not provisioning.is_canonical(text):
        return "manifest-non-canonical", f"{manifest_path} is not the canonical rendering of its own content -- it was not produced by a single deterministic pass"

    signature = context.path(context.params["signature"])
    public_key = context.path(context.params["public_key"])
    verification = provisioning.verify_manifest(manifest_path, signature, public_key, verifier=context.verifier)
    if verification.verdict is not provisioning.SignatureVerdict.VERIFIED:
        return verification.code, verification.detail
    manifest = verification.manifest
    assert manifest is not None  # a VERIFIED verification always carries one

    if manifest.version != context.version:
        return "version-mismatch", f"{manifest_path} states version {manifest.version!r}, not the released version {context.version!r}"
    for arch in context.params.get("require_arches", ()):
        if manifest.artifact(arch) is None:
            return "arch-absent", f"{manifest_path} carries no artifact for required arch {arch!r}"

    artifact_dir = context.path(context.params["artifact_dir"]) if context.params.get("artifact_dir") else manifest_path.parent
    for artifact in manifest.artifacts:
        mismatch = provisioning.check_artifact(artifact_dir / artifact.filename, artifact)
        if mismatch is not None:
            code = "artifact-absent" if "not present" in mismatch else "artifact-mismatch"
            return code, mismatch
    return None, f"{manifest_path} verified against {public_key}; {len(manifest.artifacts)} artifact(s) match their recorded digests"


def _pin_equals_version(context: _Context) -> tuple[str | None, str]:
    """The provisioning pin names the released version, and only it."""
    path = context.path(context.params["path"])
    pointer, pattern = context.params.get("pointer"), context.params.get("pattern")
    if bool(pointer) == bool(pattern):
        return "binding-invalid", "the binding must name exactly one of 'pointer' (a JSON pin) or 'pattern' (a regex over the file's text)"

    if pointer:
        document, failure = _read_json(path)
        if failure is not None:
            return failure
        found, value = resolve_pointer(document, pointer)
        if not found:
            return "pin-absent", f"{path}: {pointer} resolves to nothing"
        pins = [str(value)]
    else:
        expression = str(pattern)
        try:
            text = path.read_text(encoding="utf-8")
        except OSError as exc:
            return "file-absent", f"{path}: cannot read ({exc})"
        try:
            pins = [match.group(1) for match in re.finditer(expression, text)]
        except re.error as exc:
            return "binding-invalid", f"pattern {expression!r} is not a valid regex ({exc})"
        if not pins:
            return "pin-absent", f"{path}: pattern {expression!r} matched nothing"

    distinct = sorted(set(pins))
    if len(distinct) > 1:
        return "pin-disagreement", f"{path} pins more than one version: {', '.join(distinct)}"
    if distinct[0] != context.version:
        return "value-mismatch", f"{path} pins {distinct[0]!r}, not the released version {context.version!r}"
    return None, f"{path} pins {context.version}"


def _changelog_entry(context: _Context) -> tuple[str | None, str]:
    """The changelog records the released version."""
    path = context.path(context.params["path"])
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        return "file-absent", f"{path}: cannot read ({exc})"
    pattern = context.params["pattern"]
    try:
        matched = re.search(pattern, text) is not None
    except re.error as exc:
        return "entry-absent", f"pattern {pattern!r} is not a valid regex ({exc})"
    if not matched:
        return "entry-absent", f"{path} carries no entry naming version {context.version}"
    return None, f"{path} records version {context.version}"


_RESOLVERS: dict[str, Callable[[_Context], tuple[str | None, str]]] = {
    "json_field_equals_version": _json_field_equals_version,
    "catalog_entry_version": _catalog_entry,
    "enable_state_mirror": _enable_state,
    "git_tag": _git_tag,
    "signed_manifest": _signed_manifest,
    "pin_equals_version": _pin_equals_version,
    "changelog_entry": _changelog_entry,
}


def _pauses(contract: Contract, record: Record, register: Path | None) -> list[Pause]:
    """Open compliance defects against this module, from the release-pause register.

    Args:
        contract: The loaded contract, for the pause message template.
        record: The release record, whose subject name is matched against defect owners.
        register: Register file; None or absent means nothing pauses this release.

    Returns:
        One pause per open defect owned by this module.

    Raises:
        RecordError: the register exists but is unreadable or malformed. A register that
            cannot be read is never read as an absence of defects.
    """
    if register is None or not register.exists():
        return []
    try:
        data = json.loads(register.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise RecordError(f"{register}: unreadable release-pause register ({exc})") from None
    if not isinstance(data, dict) or not isinstance(data.get("entries"), list):
        raise RecordError(f"{register}: not a release-pause register (no 'entries' array)")

    pauses: list[Pause] = []
    for index, entry in enumerate(data["entries"]):
        if not isinstance(entry, dict):
            raise RecordError(f"{register}: entries[{index}] is not an object")
        missing = [field for field in ("defect_id", "owner", "owner_kind", "invariant_id", "status") if not entry.get(field)]
        if missing:
            raise RecordError(f"{register}: entries[{index}] is missing {', '.join(missing)}")
        if entry["owner"] != record.subject.name or entry["status"] != "open":
            continue
        message = contract.message(
            "paused",
            defect_id=entry["defect_id"],
            owner=entry["owner"],
            invariant_id=entry["invariant_id"],
        )
        pauses.append(
            Pause(
                defect_id=entry["defect_id"],
                owner=entry["owner"],
                owner_kind=entry["owner_kind"],
                invariant_id=entry["invariant_id"],
                message=message,
            )
        )
    return pauses


def _read_json(path: Path) -> tuple[Any, tuple[str, str] | None]:
    """Read a JSON document, returning (document, None) or (None, (code, detail))."""
    if not path.is_file():
        return None, ("file-absent", f"no file at {path}")
    try:
        return json.loads(path.read_text(encoding="utf-8")), None
    except (OSError, UnicodeDecodeError) as exc:
        return None, ("file-absent", f"{path}: cannot read ({exc})")
    except json.JSONDecodeError as exc:
        return None, ("file-unparsable", f"{path}: invalid JSON ({exc})")
