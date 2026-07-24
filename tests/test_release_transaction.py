#!/usr/bin/env python3
"""Unit tests for the release-transaction contract and its interpreter.

The package lives at `tooling/release-transaction/` (a hyphenated directory, so it is not
importable as a dotted path); that directory is put on `sys.path` here, exactly as running
`check.py` does. Fixtures are throwaway git repos and temp trees -- nothing reads the real
repository, and no test needs a network.

Coverage (mirrors the contract's stated invariants):
    1. The enumerator set is pinned in the contract and nowhere else: the exact seven in
       declared order, and the gate demands whatever the contract declares rather than a
       list built into the interpreter.
    2. A release missing any one enumerator fails with that enumerator named.
    3. Each enumerator's evidence catches the drift it exists for, and a waiver is only ever
       a DECLARED one drawn from the contract's closed per-enumerator reason set.
    4. A manifest is rendered in one deterministic pass; a missing or invalid detached
       signature discards it outright, and verification never degrades to checksum-only.
    5. The ladder re-verifies a version-keyed cache in place and prefers the current release
       over a commit-frozen copy.
    6. A change with no version bump, or with no published release, fails the gate.
    7. An open below-floor compliance defect pauses the owning module's next release.
"""
from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_TOOLING = Path(__file__).resolve().parent.parent / "tooling" / "release-transaction"
if str(_TOOLING) not in sys.path:
    sys.path.insert(0, str(_TOOLING))

from release_transaction import changed as changed_gate  # noqa: E402
from release_transaction import evidence, provisioning  # noqa: E402
from release_transaction.contract import (  # noqa: E402
    CONTRACT_DIR,
    CONTRACT_PATH,
    ContractError,
    RecordError,
    load_contract,
    load_record,
)

# The seven the contract declares. Restated here ON PURPOSE and only here: this is the
# assertion that the set is pinned exactly and in order, so an unreviewed contract edit
# fails a test instead of silently changing what a release means.
_ENUMERATORS = ("version", "catalog_entry", "enable_state", "tag", "artifacts", "download_script_pin", "changelog")

_NAME = "demo"
_MODULE = "plugins/demo"
_MARKETPLACE = "ts-ai"
_VERSION = "1.2.3"
_ARCH = "linux-x86_64"
_PAYLOAD = b"#!/bin/sh\necho demo\n"
_FROZEN_PAYLOAD = b"#!/bin/sh\necho frozen\n"
_STUB_KEY = "-----BEGIN PUBLIC KEY-----\nstub\n-----END PUBLIC KEY-----\n"


def _accepts(manifest: Path, signature: Path, public_key: Path) -> bool:
    """Verifier that authenticates whatever it is handed."""
    return True


def _rejects(manifest: Path, signature: Path, public_key: Path) -> bool:
    """Verifier that refuses the signature."""
    return False


def _cannot_verify(manifest: Path, signature: Path, public_key: Path) -> None:
    """Verifier standing in for a host with no verifier tool at all."""
    return None


def _git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True)
    return result.stdout.strip()


def _init_repo(root: Path) -> None:
    _git(root, "init", "-q")
    _git(root, "config", "user.email", "test@example.com")
    _git(root, "config", "user.name", "Test")


def _commit(root: Path, message: str) -> str:
    _git(root, "add", "-A")
    _git(root, "commit", "-q", "-m", message)
    return _git(root, "rev-parse", "HEAD")


def _write(root: Path, relative: str, text: str) -> Path:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def _write_json(root: Path, relative: str, document: object) -> Path:
    return _write(root, relative, json.dumps(document, indent=2) + "\n")


def _publish(directory: Path, *, name: str, version: str, arch: str, payload: bytes) -> str:
    """Lay out a release directory the ladder can fetch over `file://`.

    Returns:
        The base URL to hand to `provision`.
    """
    release_dir = directory / f"{name}-v{version}"
    release_dir.mkdir(parents=True, exist_ok=True)
    artifact = release_dir / f"{name}-{arch}"
    artifact.write_bytes(payload)
    manifest = provisioning.render_manifest(version, [provisioning.Artifact.from_path(arch, artifact)])
    (release_dir / "manifest.json").write_text(manifest, encoding="utf-8")
    (release_dir / "manifest.json.sig").write_text("detached-signature\n", encoding="utf-8")
    return directory.as_uri()


def _freeze(directory: Path, *, name: str, arch: str, payload: bytes) -> Path:
    """Write a commit-frozen artifact plus its committed digest sidecar."""
    directory.mkdir(parents=True, exist_ok=True)
    artifact = directory / f"{name}-{arch}"
    artifact.write_bytes(payload)
    Path(f"{artifact}.sha256").write_text(f"{provisioning.sha256_of(artifact)}\n", encoding="utf-8")
    return artifact


