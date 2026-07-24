"""Load the release-transaction contract and one release record.

The contract in `schemas/release-transaction/` is data, and this module is its reader:
nothing here knows which enumerators exist, what they are called, or which reasons may
waive them -- all of that is read from the contract file, so the enumerator set is pinned
in exactly one place and a checkout that edits it changes the gate with it.

A record is validated structurally here (shape, types, parameter names against the
evidence kind the contract declares) and semantically by `evidence` (does the evidence
hold). Enumerator COVERAGE is deliberately not a schema question: an unaccounted-for
enumerator is a partial release, so it is reported as a gate failure naming the
enumerator rather than as a malformed record.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path
from string import Template
from typing import Any

CONTRACT_DIR = Path(__file__).resolve().parents[3] / "schemas" / "release-transaction"
CONTRACT_PATH = CONTRACT_DIR / "release-transaction.contract.json"

RECORD_SCHEMA = "release-transaction-record@1.0.0"
_CONTRACT_KIND = "release-transaction-contract"
_VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$")


class ContractError(RuntimeError):
    """The contract file is absent, unreadable, or internally inconsistent."""


class RecordError(RuntimeError):
    """The release record is absent, unreadable, or malformed."""


@dataclass(frozen=True)
class Enumerator:
    """One participant in the transaction, as the contract declares it.

    Attributes:
        id: Enumerator id, used in every message about it.
        title: Human label.
        participates: What this enumerator asserts about a complete release.
        evidence_kind: Name of the evidence kind that proves it.
        not_applicable_reasons: Allowed waiver reasons, id to meaning. Empty means the
            enumerator can never be waived.
        missing_message: Detail rendered when a record carries no binding for it.
    """

    id: str
    title: str
    participates: str
    evidence_kind: str
    not_applicable_reasons: dict[str, str]
    missing_message: str


@dataclass(frozen=True)
class EvidenceKind:
    """The parameter vocabulary and failure codes of one evidence kind.

    Attributes:
        name: Kind name as enumerators reference it.
        meaning: What the kind checks.
        required: Parameter names a binding must supply.
        optional: Parameter names a binding may supply, mapped to their declared default
            (a template string, a literal, or a prose description of the fallback).
        failures: Failure code to meaning.
    """

    name: str
    meaning: str
    required: tuple[str, ...]
    optional: dict[str, Any]
    failures: dict[str, str]


@dataclass(frozen=True)
class Contract:
    """The parsed contract: enumerators, evidence vocabulary, and the declared rules."""

    version: str
    enumerators: tuple[Enumerator, ...]
    evidence_kinds: dict[str, EvidenceKind]
    verification: dict[str, Any]
    provisioning_ladder: tuple[dict[str, Any], ...]
    changed_module_rule: dict[str, Any]
    hooks: dict[str, Any]
    messages: dict[str, str]
    path: Path

    @property
    def enumerator_ids(self) -> tuple[str, ...]:
        """Enumerator ids in declared order -- the iteration order of every result."""
        return tuple(e.id for e in self.enumerators)

    def enumerator(self, enumerator_id: str) -> Enumerator:
        """The enumerator with this id.

        Raises:
            ContractError: no enumerator carries the id.
        """
        for enumerator in self.enumerators:
            if enumerator.id == enumerator_id:
                return enumerator
        raise ContractError(f"{self.path}: no enumerator '{enumerator_id}'")

    def evidence_kind(self, name: str) -> EvidenceKind:
        """The evidence kind with this name.

        Raises:
            ContractError: the contract declares no such kind.
        """
        try:
            return self.evidence_kinds[name]
        except KeyError:
            raise ContractError(f"{self.path}: no evidence kind '{name}'") from None

    def message(self, key: str, **fields: Any) -> str:
        """Render a contract message template.

        Args:
            key: Template key under the contract's `messages`.
            **fields: Placeholder values.

        Returns:
            The rendered message.

        Raises:
            ContractError: the template is absent or needs a placeholder not supplied.
        """
        template = self.messages.get(key)
        if template is None:
            raise ContractError(f"{self.path}: no message template '{key}'")
        try:
            return Template(template).substitute(fields)
        except (KeyError, ValueError) as exc:
            raise ContractError(f"{self.path}: message '{key}' cannot be rendered ({exc})") from None


@dataclass(frozen=True)
class Waiver:
    """A declared not-applicable enumerator."""

    reason: str
    detail: str


@dataclass(frozen=True)
class Binding:
    """One enumerator's binding in a record: evidence parameters, or a waiver."""

    params: dict[str, Any] | None
    waiver: Waiver | None

    @property
    def is_waiver(self) -> bool:
        """True if the binding declares the enumerator not-applicable."""
        return self.waiver is not None


@dataclass(frozen=True)
class Subject:
    """The module being released."""

    name: str
    kind: str
    module_path: str
    repo: str = "."
    marketplace: str | None = None


