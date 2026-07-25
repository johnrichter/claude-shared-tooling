#!/usr/bin/env python3
"""CLI-level regression for tooling/release-transaction/check.py — invokes the gate as a
SUBPROCESS against throwaway trees, exercising the exact path CI hits.

Complements test_release_transaction.py's import-based tests by proving the argv, exit-code,
and output contract the contract declares: 0 complete, 1 partial, 2 malformed, 3 nothing
provisioned, 4 paused. A partial release must name the missing enumerator on stdout, since
that message is the whole diagnostic a release engineer gets from CI.
"""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_CHECK = Path(__file__).resolve().parent.parent / "tooling" / "release-transaction" / "check.py"

_NAME = "demo"
_MODULE = "plugins/demo"
_MARKETPLACE = "ts-ai"
_VERSION = "1.2.3"
_ARCH = "linux-x86_64"
_PAYLOAD = b"#!/bin/sh\necho demo\n"
_NO_ARCHIVE = {"not_applicable": {"reason": "no-release-archive", "detail": "Prose and hooks only; consumers install it from the catalog."}}


def _git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True)
    return result.stdout.strip()


def _write(root: Path, relative: str, text: str) -> Path:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def _write_json(root: Path, relative: str, document: object) -> Path:
    return _write(root, relative, json.dumps(document, indent=2) + "\n")