class _ReleaseCase(unittest.TestCase):
    """A committed, tagged `demo` plugin release in which every enumerator is satisfiable."""

    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.contract = load_contract()
        _init_repo(self.root)
        self._build_tree(_VERSION)
        _commit(self.root, f"release {_NAME} {_VERSION}")
        _git(self.root, "tag", f"{_MODULE}/v{_VERSION}")

    def _build_tree(self, version: str) -> None:
        """Every file the seven enumerators point at, all agreeing on `version`."""
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": version})
        _write(self.root, f"{_MODULE}/CHANGELOG.md", f"# Changelog\n\n## {version}\n\n- released\n")
        _write_json(self.root, ".claude-plugin/marketplace.json", {"plugins": [{"name": _NAME, "source": f"./{_MODULE}", "version": version}]})
        _write_json(self.root, ".claude/settings.json", {"enabledPlugins": {f"{_NAME}@{_MARKETPLACE}": True}})
        _write(self.root, "scripts/install.sh", f'#!/bin/sh\nVERSION="{version}"\n')
        artifact = self.root / "dist" / f"{_NAME}-{_ARCH}"
        artifact.parent.mkdir(parents=True, exist_ok=True)
        artifact.write_bytes(_PAYLOAD)
        _write(self.root, "dist/manifest.json", provisioning.render_manifest(version, [provisioning.Artifact.from_path(_ARCH, artifact)]))
        _write(self.root, "dist/manifest.json.sig", "detached-signature\n")
        _write(self.root, "release/pubkey.pem", _STUB_KEY)

    def _document(self, version: str = _VERSION) -> dict:
        """The record that declares this release complete."""
        return {
            "schema": "release-transaction-record@1.0.0",
            "contract": self.contract.version,
            "subject": {"name": _NAME, "kind": "plugin", "repo": ".", "module_path": _MODULE, "marketplace": _MARKETPLACE},
            "version": version,
            "enumerators": {
                "version": {"path": f"{_MODULE}/.claude-plugin/plugin.json", "pointer": "/version"},
                "catalog_entry": {"path": ".claude-plugin/marketplace.json"},
                "enable_state": {"path": ".claude/settings.json"},
                "tag": {"tag": f"{_MODULE}/v{version}"},
                "artifacts": {"manifest": "dist/manifest.json", "public_key": "release/pubkey.pem"},
                "download_script_pin": {"path": "scripts/install.sh", "pattern": 'VERSION="([^"]+)"'},
                "changelog": {"path": f"{_MODULE}/CHANGELOG.md"},
            },
        }

    def _record(self, *, drop: str | None = None, bindings: dict | None = None, document: dict | None = None, contract=None):
        """Write the record, with an enumerator dropped or its binding replaced, and load it."""
        document = document or self._document()
        if drop is not None:
            del document["enumerators"][drop]
        for enumerator_id, binding in (bindings or {}).items():
            document["enumerators"][enumerator_id] = binding
        path = _write_json(self.root, "release.json", document)
        return load_record(path, contract or self.contract)

    def _evaluate(self, record, *, verifier=_accepts, pause_register: Path | None = None, contract=None):
        return evidence.evaluate(contract or self.contract, record, self.root, pause_register=pause_register, verifier=verifier)

    def _result(self, transaction, enumerator_id: str):
        """The one result for `enumerator_id` -- every enumerator resolves exactly once."""
        matches = [result for result in transaction.results if result.enumerator_id == enumerator_id]
        self.assertEqual(len(matches), 1, msg=f"{enumerator_id}: {[r.enumerator_id for r in transaction.results]}")
        return matches[0]


class EnumeratorSetTests(unittest.TestCase):
    """The enumerator set is pinned exactly once, in the contract."""

    def setUp(self) -> None:
        self.contract = load_contract()

    def test_the_contract_pins_the_seven_enumerators_in_order(self):
        self.assertEqual(self.contract.enumerator_ids, _ENUMERATORS)

    def test_every_declared_evidence_kind_has_exactly_one_resolver(self):
        # A contract that names a kind the interpreter cannot resolve would fail at
        # evaluation time on a real release rather than here.
        self.assertEqual(set(self.contract.evidence_kinds), set(evidence._RESOLVERS))

    def test_every_enumerator_names_a_declared_kind_and_a_missing_message(self):
        for enumerator in self.contract.enumerators:
            self.assertIn(enumerator.evidence_kind, self.contract.evidence_kinds, msg=enumerator.id)
            self.assertTrue(enumerator.missing_message.strip(), msg=enumerator.id)

    def test_only_the_waivable_enumerators_carry_reasons(self):
        waivable = {enumerator.id for enumerator in self.contract.enumerators if enumerator.not_applicable_reasons}
        self.assertEqual(waivable, {"catalog_entry", "enable_state", "artifacts", "download_script_pin"})

    def test_a_duplicated_enumerator_id_is_rejected(self):
        document = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        document["enumerators"].append(dict(document["enumerators"][0]))
        with tempfile.TemporaryDirectory() as directory:
            path = _write_json(Path(directory), "contract.json", document)
            with self.assertRaises(ContractError) as raised:
                load_contract(path)
        self.assertIn("version", str(raised.exception))

    def test_an_empty_enumerator_list_is_rejected(self):
        document = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        document["enumerators"] = []
        with tempfile.TemporaryDirectory() as directory:
            path = _write_json(Path(directory), "contract.json", document)
            with self.assertRaises(ContractError):
                load_contract(path)

    def test_the_record_schema_does_not_restate_the_enumerator_ids(self):
        # The record schema accepts any enumerator key and defers coverage to the gate; a
        # `propertyNames`/`required` enumeration here would be a second place the set drifts.
        schema = json.loads((CONTRACT_DIR / "release-transaction-record.schema.json").read_text(encoding="utf-8"))
        enumerators = schema["properties"]["enumerators"]
        self.assertNotIn("propertyNames", enumerators)
        self.assertNotIn("required", enumerators)
        self.assertIn("$ref", enumerators["additionalProperties"])


