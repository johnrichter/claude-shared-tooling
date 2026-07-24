"""Interpreter for the release-transaction contract in `schemas/release-transaction/`.

A release is one atomic transaction over its enumerators. This package reads the contract,
resolves a release record's evidence, judges whether a changed module carries a release,
and provides the manifest rendering, signature verification, and provisioning ladder that
release producers and consumers share.

The contract is the only place the enumerator set is written down. Nothing here holds a
second copy of it, so a change to the contract changes the gate with it.

Modules:
    contract: Load the contract and one release record; JSON Pointer and template helpers.
    evidence: Resolve a record's evidence to per-enumerator statuses and one verdict.
    changed: Require a version bump and a published release for every changed module.
    provisioning: Manifest rendering, signature verification, and the provisioning ladder.
    gitstate: Read-only git access.
"""
from __future__ import annotations

from . import changed, contract, evidence, gitstate, provisioning

__all__ = ["changed", "contract", "evidence", "gitstate", "provisioning"]
