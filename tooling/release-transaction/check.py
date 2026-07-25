#!/usr/bin/env python3
"""Release-transaction gate — a release is one atomic transaction over its enumerators.

Stdlib-only, no setup: run it straight from a checkout. The enumerator set, the evidence
rules, the provisioning ladder, and the release-pause hook all come from the contract at
`schemas/release-transaction/`; this is its interpreter and command line.

Commands:
    verify-release   Judge one release record: every enumerator satisfied or declared
                     not-applicable, and no open compliance defect pausing the module.
    changed          Require every module changed since a base ref to carry a version bump
                     and a published release.
    render-manifest  Render a release manifest in one deterministic pass.
    verify-manifest  Authenticate a manifest against its detached signature (and, given an
                     artifact directory, check every artifact's bytes).
    provision        Resolve a verified artifact by walking the provisioning ladder.

Exit codes (declared in the contract): 0 complete or resolved, 1 partial release or failed
verification, 2 usage or contract error, 3 provisioning resolved nothing (the caller fails
open to the raw OS tool), 4 paused by an open compliance defect.

Usage:
    python3 tooling/release-transaction/check.py verify-release --record release.json
    python3 tooling/release-transaction/check.py changed --base origin/main
    python3 tooling/release-transaction/check.py render-manifest --version 1.2.3 --artifact linux-x86_64=dist/tool-linux-x86_64
    python3 tooling/release-transaction/check.py verify-manifest --manifest dist/manifest.json --public-key release-pubkey.pem
    python3 tooling/release-transaction/check.py provision --name tool --version 1.2.3 --arch linux-x86_64 --cache-dir ~/.cache/tool --public-key release-pubkey.pem
"""
from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Callable
from pathlib import Path

from release_transaction import changed as changed_gate
from release_transaction import evidence, provisioning
from release_transaction.contract import (
    ContractError,
    RecordError,
    load_contract,
    load_record,
)
from release_transaction.gitstate import GitError

_USAGE_ERROR = 2
_PROVISIONING_UNAVAILABLE = 3


def main(argv: list[str] | None = None) -> int:
    """Run the gate.

    Args:
        argv: Command-line arguments (default: `sys.argv[1:]`).

    Returns:
        The process exit code.
    """
    parser = _parser()
    args = parser.parse_args(argv)
    handler: Callable[[argparse.Namespace], int] = args.handler
    try:
        return handler(args)
    except (ContractError, RecordError, changed_gate.ChangedError, GitError, ValueError) as exc:
        print(f"release-transaction: {exc}", file=sys.stderr)
        return _USAGE_ERROR