class CompleteReleaseTests(_ReleaseCase):
    """A complete release passes; a partial one fails with the enumerator named."""

    def test_a_complete_release_passes(self):
        transaction = self._evaluate(self._record())
        self.assertEqual(transaction.verdict, "pass", msg=[r.message for r in transaction.results])
        self.assertEqual(transaction.exit_code, 0)
        self.assertEqual([result.status for result in transaction.results], [evidence.Status.SATISFIED] * len(_ENUMERATORS))

    def test_results_follow_the_contract_order(self):
        transaction = self._evaluate(self._record())
        self.assertEqual(tuple(result.enumerator_id for result in transaction.results), self.contract.enumerator_ids)

    def test_each_missing_enumerator_fails_naming_itself(self):
        for enumerator_id in _ENUMERATORS:
            with self.subTest(enumerator=enumerator_id):
                transaction = self._evaluate(self._record(drop=enumerator_id))
                result = self._result(transaction, enumerator_id)
                self.assertEqual(result.status, evidence.Status.MISSING)
                self.assertEqual(result.code, "binding-absent")
                self.assertIn(enumerator_id, result.message)
                self.assertIn("MISSING", result.message)
                self.assertEqual(transaction.verdict, "fail")
                self.assertEqual(transaction.exit_code, 1)
                self.assertIn(enumerator_id, [failure.enumerator_id for failure in transaction.failures])

    def test_an_unbound_version_also_fails_the_tag_it_anchors(self):
        # `tag` compares the tagged tree against whatever the version enumerator binds, so an
        # unbound version takes `tag` with it. Both are non-waivable, so this cascade can only
        # be reached by a record that is already failing on version.
        transaction = self._evaluate(self._record(drop="version"))
        self.assertEqual([failure.enumerator_id for failure in transaction.failures], ["version", "tag"])
        self.assertEqual(self._result(transaction, "tag").code, "manifest-binding-absent")

    def test_only_the_dropped_enumerator_fails(self):
        for enumerator_id in [name for name in _ENUMERATORS if name != "version"]:
            with self.subTest(enumerator=enumerator_id):
                transaction = self._evaluate(self._record(drop=enumerator_id))
                self.assertEqual([failure.enumerator_id for failure in transaction.failures], [enumerator_id])

    def test_the_gate_demands_whatever_the_contract_declares(self):
        # Single source of truth, proved behaviourally: an eighth enumerator added to the
        # contract is demanded of a record that was complete a moment ago.
        document = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        document["enumerators"].append(
            {
                "id": "attestation",
                "title": "Attestation",
                "participates": "a build attestation accompanies the release",
                "evidence_kind": "changelog_entry",
                "not_applicable_reasons": [],
                "missing_message": "no attestation binding",
            }
        )
        contract = load_contract(_write_json(self.root, "eighth.contract.json", document))
        transaction = self._evaluate(self._record(contract=contract), contract=contract)
        self.assertEqual([failure.enumerator_id for failure in transaction.failures], ["attestation"])
        self.assertIn("attestation", self._result(transaction, "attestation").message)

    def test_a_record_naming_an_undeclared_enumerator_is_rejected(self):
        document = self._document()
        document["enumerators"]["provenance"] = {"path": "nowhere.md"}
        with self.assertRaises(RecordError) as raised:
            self._record(document=document)
        self.assertIn("provenance", str(raised.exception))

    def test_a_binding_missing_a_required_parameter_is_rejected(self):
        with self.assertRaises(RecordError) as raised:
            self._record(bindings={"artifacts": {"manifest": "dist/manifest.json"}})
        self.assertIn("public_key", str(raised.exception))

    def test_a_binding_with_an_undeclared_parameter_is_rejected(self):
        with self.assertRaises(RecordError) as raised:
            self._record(bindings={"changelog": {"path": f"{_MODULE}/CHANGELOG.md", "grep": "1.2.3"}})
        self.assertIn("grep", str(raised.exception))