@dataclass(frozen=True)
class Record:
    """One release declaring itself against the contract."""

    subject: Subject
    version: str
    bindings: dict[str, Binding]
    pause_register: str | None
    path: Path


def load_contract(path: Path | None = None) -> Contract:
    """Read and validate the contract.

    Args:
        path: Contract file (default: the in-repo contract next to this tooling).

    Returns:
        The parsed contract.

    Raises:
        ContractError: the file is missing, unparsable, or inconsistent -- an unknown
            evidence kind, a duplicate enumerator id, or a missing message template.
    """
    contract_path = path or CONTRACT_PATH
    data = _read_json(contract_path, ContractError)
    if data.get("kind") != _CONTRACT_KIND:
        raise ContractError(f"{contract_path}: not a {_CONTRACT_KIND} (kind={data.get('kind')!r})")

    kinds: dict[str, EvidenceKind] = {}
    for name, raw in _require(contract_path, data, "evidence_kinds", dict).items():
        params = raw.get("params", {})
        kinds[name] = EvidenceKind(
            name=name,
            meaning=raw.get("meaning", ""),
            required=tuple(params.get("required", ())),
            optional=dict(params.get("optional", {})),
            failures=dict(raw.get("failures", {})),
        )

    enumerators: list[Enumerator] = []
    for raw in _require(contract_path, data, "enumerators", list):
        reasons = {r["id"]: r.get("meaning", "") for r in raw.get("not_applicable_reasons", ())}
        enumerator = Enumerator(
            id=raw["id"],
            title=raw.get("title", raw["id"]),
            participates=raw.get("participates", ""),
            evidence_kind=raw["evidence_kind"],
            not_applicable_reasons=reasons,
            missing_message=raw.get("missing_message", "no binding"),
        )
        if enumerator.evidence_kind not in kinds:
            raise ContractError(f"{contract_path}: enumerator '{enumerator.id}' names unknown evidence kind '{enumerator.evidence_kind}'")
        enumerators.append(enumerator)
    if not enumerators:
        raise ContractError(f"{contract_path}: the enumerator list is empty -- there is no transaction to check")
    duplicates = _duplicates([e.id for e in enumerators])
    if duplicates:
        raise ContractError(f"{contract_path}: enumerator id(s) declared more than once: {', '.join(duplicates)}")

    contract = Contract(
        version=_require(contract_path, data, "version", str),
        enumerators=tuple(enumerators),
        evidence_kinds=kinds,
        verification=_require(contract_path, data, "verification", dict),
        provisioning_ladder=tuple(_require(contract_path, data, "provisioning_ladder", list)),
        changed_module_rule=_require(contract_path, data, "changed_module_rule", dict),
        hooks=_require(contract_path, data, "hooks", dict),
        messages=_require(contract_path, data, "messages", dict),
        path=contract_path,
    )
    for key in ("prefix", "satisfied", "not_applicable", "missing", "unsatisfied", "paused", "pass", "fail"):
        if key not in contract.messages:
            raise ContractError(f"{contract_path}: messages is missing template '{key}'")
    return contract


def load_record(path: Path, contract: Contract) -> Record:
    """Read and structurally validate a release record against the contract.

    Checks the record's own shape, its declared schema and contract versions, and each
    binding's parameter names against the evidence kind its enumerator names. Does NOT
    check that every enumerator is bound: that is the gate's verdict, not a shape error.

    Args:
        path: Record file.
        contract: The loaded contract.

    Returns:
        The parsed record.

    Raises:
        RecordError: the record is missing, unparsable, or malformed.
    """
    data = _read_json(path, RecordError)
    schema = data.get("schema")
    if schema != RECORD_SCHEMA:
        raise RecordError(f"{path}: schema is {schema!r}, expected {RECORD_SCHEMA!r}")
    declared = data.get("contract")
    if declared != contract.version:
        raise RecordError(f"{path}: written against contract {declared!r}, but the loaded contract is {contract.version!r}")

    version = _require(path, data, "version", str, RecordError)
    if not _VERSION_RE.match(version):
        raise RecordError(f"{path}: version {version!r} is not a semantic version")

    raw_subject = _require(path, data, "subject", dict, RecordError)
    for field in ("name", "kind", "module_path"):
        if not isinstance(raw_subject.get(field), str) or not raw_subject[field]:
            raise RecordError(f"{path}: subject.{field} must be a non-empty string")
    subject = Subject(
        name=raw_subject["name"],
        kind=raw_subject["kind"],
        module_path=raw_subject["module_path"],
        repo=raw_subject.get("repo", "."),
        marketplace=raw_subject.get("marketplace"),
    )

    raw_bindings = _require(path, data, "enumerators", dict, RecordError)
    unknown = sorted(set(raw_bindings) - set(contract.enumerator_ids))
    if unknown:
        raise RecordError(f"{path}: enumerators names no such enumerator: {', '.join(unknown)}")

    bindings: dict[str, Binding] = {}
    for enumerator_id, raw in raw_bindings.items():
        if not isinstance(raw, dict) or not raw:
            raise RecordError(f"{path}: binding for '{enumerator_id}' must be a non-empty object")
        bindings[enumerator_id] = _binding(path, contract, enumerator_id, raw)

    return Record(
        subject=subject,
        version=version,
        bindings=bindings,
        pause_register=data.get("pause_register"),
        path=path,
    )


