# release-transaction — a release is one atomic transaction

A release is not a sequence of steps that may each succeed. It is **one transaction over seven enumerators**, and a release missing any one of them is a partial release that fails the gate with the missing enumerator named.

This directory is the contract. The interpreter that enforces it is `tooling/release-transaction/`.

## Layout

| File | Role | Version |
|---|---|---|
| `release-transaction.contract.json` | **The contract** — data only. The normative enumerator list, the evidence-kind vocabulary, the artifact-verification rules, the provisioning ladder, the changed-module rule, the release-pause hook, the message templates, the exit codes. | `release-transaction@1.0.0` |
| `release-transaction-record.schema.json` | **Record schema** (JSON Schema 2020-12) — the shape of one release declaring itself: subject, version, one binding per enumerator. | `release-transaction-record@1.0.0` |
| `release-pause-register.schema.json` | **Pause-register schema** (JSON Schema 2020-12) — the hand-off from compliance measurement to the release gate. | `release-pause-register@1.0.0` |

## The seven enumerators

Read them from `release-transaction.contract.json` — that array is the **only** normative enumeration. Nothing else restates it: not this README, not the record schema, not the interpreter. A document or a code path that carried its own copy would be the second place the set could drift, which is exactly the defect the contract exists to close.

Every enumerator resolves to exactly one status per release:

| Status | Meaning | Verdict |
|---|---|---|
| `satisfied` | the record's evidence proved it | passes |
| `not-applicable` | the record **declared** it inapplicable, with a reason the contract allows for that enumerator plus a human statement | passes |
| `missing` | the record carries no binding for it | fails, named |
| `unsatisfied` | the record bound it, and the evidence did not hold | fails, named, with the failure code |

Two of those statuses pass, two fail, and there is no fifth. Nothing is inferred: an enumerator that does not apply to a given module is a declared waiver in the record, reviewable in the diff, drawn from a closed per-enumerator reason set — a library declares `artifacts: no-release-archive`, and no module can declare its way out of a version, a tag, or a changelog.

## Verification properties

- **The manifest is rendered in one pass.** The assembled record is serialized exactly once and never post-edited, so identical inputs give identical bytes. A manifest whose bytes differ from the canonical rendering of their own content was assembled in more than one pass and is rejected. Canonical form is pinned in the contract (`verification.manifest.canonical_form`) precisely enough for a producer in any language to match it byte-for-byte.
- **A detached signature is required.** A missing or invalid signature **discards the manifest outright** — no content, no digests, no artifact is accepted from it.
- **Verification never degrades to checksum-only.** A digest list is only as trustworthy as the signature over it. A host with no verifier available reports `UNVERIFIABLE`, and its caller fails open to the raw OS tool — never to an unverified artifact.
- **The provisioning ladder prefers the current release.** A version-keyed cache is re-verified in place first (no network), then the current release, and only then a commit-frozen copy — so a host that can reach the release host always gets the current release rather than whatever the last commit froze. An authentication failure at the release rung **stops** the ladder; only an unreachable host continues down it.

## Changed-module rule

Any change to a released module carries a version bump **and** a published release. A changed module whose version equals the base ref's fails; a bumped module with no tag for the new version fails. The contract declares how a module root is recognised, where its version is read, and which tag names count as its release, plus the short, reviewable list of repository-infrastructure paths that ship in no release.

## Release-pause hook

An open below-floor compliance defect against a module **pauses that module's next release**: the transaction is refused even with every enumerator satisfied. The register is the hand-off — compliance measurement (`M10.P2.T3`, rung-4 compliance-floor measurement wiring) writes the entry, this gate reads it. The pause lands on the owner's next release and is never retroactive against the milestone that shipped the invariant. An entry still declared-unmeasured pauses nothing here; opening its defect belongs to the measurement wiring.

## Self-validation

```
python3 -m json.tool release-transaction.contract.json >/dev/null          # well-formed
python3 -m jsonschema -i <record.json>  release-transaction-record.schema.json
python3 -m jsonschema -i <register.json> release-pause-register.schema.json
```

The interpreter validates a record structurally on its own (stdlib-only, no validator dependency) so its errors can name the offending enumerator; the schemas above are the published contract for anyone else's validator.

## Record example

```json
{
  "schema": "release-transaction-record@1.0.0",
  "contract": "release-transaction@1.0.0",
  "subject": {
    "name": "governance-claude-marketplace",
    "kind": "plugin",
    "repo": "marketplace",
    "module_path": "plugins/governance-claude-marketplace",
    "marketplace": "jr-claude-plugins"
  },
  "version": "0.3.0",
  "enumerators": {
    "version": { "path": "marketplace/plugins/governance-claude-marketplace/.claude-plugin/plugin.json", "pointer": "/version" },
    "catalog_entry": { "path": "marketplace/.claude-plugin/marketplace.json" },
    "enable_state": { "path": "workspace/.claude/settings.json" },
    "tag": { "tag": "governance-claude-marketplace-v0.3.0" },
    "artifacts": { "not_applicable": { "reason": "no-release-archive", "detail": "A plugin of prose and hooks; consumers install it from the catalog, so no archive is published." } },
    "download_script_pin": { "not_applicable": { "reason": "no-provisioned-binary", "detail": "Ships no binary; nothing downloads it at runtime." } },
    "changelog": { "path": "marketplace/plugins/governance-claude-marketplace/README.md" }
  }
}
```

## Versioning

This is a language-agnostic contract module: it releases as `schemas/release-transaction/vX.Y.Z` — the tag prefix equals the module's path from the repository root, with no language segment. Every file here carries its own `version`/`$id`, so a consumer pins a contract version rather than a checkout.