class EvidenceDriftTests(_ReleaseCase):
    """Each enumerator's evidence catches the drift it exists to catch."""

    def test_a_module_manifest_at_another_version_fails(self):
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": "1.2.2"})
        self.assertEqual(self._result(self._evaluate(self._record()), "version").code, "value-mismatch")

    def test_a_catalog_lagging_the_plugin_fails(self):
        _write_json(self.root, ".claude-plugin/marketplace.json", {"plugins": [{"name": _NAME, "version": "1.2.2"}]})
        self.assertEqual(self._result(self._evaluate(self._record()), "catalog_entry").code, "value-mismatch")

    def test_a_catalog_with_no_entry_for_the_module_fails(self):
        _write_json(self.root, ".claude-plugin/marketplace.json", {"plugins": [{"name": "other", "version": _VERSION}]})
        self.assertEqual(self._result(self._evaluate(self._record()), "catalog_entry").code, "entry-absent")

    def test_a_plugin_nobody_enables_fails(self):
        _write_json(self.root, ".claude/settings.json", {"enabledPlugins": {f"{_NAME}@{_MARKETPLACE}": False}})
        self.assertEqual(self._result(self._evaluate(self._record()), "enable_state").code, "value-mismatch")

    def test_an_absent_enable_state_key_fails(self):
        _write_json(self.root, ".claude/settings.json", {"enabledPlugins": {}})
        self.assertEqual(self._result(self._evaluate(self._record()), "enable_state").code, "key-absent")

    def test_a_tag_off_the_convention_fails(self):
        result = self._result(self._evaluate(self._record(bindings={"tag": {"tag": "release-2024-06"}})), "tag")
        self.assertEqual(result.code, "tag-off-convention")

    def test_a_conventional_tag_that_does_not_exist_fails(self):
        result = self._result(self._evaluate(self._record(bindings={"tag": {"tag": f"{_NAME}-v{_VERSION}"}})), "tag")
        self.assertEqual(result.code, "tag-absent")

    def test_a_tag_pointing_at_a_pre_bump_tree_fails(self):
        _git(self.root, "tag", "-d", f"{_MODULE}/v{_VERSION}")
        self._build_tree("1.2.2")
        _commit(self.root, "revert to 1.2.2")
        _git(self.root, "tag", f"{_MODULE}/v{_VERSION}")
        result = self._result(self._evaluate(self._record()), "tag")
        self.assertEqual(result.code, "tag-tree-mismatch")

    def test_a_pin_left_on_the_previous_version_fails(self):
        _write(self.root, "scripts/install.sh", '#!/bin/sh\nVERSION="1.2.2"\n')
        self.assertEqual(self._result(self._evaluate(self._record()), "download_script_pin").code, "value-mismatch")

    def test_a_script_pinning_two_versions_fails(self):
        _write(self.root, "scripts/install.sh", f'#!/bin/sh\nVERSION="{_VERSION}"\nFALLBACK_VERSION="1.2.2"\n')
        self.assertEqual(self._result(self._evaluate(self._record()), "download_script_pin").code, "pin-disagreement")

    def test_a_changelog_without_the_released_version_fails(self):
        _write(self.root, f"{_MODULE}/CHANGELOG.md", "# Changelog\n\n## 1.2.2\n\n- previous\n")
        self.assertEqual(self._result(self._evaluate(self._record()), "changelog").code, "entry-absent")

    def test_an_absent_evidence_file_fails_rather_than_passing(self):
        (self.root / ".claude-plugin" / "marketplace.json").unlink()
        self.assertEqual(self._result(self._evaluate(self._record()), "catalog_entry").code, "file-absent")


class WaiverTests(_ReleaseCase):
    """A not-applicable enumerator is declared, never inferred."""

    _WAIVER = {"not_applicable": {"reason": "no-catalog", "detail": "Released as a library; no catalog publishes it."}}

    def test_a_declared_waiver_with_an_allowed_reason_passes(self):
        transaction = self._evaluate(self._record(bindings={"catalog_entry": self._WAIVER}))
        self.assertEqual(self._result(transaction, "catalog_entry").status, evidence.Status.NOT_APPLICABLE)
        self.assertEqual(transaction.verdict, "pass")

    def test_a_reason_the_contract_does_not_allow_here_fails(self):
        binding = {"not_applicable": {"reason": "no-release-archive", "detail": "Borrowed from another enumerator."}}
        result = self._result(self._evaluate(self._record(bindings={"catalog_entry": binding})), "catalog_entry")
        self.assertEqual(result.code, "waiver-not-declared")
        self.assertEqual(result.status, evidence.Status.UNSATISFIED)

    def test_a_non_waivable_enumerator_cannot_be_waived(self):
        for enumerator_id in ("version", "tag", "changelog"):
            with self.subTest(enumerator=enumerator_id):
                binding = {"not_applicable": {"reason": "no-catalog", "detail": "Arguing it away."}}
                result = self._result(self._evaluate(self._record(bindings={enumerator_id: binding})), enumerator_id)
                self.assertEqual(result.code, "waiver-not-permitted")
                self.assertEqual(result.status, evidence.Status.UNSATISFIED)

    def test_a_waiver_mixed_with_evidence_is_rejected(self):
        binding = {"not_applicable": {"reason": "no-catalog", "detail": "Both at once."}, "path": ".claude-plugin/marketplace.json"}
        with self.assertRaises(RecordError):
            self._record(bindings={"catalog_entry": binding})

    def test_a_waiver_without_a_human_statement_is_rejected(self):
        with self.assertRaises(RecordError):
            self._record(bindings={"catalog_entry": {"not_applicable": {"reason": "no-catalog", "detail": "  "}}})