def _binding(path: Path, contract: Contract, enumerator_id: str, raw: dict[str, Any]) -> Binding:
    """Parse one binding, validating waiver shape or evidence parameter names."""
    if "not_applicable" in raw:
        if len(raw) != 1:
            raise RecordError(f"{path}: binding for '{enumerator_id}' mixes a not-applicable declaration with evidence")
        waiver = raw["not_applicable"]
        if not isinstance(waiver, dict):
            raise RecordError(f"{path}: binding for '{enumerator_id}': not_applicable must be an object")
        for field in ("reason", "detail"):
            if not isinstance(waiver.get(field), str) or not waiver[field].strip():
                raise RecordError(f"{path}: binding for '{enumerator_id}': not_applicable.{field} must be a non-empty string")
        return Binding(params=None, waiver=Waiver(reason=waiver["reason"], detail=waiver["detail"]))

    kind = contract.evidence_kind(contract.enumerator(enumerator_id).evidence_kind)
    allowed = set(kind.required) | set(kind.optional)
    unknown = sorted(set(raw) - allowed)
    if unknown:
        raise RecordError(f"{path}: binding for '{enumerator_id}' carries parameter(s) '{', '.join(unknown)}' that evidence kind '{kind.name}' does not declare")
    missing = sorted(set(kind.required) - set(raw))
    if missing:
        raise RecordError(f"{path}: binding for '{enumerator_id}' is missing required parameter(s) '{', '.join(missing)}' of evidence kind '{kind.name}'")
    return Binding(params=dict(raw), waiver=None)


def resolve_pointer(document: Any, pointer: str) -> tuple[bool, Any]:
    """Resolve an RFC 6901 JSON Pointer.

    Args:
        document: Parsed JSON document.
        pointer: Pointer string; the empty pointer selects the whole document.

    Returns:
        A (found, value) pair. `found` is False when any segment does not exist, so a
        legitimately null value is distinguishable from an absent one.

    Raises:
        ValueError: the pointer is not a JSON Pointer (no leading slash).
    """
    if pointer == "":
        return True, document
    if not pointer.startswith("/"):
        raise ValueError(f"{pointer!r} is not an RFC 6901 JSON Pointer")
    current = document
    for raw_segment in pointer[1:].split("/"):
        segment = raw_segment.replace("~1", "/").replace("~0", "~")
        if isinstance(current, dict):
            if segment not in current:
                return False, None
            current = current[segment]
        elif isinstance(current, list):
            if not segment.isdigit() or int(segment) >= len(current):
                return False, None
            current = current[int(segment)]
        else:
            return False, None
    return True, current


def render_template(template: str, context: dict[str, str]) -> str:
    """Substitute `$name` placeholders in a contract-declared default.

    `$name` form rather than brace form, so a regex-valued default (`#{1,6}`) carries no
    placeholder ambiguity.

    Args:
        template: Template string from the contract.
        context: Placeholder values.

    Returns:
        The substituted string.

    Raises:
        ContractError: the template needs a placeholder the context does not carry, or is
            not a valid template.
    """
    try:
        return Template(template).substitute(context)
    except (KeyError, ValueError) as exc:
        raise ContractError(f"template {template!r} cannot be rendered ({exc})") from None


def parse_version(version: str, pattern: str) -> tuple[int, int, int] | None:
    """Numeric triple of a version, or None when it does not match `pattern`.

    Args:
        version: Version string.
        pattern: Regex with three numeric capture groups, from the contract.

    Returns:
        The (major, minor, patch) triple, or None.
    """
    match = re.match(pattern, version)
    if not match:
        return None
    return int(match.group(1)), int(match.group(2)), int(match.group(3))


def _read_json(path: Path, error: type[RuntimeError]) -> dict[str, Any]:
    """Read a JSON object, raising `error` on any failure."""
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise error(f"{path}: cannot read ({exc})") from None
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise error(f"{path}: invalid JSON ({exc})") from None
    if not isinstance(data, dict):
        raise error(f"{path}: expected a JSON object")
    return data


def _require(path: Path, data: dict[str, Any], key: str, kind: type, error: type[RuntimeError] = ContractError) -> Any:
    """Return `data[key]`, raising `error` when absent or of the wrong type."""
    value = data.get(key)
    if not isinstance(value, kind):
        raise error(f"{path}: '{key}' must be present and of type {kind.__name__}")
    return value


def _duplicates(values: list[str]) -> list[str]:
    """Values appearing more than once, in first-seen order."""
    seen: set[str] = set()
    repeated: list[str] = []
    for value in values:
        if value in seen and value not in repeated:
            repeated.append(value)
        seen.add(value)
    return repeated