def _parser() -> argparse.ArgumentParser:
    """Build the command-line parser."""
    parser = argparse.ArgumentParser(prog="release-transaction", description="Gate a release against the release-transaction contract.")
    subcommands = parser.add_subparsers(dest="command", required=True)

    verify = subcommands.add_parser("verify-release", help="Judge one release record against the contract.")
    verify.add_argument("--record", type=Path, required=True, help="Release record to judge.")
    verify.add_argument("--contract", type=Path, default=None, help="Contract file (default: the in-repo release-transaction contract).")
    verify.add_argument("--root", type=Path, default=None, help="Directory the record's paths resolve against (default: the working directory).")
    verify.add_argument("--pause-register", type=Path, default=None, help="Release-pause register to consult (default: the record's own, if it declares one).")
    verify.add_argument("--json", action="store_true", help="Emit the result as JSON.")
    verify.set_defaults(handler=_verify_release)

    changed = subcommands.add_parser("changed", help="Require every changed module to carry a version bump and a published release.")
    changed.add_argument("--base", default="origin/main", help="Base revision the change is measured from (default: origin/main).")
    changed.add_argument("--head", default="HEAD", help="Head revision (default: HEAD).")
    changed.add_argument("--repo", type=Path, default=None, help="Repository to inspect (default: the working directory).")
    changed.add_argument("--contract", type=Path, default=None, help="Contract file (default: the in-repo release-transaction contract).")
    changed.add_argument("--json", action="store_true", help="Emit the report as JSON.")
    changed.set_defaults(handler=_changed)

    render = subcommands.add_parser("render-manifest", help="Render a release manifest in one deterministic pass.")
    render.add_argument("--version", required=True, help="Version being released.")
    render.add_argument("--artifact", action="append", default=[], metavar="ARCH=PATH", help="An artifact to record; repeatable.")
    render.add_argument("--out", type=Path, default=None, help="Write here instead of standard output.")
    render.set_defaults(handler=_render_manifest)

    verify_manifest = subcommands.add_parser("verify-manifest", help="Authenticate a manifest against its detached signature.")
    verify_manifest.add_argument("--manifest", type=Path, required=True, help="Manifest file.")
    verify_manifest.add_argument("--signature", type=Path, default=None, help="Detached signature (default: <manifest>.sig).")
    verify_manifest.add_argument("--public-key", type=Path, required=True, help="PEM public key the signature is verified against.")
    verify_manifest.add_argument("--artifact-dir", type=Path, default=None, help="Also check every listed artifact's bytes in this directory.")
    verify_manifest.add_argument("--version", default=None, help="Also require the manifest to state this version.")
    verify_manifest.set_defaults(handler=_verify_manifest)

    provision = subcommands.add_parser("provision", help="Resolve a verified artifact by walking the provisioning ladder.")
    provision.add_argument("--name", required=True, help="Module name, as the cache and release asset spell it.")
    provision.add_argument("--version", required=True, help="Version to provision.")
    provision.add_argument("--arch", required=True, help="Arch id to resolve.")
    provision.add_argument("--cache-dir", type=Path, required=True, help="Version-keyed cache directory.")
    provision.add_argument("--public-key", type=Path, required=True, help="PEM public key for the release signature.")
    provision.add_argument("--base-url", default=None, help="Release host root (https:// or file://).")
    provision.add_argument("--frozen-dir", type=Path, default=None, help="Directory holding commit-frozen artifacts.")
    provision.set_defaults(handler=_provision)

    return parser


def _verify_release(args: argparse.Namespace) -> int:
    """Judge one release record and report per enumerator."""
    contract = load_contract(args.contract)
    root = (args.root or Path.cwd()).resolve()
    record = load_record(args.record, contract)

    register = args.pause_register
    if register is None and record.pause_register:
        candidate = Path(record.pause_register)
        register = candidate if candidate.is_absolute() else root / candidate
    transaction = evidence.evaluate(contract, record, root, pause_register=register)

    if args.json:
        print(json.dumps(_transaction_json(transaction), indent=2, sort_keys=True))
        return transaction.exit_code

    prefix = contract.message("prefix", subject=transaction.subject, version=transaction.version)
    for result in transaction.results:
        print(f"{prefix}{result.message}")
    for pause in transaction.pauses:
        print(f"{prefix}{pause.message}")
    if transaction.pauses and transaction.written_by:
        print(f"{prefix}pause register written by: {transaction.written_by}")
    summary = (
        contract.message("fail", count=len(transaction.failures))
        if transaction.failures
        else contract.message("pass")
    )
    print(f"{prefix}{summary}")
    return transaction.exit_code


def _transaction_json(transaction: evidence.Transaction) -> dict[str, object]:
    """The transaction as a JSON-serializable document."""
    return {
        "subject": transaction.subject,
        "version": transaction.version,
        "verdict": transaction.verdict,
        "exit_code": transaction.exit_code,
        "results": [
            {
                "enumerator": result.enumerator_id,
                "status": result.status.value,
                "code": result.code,
                "detail": result.detail,
            }
            for result in transaction.results
        ],
        "pauses": [
            {
                "defect_id": pause.defect_id,
                "owner": pause.owner,
                "owner_kind": pause.owner_kind,
                "invariant_id": pause.invariant_id,
            }
            for pause in transaction.pauses
        ],
    }