class ManifestTests(unittest.TestCase):
    """One rendering pass, and a signature or nothing."""

    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.artifact = self.root / f"{_NAME}-{_ARCH}"
        self.artifact.write_bytes(_PAYLOAD)
        self.entry = provisioning.Artifact.from_path(_ARCH, self.artifact)
        self.manifest = self.root / "manifest.json"
        self.manifest.write_text(provisioning.render_manifest(_VERSION, [self.entry]), encoding="utf-8")
        self.signature = self.root / "manifest.json.sig"
        self.signature.write_text("detached-signature\n", encoding="utf-8")
        self.public_key = self.root / "pubkey.pem"
        self.public_key.write_text(_STUB_KEY, encoding="utf-8")

    def test_rendering_is_one_deterministic_pass(self):
        other = provisioning.Artifact(arch="darwin-aarch64", filename="demo-darwin-aarch64", sha256="a" * 64, size=7)
        forward = provisioning.render_manifest(_VERSION, [self.entry, other])
        reversed_order = provisioning.render_manifest(_VERSION, [other, self.entry])
        self.assertEqual(forward, reversed_order)
        self.assertTrue(forward.endswith("}\n"))
        self.assertTrue(provisioning.is_canonical(forward))

    def test_a_post_edited_manifest_is_not_canonical(self):
        document = json.loads(self.manifest.read_text(encoding="utf-8"))
        self.assertFalse(provisioning.is_canonical(json.dumps(document, indent=4) + "\n"))
        self.assertFalse(provisioning.is_canonical(self.manifest.read_text(encoding="utf-8") + "\n"))

    def test_a_missing_signature_discards_the_manifest(self):
        self.signature.unlink()
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_accepts)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.DISCARDED)
        self.assertEqual(verification.code, "signature-missing")
        self.assertIsNone(verification.manifest)

    def test_an_invalid_signature_discards_the_manifest(self):
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_rejects)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.DISCARDED)
        self.assertEqual(verification.code, "signature-invalid")
        self.assertIsNone(verification.manifest)

    def test_a_missing_signature_discards_even_where_no_verifier_exists(self):
        # Host tooling cannot excuse a release that published no signature.
        self.signature.unlink()
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_cannot_verify)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.DISCARDED)
        self.assertEqual(verification.code, "signature-missing")

    def test_a_host_with_no_verifier_is_unverifiable(self):
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_cannot_verify)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.UNVERIFIABLE)
        self.assertEqual(verification.code, "signature-unverifiable")
        self.assertIsNone(verification.manifest)

    def test_an_absent_public_key_is_named_apart_from_an_absent_verifier(self):
        self.public_key.unlink()
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_accepts)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.UNVERIFIABLE)
        self.assertEqual(verification.code, "public-key-absent")
        self.assertIsNone(verification.manifest)

    def test_an_authentic_manifest_that_is_not_manifest_shaped_is_discarded(self):
        self.manifest.write_text('{"version": "1.2.3"}\n', encoding="utf-8")
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key, verifier=_accepts)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.DISCARDED)
        self.assertEqual(verification.code, "manifest-unparsable")

    def test_artifact_bytes_are_checked_against_the_recorded_digest_and_size(self):
        self.assertIsNone(provisioning.check_artifact(self.artifact, self.entry))
        self.artifact.write_bytes(_PAYLOAD + b"tampered\n")
        self.assertIsNotNone(provisioning.check_artifact(self.artifact, self.entry))
        self.artifact.unlink()
        self.assertIn("not present", provisioning.check_artifact(self.artifact, self.entry) or "")

    def test_a_plaintext_transport_is_refused(self):
        self.assertIsNotNone(provisioning.fetch("http://example.invalid/manifest.json", self.root / "fetched.json"))

    @unittest.skipUnless(shutil.which("openssl"), "openssl is not installed on this host")
    def test_openssl_round_trip_verifies_and_rejects(self):
        private_key = self.root / "key.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(private_key)], check=True, capture_output=True)
        subprocess.run(["openssl", "pkey", "-in", str(private_key), "-pubout", "-out", str(self.public_key)], check=True, capture_output=True)
        subprocess.run(
            ["openssl", "pkeyutl", "-sign", "-inkey", str(private_key), "-rawin", "-in", str(self.manifest), "-out", str(self.signature)],
            check=True,
            capture_output=True,
        )
        verification = provisioning.verify_manifest(self.manifest, self.signature, self.public_key)
        self.assertEqual(verification.verdict, provisioning.SignatureVerdict.VERIFIED, msg=verification.detail)
        self.assertIsNotNone(verification.manifest)

        self.manifest.write_text(provisioning.render_manifest("9.9.9", [self.entry]), encoding="utf-8")
        tampered = provisioning.verify_manifest(self.manifest, self.signature, self.public_key)
        self.assertEqual(tampered.verdict, provisioning.SignatureVerdict.DISCARDED)
        self.assertEqual(tampered.code, "signature-invalid")