def _run(*args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
    return subprocess.run([sys.executable, str(_CHECK), *args], capture_output=True, text=True, check=False, env=env)


class _CliCase(unittest.TestCase):
    """A committed, tagged `demo` plugin release on disk, plus its record."""

    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        _git(self.root, "init", "-q")
        _git(self.root, "config", "user.email", "test@example.com")
        _git(self.root, "config", "user.name", "Test")
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": _VERSION})
        _write(self.root, f"{_MODULE}/CHANGELOG.md", f"# Changelog\n\n## {_VERSION}\n")
        _write_json(self.root, ".claude-plugin/marketplace.json", {"plugins": [{"name": _NAME, "version": _VERSION}]})
        _write_json(self.root, ".claude/settings.json", {"enabledPlugins": {f"{_NAME}@{_MARKETPLACE}": True}})
        _write(self.root, "scripts/install.sh", f'#!/bin/sh\nVERSION="{_VERSION}"\n')
        _git(self.root, "add", "-A")
        _git(self.root, "commit", "-q", "-m", f"release {_NAME} {_VERSION}")
        _git(self.root, "tag", f"{_MODULE}/v{_VERSION}")
        self.record = self._record()

    def _document(self) -> dict:
        return {
            "schema": "release-transaction-record@1.0.0",
            "contract": "release-transaction@1.0.0",
            "subject": {"name": _NAME, "kind": "plugin", "repo": ".", "module_path": _MODULE, "marketplace": _MARKETPLACE},
            "version": _VERSION,
            "enumerators": {
                "version": {"path": f"{_MODULE}/.claude-plugin/plugin.json", "pointer": "/version"},
                "catalog_entry": {"path": ".claude-plugin/marketplace.json"},
                "enable_state": {"path": ".claude/settings.json"},
                "tag": {"tag": f"{_MODULE}/v{_VERSION}"},
                "artifacts": dict(_NO_ARCHIVE),
                "download_script_pin": {"path": "scripts/install.sh", "pattern": 'VERSION="([^"]+)"'},
                "changelog": {"path": f"{_MODULE}/CHANGELOG.md"},
            },
        }

    def _record(self, *, drop: str | None = None, bindings: dict | None = None) -> Path:
        document = self._document()
        if drop is not None:
            del document["enumerators"][drop]
        for enumerator_id, binding in (bindings or {}).items():
            document["enumerators"][enumerator_id] = binding
        return _write_json(self.root, "release.json", document)

    def _verify(self, record: Path | None = None, *args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
        return _run("verify-release", "--record", str(record or self.record), "--root", str(self.root), *args, env=env)


class VerifyReleaseCliTests(_CliCase):
    def test_a_complete_release_exits_zero(self):
        result = self._verify()
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)
        self.assertIn("complete transaction", result.stdout)

    def test_each_missing_enumerator_is_named_on_stdout(self):
        for enumerator_id in ("version", "catalog_entry", "enable_state", "tag", "artifacts", "download_script_pin", "changelog"):
            with self.subTest(enumerator=enumerator_id):
                result = self._verify(self._record(drop=enumerator_id))
                self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
                self.assertIn(f"enumerator '{enumerator_id}' MISSING", result.stdout)
                self.assertIn("partial release", result.stdout)

    def test_json_output_carries_the_verdict_and_every_enumerator(self):
        result = self._verify(self._record(drop="changelog"), "--json")
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        document = json.loads(result.stdout)
        self.assertEqual(document["verdict"], "fail")
        self.assertEqual(document["exit_code"], 1)
        statuses = {entry["enumerator"]: entry["status"] for entry in document["results"]}
        self.assertEqual(len(statuses), 7)
        self.assertEqual(statuses["changelog"], "missing")

    def test_evidence_that_does_not_hold_names_the_enumerator_and_its_failure_code(self):
        _write_json(self.root, ".claude-plugin/marketplace.json", {"plugins": [{"name": _NAME, "version": "1.2.2"}]})
        result = self._verify()
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("enumerator 'catalog_entry' UNSATISFIED - value-mismatch", result.stdout)

    def test_a_malformed_record_is_a_usage_error(self):
        document = self._document()
        document["schema"] = "release-transaction-record@0.9.0"
        result = self._verify(_write_json(self.root, "bad.json", document))
        self.assertEqual(result.returncode, 2, msg=result.stdout + result.stderr)
        self.assertIn("release-transaction:", result.stderr)

    def test_an_absent_record_is_a_usage_error(self):
        result = self._verify(self.root / "nowhere.json")
        self.assertEqual(result.returncode, 2, msg=result.stdout + result.stderr)

    def test_an_open_compliance_defect_exits_four(self):
        register = _write_json(
            self.root,
            "release-pause-register.json",
            {
                "schema": "release-pause-register@1.0.0",
                "entries": [{"defect_id": "FB42", "owner": _NAME, "owner_kind": "plugin", "invariant_id": "INV-R4-007", "status": "open"}],
            },
        )
        result = self._verify(None, "--pause-register", str(register))
        self.assertEqual(result.returncode, 4, msg=result.stdout + result.stderr)
        self.assertIn("FB42", result.stdout)
        self.assertIn("M10.P2.T3", result.stdout)

    @unittest.skipUnless(shutil.which("openssl"), "openssl is not installed on this host")
    def test_a_signed_release_verifies_end_to_end(self):
        artifact = self.root / "dist" / f"{_NAME}-{_ARCH}"
        artifact.parent.mkdir(parents=True, exist_ok=True)
        artifact.write_bytes(_PAYLOAD)
        rendered = _run("render-manifest", "--version", _VERSION, "--artifact", f"{_ARCH}={artifact}", "--out", str(self.root / "dist" / "manifest.json"))
        self.assertEqual(rendered.returncode, 0, msg=rendered.stdout + rendered.stderr)

        private_key = self.root / "release" / "key.pem"
        private_key.parent.mkdir(parents=True, exist_ok=True)
        public_key = self.root / "release" / "pubkey.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(private_key)], check=True, capture_output=True)
        subprocess.run(["openssl", "pkey", "-in", str(private_key), "-pubout", "-out", str(public_key)], check=True, capture_output=True)
        subprocess.run(
            [
                "openssl", "pkeyutl", "-sign", "-inkey", str(private_key), "-rawin",
                "-in", str(self.root / "dist" / "manifest.json"),
                "-out", str(self.root / "dist" / "manifest.json.sig"),
            ],
            check=True,
            capture_output=True,
        )

        standalone = _run(
            "verify-manifest",
            "--manifest", str(self.root / "dist" / "manifest.json"),
            "--public-key", str(public_key),
            "--artifact-dir", str(self.root / "dist"),
            "--version", _VERSION,
        )
        self.assertEqual(standalone.returncode, 0, msg=standalone.stdout + standalone.stderr)

        bindings = {"artifacts": {"manifest": "dist/manifest.json", "public_key": "release/pubkey.pem"}}
        result = self._verify(self._record(bindings=bindings))
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)
        self.assertIn("enumerator 'artifacts' satisfied", result.stdout)

    @unittest.skipUnless(shutil.which("openssl"), "openssl is not installed on this host")
    def test_an_unsigned_release_fails_the_gate(self):
        artifact = self.root / "dist" / f"{_NAME}-{_ARCH}"
        artifact.parent.mkdir(parents=True, exist_ok=True)
        artifact.write_bytes(_PAYLOAD)
        _run("render-manifest", "--version", _VERSION, "--artifact", f"{_ARCH}={artifact}", "--out", str(self.root / "dist" / "manifest.json"))
        _write(self.root, "release/pubkey.pem", "-----BEGIN PUBLIC KEY-----\nstub\n-----END PUBLIC KEY-----\n")
        bindings = {"artifacts": {"manifest": "dist/manifest.json", "public_key": "release/pubkey.pem"}}
        result = self._verify(self._record(bindings=bindings))
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("signature-missing", result.stdout)