def _changed(args: argparse.Namespace) -> int:
    """Report every module that changed without a release."""
    contract = load_contract(args.contract)
    repo = (args.repo or Path.cwd()).resolve()
    report = changed_gate.check_changed(contract, repo, args.base, args.head)

    if args.json:
        print(
            json.dumps(
                {
                    "base": report.base,
                    "head": report.head,
                    "exit_code": report.exit_code,
                    "modules": [
                        {
                            "module": verdict.module.path,
                            "kind": verdict.module.kind,
                            "status": verdict.status,
                            "detail": verdict.detail,
                            "changed": list(verdict.changed),
                        }
                        for verdict in report.verdicts
                    ],
                    "exempt": list(report.exempt),
                    "unowned": list(report.unowned),
                },
                indent=2,
                sort_keys=True,
            )
        )
        return report.exit_code

    for verdict in report.verdicts:
        marker = "FAIL" if verdict.fails else "ok"
        print(f"release-transaction: {marker} {verdict.module.path} [{verdict.status}] - {verdict.detail}")
    if not report.verdicts:
        print(f"release-transaction: no released module changed between {report.base} and {report.head}")
    if report.failures:
        print(f"release-transaction: {len(report.failures)} changed module(s) carry no complete release")
    return report.exit_code


def _render_manifest(args: argparse.Namespace) -> int:
    """Render a manifest from `arch=path` artifact specs."""
    artifacts = provisioning.artifacts_from_specs(args.artifact)
    if not artifacts:
        raise ValueError("render-manifest needs at least one --artifact ARCH=PATH")
    text = provisioning.render_manifest(args.version, artifacts)
    if args.out is None:
        sys.stdout.write(text)
    else:
        args.out.write_text(text, encoding="utf-8")
        print(f"release-transaction: wrote {args.out}", file=sys.stderr)
    return 0


def _verify_manifest(args: argparse.Namespace) -> int:
    """Authenticate a manifest, then optionally check its version and artifacts."""
    signature = args.signature or Path(f"{args.manifest}.sig")
    verification = provisioning.verify_manifest(args.manifest, signature, args.public_key)
    print(f"release-transaction: {verification.verdict.value} - {verification.code}: {verification.detail}")
    if verification.verdict is provisioning.SignatureVerdict.UNVERIFIABLE:
        return _PROVISIONING_UNAVAILABLE
    manifest = verification.manifest
    if manifest is None:
        return 1

    if not provisioning.is_canonical(args.manifest.read_text(encoding="utf-8")):
        print(f"release-transaction: {args.manifest} is not the canonical rendering of its own content -- it was not produced by a single deterministic pass")
        return 1
    if args.version is not None and manifest.version != args.version:
        print(f"release-transaction: manifest states version {manifest.version}, not {args.version}")
        return 1
    if args.artifact_dir is not None:
        for artifact in manifest.artifacts:
            mismatch = provisioning.check_artifact(args.artifact_dir / artifact.filename, artifact)
            if mismatch is not None:
                print(f"release-transaction: {mismatch}")
                return 1
        print(f"release-transaction: {len(manifest.artifacts)} artifact(s) match their recorded digests")
    return 0


def _provision(args: argparse.Namespace) -> int:
    """Walk the provisioning ladder and report which rung resolved."""
    result = provisioning.provision(
        name=args.name,
        version=args.version,
        arch=args.arch,
        cache_dir=args.cache_dir,
        public_key=args.public_key,
        release_base_url=args.base_url,
        frozen_dir=args.frozen_dir,
    )
    for attempt in result.attempts:
        print(f"release-transaction: rung {attempt.rung.value} {attempt.outcome} - {attempt.detail}", file=sys.stderr)
    if not result.provisioned:
        return _PROVISIONING_UNAVAILABLE
    print(f"{result.path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