class ArtifactEnumeratorTests(_ReleaseCase):
    """The gate's artifact evidence, where "cannot verify" fails rather than falls open."""

    def test_a_missing_signature_fails_the_gate_even_though_every_digest_matches(self):
        # The checksum-only path does not exist: the digests here are correct and the
        # artifact is present, and the enumerator still fails on the absent signature.
        (self.root / "dist" / "manifest.json.sig").unlink()
        transaction = self._evaluate(self._record())
        result = self._result(transaction, "artifacts")
        self.assertEqual(result.code, "signature-missing")
        self.assertEqual(transaction.exit_code, 1)

    def test_an_invalid_signature_fails_the_gate(self):
        result = self._result(self._evaluate(self._record(), verifier=_rejects), "artifacts")
        self.assertEqual(result.code, "signature-invalid")

    def test_a_host_that_cannot_verify_fails_the_gate(self):
        result = self._result(self._evaluate(self._record(), verifier=_cannot_verify), "artifacts")
        self.assertEqual(result.code, "signature-unverifiable")
        self.assertEqual(result.status, evidence.Status.UNSATISFIED)

    def test_a_post_edited_manifest_fails_the_gate(self):
        document = json.loads((self.root / "dist" / "manifest.json").read_text(encoding="utf-8"))
        _write(self.root, "dist/manifest.json", json.dumps(document, indent=4) + "\n")
        self.assertEqual(self._result(self._evaluate(self._record()), "artifacts").code, "manifest-non-canonical")

    def test_a_manifest_at_another_version_fails(self):
        artifact = self.root / "dist" / f"{_NAME}-{_ARCH}"
        _write(self.root, "dist/manifest.json", provisioning.render_manifest("1.2.2", [provisioning.Artifact.from_path(_ARCH, artifact)]))
        self.assertEqual(self._result(self._evaluate(self._record()), "artifacts").code, "version-mismatch")

    def test_a_tampered_artifact_fails(self):
        (self.root / "dist" / f"{_NAME}-{_ARCH}").write_bytes(_PAYLOAD + b"tampered\n")
        self.assertEqual(self._result(self._evaluate(self._record()), "artifacts").code, "artifact-mismatch")

    def test_a_required_arch_with_no_entry_fails(self):
        bindings = {"artifacts": {"manifest": "dist/manifest.json", "public_key": "release/pubkey.pem", "require_arches": ["darwin-aarch64"]}}
        self.assertEqual(self._result(self._evaluate(self._record(bindings=bindings)), "artifacts").code, "arch-absent")


class LadderTests(unittest.TestCase):
    """The provisioning ladder: cache re-verified in place, current release before frozen."""

    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.cache = self.root / "cache"
        self.cache.mkdir()
        self.public_key = self.root / "pubkey.pem"
        self.public_key.write_text(_STUB_KEY, encoding="utf-8")

    def _provision(self, *, base_url: str | None = None, frozen_dir: Path | None = None, verifier=_accepts):
        return provisioning.provision(
            name=_NAME,
            version=_VERSION,
            arch=_ARCH,
            cache_dir=self.cache,
            public_key=self.public_key,
            release_base_url=base_url,
            frozen_dir=frozen_dir,
            verifier=verifier,
        )

    def _seed_cache(self, payload: bytes, *, digest: str | None = None) -> Path:
        cached = self.cache / f"{_NAME}-{_VERSION}"
        cached.write_bytes(payload)
        Path(f"{cached}.sha256").write_text(f"{digest or provisioning.sha256_of(cached)}\n", encoding="utf-8")
        return cached

    def _rung(self, result, rung: provisioning.Rung):
        matches = [attempt for attempt in result.attempts if attempt.rung is rung]
        return matches[0] if matches else None

    def test_a_version_keyed_cache_entry_is_reverified_in_place(self):
        cached = self._seed_cache(_PAYLOAD)
        result = self._provision(base_url="https://example.invalid/releases")
        self.assertTrue(result.provisioned)
        self.assertIs(result.rung, provisioning.Rung.CACHE)
        self.assertEqual(result.path, cached)
        self.assertEqual(result.sha256, provisioning.sha256_of(cached))
        # No network rung was even attempted: the cache hit is decided with no fetch.
        self.assertIsNone(self._rung(result, provisioning.Rung.RELEASE))

    def test_a_cache_entry_failing_reverification_is_evicted(self):
        cached = self._seed_cache(_PAYLOAD, digest="0" * 64)
        result = self._provision()
        self.assertFalse(cached.exists())
        self.assertFalse(Path(f"{cached}.sha256").exists())
        self.assertIn("evicted", self._rung(result, provisioning.Rung.CACHE).detail)

    def test_a_cache_entry_without_a_sidecar_is_evicted(self):
        cached = self.cache / f"{_NAME}-{_VERSION}"
        cached.write_bytes(_PAYLOAD)
        self._provision()
        self.assertFalse(cached.exists())

    def test_the_current_release_is_preferred_over_a_commit_frozen_copy(self):
        base_url = _publish(self.root / "releases", name=_NAME, version=_VERSION, arch=_ARCH, payload=_PAYLOAD)
        frozen = _freeze(self.root / "frozen", name=_NAME, arch=_ARCH, payload=_FROZEN_PAYLOAD)
        result = self._provision(base_url=base_url, frozen_dir=frozen.parent)
        self.assertIs(result.rung, provisioning.Rung.RELEASE)
        self.assertEqual(result.path.read_bytes(), _PAYLOAD)
        self.assertIsNone(self._rung(result, provisioning.Rung.FROZEN))

    def test_a_resolved_release_is_cached_with_its_sidecar(self):
        base_url = _publish(self.root / "releases", name=_NAME, version=_VERSION, arch=_ARCH, payload=_PAYLOAD)
        result = self._provision(base_url=base_url)
        sidecar = Path(f"{result.path}.sha256")
        self.assertTrue(sidecar.is_file())
        self.assertEqual(sidecar.read_text(encoding="utf-8").split()[0], provisioning.sha256_of(result.path))

    def test_a_discarded_release_manifest_stops_the_ladder(self):
        base_url = _publish(self.root / "releases", name=_NAME, version=_VERSION, arch=_ARCH, payload=_PAYLOAD)
        frozen = _freeze(self.root / "frozen", name=_NAME, arch=_ARCH, payload=_FROZEN_PAYLOAD)
        result = self._provision(base_url=base_url, frozen_dir=frozen.parent, verifier=_rejects)
        self.assertFalse(result.provisioned)
        self.assertIs(result.rung, provisioning.Rung.NONE)
        self.assertEqual(self._rung(result, provisioning.Rung.RELEASE).outcome, "discarded")
        self.assertIsNone(self._rung(result, provisioning.Rung.FROZEN))

    def test_an_unverifiable_release_walks_on_to_the_frozen_copy(self):
        base_url = _publish(self.root / "releases", name=_NAME, version=_VERSION, arch=_ARCH, payload=_PAYLOAD)
        frozen = _freeze(self.root / "frozen", name=_NAME, arch=_ARCH, payload=_FROZEN_PAYLOAD)
        result = self._provision(base_url=base_url, frozen_dir=frozen.parent, verifier=_cannot_verify)
        self.assertIs(result.rung, provisioning.Rung.FROZEN)
        self.assertEqual(result.path.read_bytes(), _FROZEN_PAYLOAD)

    def test_an_unreachable_release_walks_on_to_the_frozen_copy(self):
        frozen = _freeze(self.root / "frozen", name=_NAME, arch=_ARCH, payload=_FROZEN_PAYLOAD)
        result = self._provision(base_url=(self.root / "no-releases").as_uri(), frozen_dir=frozen.parent)
        self.assertIs(result.rung, provisioning.Rung.FROZEN)

    def test_a_frozen_copy_without_a_sidecar_is_rejected(self):
        frozen_dir = self.root / "frozen"
        frozen_dir.mkdir()
        (frozen_dir / f"{_NAME}-{_ARCH}").write_bytes(_FROZEN_PAYLOAD)
        result = self._provision(frozen_dir=frozen_dir)
        self.assertFalse(result.provisioned)
        self.assertEqual(self._rung(result, provisioning.Rung.FROZEN).outcome, "rejected")

    def test_an_unmaterialized_large_file_pointer_counts_as_absent(self):
        frozen_dir = self.root / "frozen"
        frozen_dir.mkdir()
        pointer = frozen_dir / f"{_NAME}-{_ARCH}"
        pointer.write_bytes(b"version https://git-lfs.github.com/spec/v1\noid sha256:" + b"0" * 64 + b"\nsize 12\n")
        Path(f"{pointer}.sha256").write_text(f"{provisioning.sha256_of(pointer)}\n", encoding="utf-8")
        result = self._provision(frozen_dir=frozen_dir)
        self.assertFalse(result.provisioned)
        self.assertEqual(self._rung(result, provisioning.Rung.FROZEN).outcome, "absent")

    def test_nothing_available_resolves_nothing(self):
        result = self._provision()
        self.assertFalse(result.provisioned)
        self.assertIs(result.rung, provisioning.Rung.NONE)