class ChangedCliTests(_CliCase):
    def test_a_change_with_no_version_bump_exits_one(self):
        base = _git(self.root, "rev-parse", "HEAD")
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        _git(self.root, "add", "-A")
        _git(self.root, "commit", "-q", "-m", "change behaviour")
        result = _run("changed", "--base", base, "--repo", str(self.root))
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("no-version-bump", result.stdout)
        self.assertIn(_MODULE, result.stdout)

    def test_a_bumped_and_tagged_change_exits_zero(self):
        base = _git(self.root, "rev-parse", "HEAD")
        _write(self.root, f"{_MODULE}/README.md", "# demo\n\nNew behaviour.\n")
        _write_json(self.root, f"{_MODULE}/.claude-plugin/plugin.json", {"name": _NAME, "version": "1.3.0"})
        _git(self.root, "add", "-A")
        _git(self.root, "commit", "-q", "-m", "release 1.3.0")
        _git(self.root, "tag", f"{_MODULE}/v1.3.0")
        result = _run("changed", "--base", base, "--repo", str(self.root), "--json")
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)
        self.assertEqual([module["status"] for module in json.loads(result.stdout)["modules"]], ["released"])

    def test_a_base_ref_that_does_not_resolve_is_a_usage_error(self):
        result = _run("changed", "--base", "refs/heads/nonexistent", "--repo", str(self.root))
        self.assertEqual(result.returncode, 2, msg=result.stdout + result.stderr)


class ManifestCliTests(unittest.TestCase):
    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.artifact = self.root / f"{_NAME}-{_ARCH}"
        self.artifact.write_bytes(_PAYLOAD)

    def _render(self) -> str:
        result = _run("render-manifest", "--version", _VERSION, "--artifact", f"{_ARCH}={self.artifact}")
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        return result.stdout

    def test_two_renderings_of_the_same_inputs_are_byte_identical(self):
        self.assertEqual(self._render(), self._render())

    def test_an_artifact_spec_that_is_not_arch_equals_path_is_a_usage_error(self):
        result = _run("render-manifest", "--version", _VERSION, "--artifact", "no-separator")
        self.assertEqual(result.returncode, 2, msg=result.stdout + result.stderr)

    def test_a_manifest_with_no_signature_is_discarded(self):
        manifest = _write(self.root, "manifest.json", self._render())
        public_key = _write(self.root, "pubkey.pem", "-----BEGIN PUBLIC KEY-----\nstub\n-----END PUBLIC KEY-----\n")
        result = _run("verify-manifest", "--manifest", str(manifest), "--public-key", str(public_key))
        self.assertEqual(result.returncode, 1, msg=result.stdout + result.stderr)
        self.assertIn("discarded", result.stdout)
        self.assertIn("signature-missing", result.stdout)

    def test_a_host_with_no_verifier_reports_unverifiable(self):
        # PATH emptied so no `openssl` is findable: the runtime direction is to resolve
        # nothing (exit 3) and let the caller fall open to the raw OS tool.
        manifest = _write(self.root, "manifest.json", self._render())
        _write(self.root, "manifest.json.sig", "detached-signature\n")
        public_key = _write(self.root, "pubkey.pem", "-----BEGIN PUBLIC KEY-----\nstub\n-----END PUBLIC KEY-----\n")
        env = {**os.environ, "PATH": ""}
        result = _run("verify-manifest", "--manifest", str(manifest), "--public-key", str(public_key), env=env)
        self.assertEqual(result.returncode, 3, msg=result.stdout + result.stderr)
        self.assertIn("unverifiable", result.stdout)


class ProvisionCliTests(unittest.TestCase):
    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()

    def test_nothing_provisionable_exits_three(self):
        result = _run(
            "provision",
            "--name", _NAME,
            "--version", _VERSION,
            "--arch", _ARCH,
            "--cache-dir", str(self.root / "cache"),
            "--public-key", str(self.root / "pubkey.pem"),
        )
        self.assertEqual(result.returncode, 3, msg=result.stdout + result.stderr)
        self.assertIn("fail open to the raw OS tool", result.stderr)

    def test_a_version_keyed_cache_entry_is_reverified_and_returned(self):
        cache = self.root / "cache"
        cache.mkdir()
        cached = cache / f"{_NAME}-{_VERSION}"
        cached.write_bytes(_PAYLOAD)
        digest = subprocess.run([sys.executable, "-c", "import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())", str(cached)], check=True, capture_output=True, text=True).stdout.strip()
        Path(f"{cached}.sha256").write_text(f"{digest}\n", encoding="utf-8")
        result = _run(
            "provision",
            "--name", _NAME,
            "--version", _VERSION,
            "--arch", _ARCH,
            "--cache-dir", str(cache),
            "--public-key", str(self.root / "pubkey.pem"),
        )
        self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), str(cached))
        self.assertIn("re-verified in place", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