class ChangedModuleTests(unittest.TestCase):
    """No behaviour change ships without a version bump and a published release."""

    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.contract = load_contract()
        _init_repo(self.root)
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": _VERSION})
        _write(self.root, f"{_MODULE}/README.md", "# demo\n")
        _write(self.root, ".github/workflows/ci.yml", "name: CI\n")
        _write(self.root, "notes.md", "scratch\n")
        self.base = _commit(self.root, "base")

    def _check(self):
        return changed_gate.check_changed(self.contract, self.root, self.base, "HEAD")

    def _bump(self, version: str) -> None:
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": version})

    def test_a_change_with_no_version_bump_fails(self):
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        _commit(self.root, "change behaviour")
        report = self._check()
        self.assertEqual(report.exit_code, 1)
        self.assertEqual([verdict.status for verdict in report.verdicts], ["no-version-bump"])
        self.assertIn(_MODULE, report.verdicts[0].detail)

    def test_a_bump_with_no_published_release_fails(self):
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        self._bump("1.3.0")
        _commit(self.root, "bump but do not release")
        report = self._check()
        self.assertEqual(report.exit_code, 1)
        self.assertEqual([verdict.status for verdict in report.verdicts], ["no-published-release"])
        self.assertIn(f"{_MODULE}/v1.3.0", report.verdicts[0].detail)

    def test_a_bump_with_a_release_tag_passes(self):
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        self._bump("1.3.0")
        _commit(self.root, "release 1.3.0")
        _git(self.root, "tag", f"{_MODULE}/v1.3.0")
        report = self._check()
        self.assertEqual(report.exit_code, 0)
        self.assertEqual([verdict.status for verdict in report.verdicts], ["released"])

    def test_a_version_that_does_not_increase_fails(self):
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        self._bump("1.2.2")
        _commit(self.root, "walk the version backwards")
        self.assertEqual([verdict.status for verdict in self._check().verdicts], ["version-not-increased"])

    def test_a_prerelease_suffix_alone_is_not_a_bump(self):
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        self._bump(f"{_VERSION}-rc1")
        _commit(self.root, "suffix only")
        self.assertEqual([verdict.status for verdict in self._check().verdicts], ["version-not-increased"])

    def test_repository_infrastructure_ships_in_no_release(self):
        _write(self.root, ".github/workflows/ci.yml", "name: CI\n\n# tweak\n")
        _commit(self.root, "ci tweak")
        report = self._check()
        self.assertEqual(report.exit_code, 0)
        self.assertEqual(report.verdicts, ())
        self.assertIn(".github/workflows/ci.yml", report.exempt)

    def test_a_path_under_no_module_belongs_to_no_release(self):
        _write(self.root, "notes.md", "scratch\n\nmore\n")
        _commit(self.root, "notes")
        report = self._check()
        self.assertEqual(report.exit_code, 0)
        self.assertEqual(report.verdicts, ())
        self.assertIn("notes.md", report.unowned)

    def test_a_tag_versioned_module_with_no_tag_after_the_base_fails(self):
        _write_json(self.root, "schemas/thing/thing.schema.json", {"title": "Thing"})
        _commit(self.root, "add a contract module")
        report = self._check()
        self.assertEqual(report.exit_code, 1)
        verdict = report.verdicts[0]
        self.assertEqual(verdict.module.path, "schemas/thing")
        self.assertEqual(verdict.status, "no-published-release")

    def test_a_tag_versioned_module_released_after_the_base_passes(self):
        _write_json(self.root, "schemas/thing/thing.schema.json", {"title": "Thing"})
        _commit(self.root, "add a contract module")
        _git(self.root, "tag", "schemas/thing/v1.0.0")
        self.assertEqual(self._check().exit_code, 0)

    def test_a_base_ref_that_does_not_resolve_is_an_error(self):
        with self.assertRaises(changed_gate.ChangedError):
            changed_gate.check_changed(self.contract, self.root, "refs/heads/nonexistent", "HEAD")

    def test_a_directory_that_is_not_a_repository_is_an_error(self):
        outside = tempfile.TemporaryDirectory()
        self.addCleanup(outside.cleanup)
        with self.assertRaises(changed_gate.ChangedError):
            changed_gate.check_changed(self.contract, Path(outside.name), self.base, "HEAD")


class ReleasePauseTests(_ReleaseCase):
    """An open below-floor compliance defect pauses the owner's next release."""

    def _register(self, **overrides) -> Path:
        entry = {
            "defect_id": "FB42",
            "owner": _NAME,
            "owner_kind": "plugin",
            "invariant_id": "INV-R4-007",
            "status": "open",
            "model": "opus-5",
            "declared_floor": 0.9,
            "measured_rate": 0.62,
        }
        entry.update(overrides)
        return _write_json(self.root, "release-pause-register.json", {"schema": "release-pause-register@1.0.0", "entries": [entry]})

    def test_the_hook_names_the_consumer_that_writes_the_register(self):
        hook = self.contract.hooks["release_pause"]
        self.assertIn("M10.P2.T3", hook["written_by"])
        self.assertEqual(hook["owner_kinds"], ["plugin", "cli"])
        self.assertIn("M10.P2.T3", self._evaluate(self._record(), pause_register=None).written_by)

    def test_an_open_defect_pauses_an_otherwise_complete_release(self):
        transaction = self._evaluate(self._record(), pause_register=self._register())
        self.assertEqual(transaction.failures, ())
        self.assertEqual(transaction.verdict, "paused")
        self.assertEqual(transaction.exit_code, 4)
        self.assertEqual(len(transaction.pauses), 1)
        self.assertIn("FB42", transaction.pauses[0].message)
        self.assertIn("INV-R4-007", transaction.pauses[0].message)

    def test_an_absent_register_pauses_nothing(self):
        transaction = self._evaluate(self._record(), pause_register=self.root / "no-such-register.json")
        self.assertEqual(transaction.verdict, "pass")

    def test_a_resolved_or_unmeasured_entry_pauses_nothing(self):
        for status in ("resolved", "declared-unmeasured"):
            with self.subTest(status=status):
                transaction = self._evaluate(self._record(), pause_register=self._register(status=status))
                self.assertEqual(transaction.verdict, "pass")

    def test_a_defect_against_another_module_does_not_pause_this_release(self):
        transaction = self._evaluate(self._record(), pause_register=self._register(owner="other-plugin"))
        self.assertEqual(transaction.verdict, "pass")

    def test_a_partial_release_reports_the_partial_rather_than_the_pause(self):
        transaction = self._evaluate(self._record(drop="changelog"), pause_register=self._register())
        self.assertEqual(transaction.verdict, "fail")
        self.assertEqual(transaction.exit_code, 1)

    def test_an_unreadable_register_is_an_error_not_an_absence_of_defects(self):
        _write(self.root, "release-pause-register.json", "{not json\n")
        with self.assertRaises(RecordError):
            self._evaluate(self._record(), pause_register=self.root / "release-pause-register.json")

    def test_an_entry_missing_a_required_field_is_an_error(self):
        register = _write_json(self.root, "release-pause-register.json", {"schema": "release-pause-register@1.0.0", "entries": [{"owner": _NAME}]})
        with self.assertRaises(RecordError):
            self._evaluate(self._record(), pause_register=register)


if __name__ == "__main__":
    unittest.main(verbosity=2)
