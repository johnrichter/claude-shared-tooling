"""Release manifests, signature verification, and the provisioning ladder.

Three properties this module exists to hold:

* **One rendering pass.** A manifest's bytes come from a single serialization of the
  assembled record. Nothing appends, reformats, or patches afterward, so identical
  inputs give identical bytes -- and a manifest whose bytes differ from the canonical
  rendering of their own content is provably not the product of one pass.
* **A signature or nothing.** A missing or invalid detached signature discards the
  manifest outright. There is no digest-only path: a digest list is worth exactly as
  much as the signature over it. A host with no verifier reports `UNVERIFIABLE`, and
  its caller falls open to the raw OS tool rather than to unverified bytes.
* **Current release over frozen copy.** The ladder re-verifies a version-keyed cache in
  place first, then the current release, and only then a copy frozen into a commit -- so
  a host that can reach the release host never runs last commit's binary.
"""
from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import urllib.error
import urllib.request
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any

DEFAULT_RELEASE_DIR_TEMPLATE = "{name}-v{version}"
DEFAULT_MAX_ARTIFACT_BYTES = 256 * 1024 * 1024
DEFAULT_TIMEOUT_SECONDS = 30
_ALLOWED_URL_SCHEMES = ("https://", "file://")
_HASH_CHUNK = 1024 * 1024
_LFS_POINTER_MARKERS = (b"version https://git-lfs", b"oid sha256:")
_SNIFF_BYTES = 200


class ManifestError(ValueError):
    """A manifest is not manifest-shaped."""


@dataclass(frozen=True)
class Artifact:
    """One published artifact, as the manifest records it.

    Attributes:
        arch: Arch id the artifact was built for.
        filename: Asset name at the release host.
        sha256: Lowercase hex digest of the artifact's bytes.
        size: Size in bytes.
    """

    arch: str
    filename: str
    sha256: str
    size: int

    @classmethod
    def from_path(cls, arch: str, path: Path) -> Artifact:
        """Describe a built artifact by reading it.

        Args:
            arch: Arch id the artifact was built for.
            path: Artifact file.

        Returns:
            The artifact entry, its digest and size read from the bytes on disk.
        """
        return cls(arch=arch, filename=path.name, sha256=sha256_of(path), size=path.stat().st_size)

    def as_record(self) -> dict[str, Any]:
        """The artifact as its manifest record."""
        return {"arch": self.arch, "filename": self.filename, "sha256": self.sha256, "size": self.size}


@dataclass(frozen=True)
class Manifest:
    """A parsed release manifest."""

    version: str
    artifacts: tuple[Artifact, ...]

    def artifact(self, arch: str) -> Artifact | None:
        """The artifact for `arch`, or None when the manifest carries no entry for it."""
        for artifact in self.artifacts:
            if artifact.arch == arch:
                return artifact
        return None


class SignatureVerdict(str, Enum):
    """Outcome of authenticating a manifest.

    Attributes:
        VERIFIED: The signature is valid; the manifest may be used.
        DISCARDED: The manifest is thrown away -- no content and no digest from it is
            usable. Covers an absent manifest, an absent or invalid signature, and an
            authentic manifest that is not manifest-shaped.
        UNVERIFIABLE: This host cannot verify signatures at all. The manifest is neither
            trusted nor digest-checked; the caller falls open to the raw OS tool.
    """

    VERIFIED = "verified"
    DISCARDED = "discarded"
    UNVERIFIABLE = "unverifiable"


@dataclass(frozen=True)
class ManifestVerification:
    """Verdict of one manifest authentication, with the manifest only when verified."""

    verdict: SignatureVerdict
    code: str
    detail: str
    manifest: Manifest | None = None


class Rung(str, Enum):
    """Rungs of the provisioning ladder, in the order they are attempted."""

    CACHE = "version-keyed-cache"
    RELEASE = "current-release"
    FROZEN = "commit-frozen-copy"
    NONE = "unavailable"


@dataclass(frozen=True)
class Attempt:
    """What one rung did, for the caller's diagnostics."""

    rung: Rung
    outcome: str
    detail: str


@dataclass(frozen=True)
class ProvisionResult:
    """Result of a provisioning request.

    Attributes:
        path: The verified artifact, or None when nothing resolved.
        rung: Rung that resolved it, or `Rung.NONE`.
        sha256: Digest the artifact was verified against, when one resolved.
        attempts: Every rung attempted, in order.
    """

    path: Path | None
    rung: Rung
    sha256: str | None
    attempts: tuple[Attempt, ...]

    @property
    def provisioned(self) -> bool:
        """True if a verified artifact resolved."""
        return self.path is not None


def render_manifest(version: str, artifacts: Iterable[Artifact]) -> str:
    """Render a release manifest in one deterministic pass.

    The assembled record is serialized exactly once -- sorted keys, two-space indent, no
    escaping of non-ASCII, artifacts ordered by arch, one trailing newline -- and never
    post-edited, so two runs over identical inputs produce identical bytes.

    Args:
        version: Version being released.
        artifacts: Artifacts to record, in any order.

    Returns:
        The manifest text, ready to write as UTF-8.
    """
    record = {
        "version": version,
        "artifacts": [artifact.as_record() for artifact in sorted(artifacts, key=lambda a: a.arch)],
    }
    return json.dumps(record, sort_keys=True, indent=2, ensure_ascii=False) + "\n"


def parse_manifest(text: str) -> Manifest:
    """Parse manifest text.

    Args:
        text: Manifest contents.

    Returns:
        The parsed manifest.

    Raises:
        ManifestError: the text is not valid JSON, or not manifest-shaped: a version
            string plus artifact entries each carrying an arch, a filename, a 64-character
            lowercase hex digest, and a non-negative size.
    """
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise ManifestError(f"invalid JSON ({exc})") from None
    if not isinstance(data, dict):
        raise ManifestError("expected a JSON object")
    version = data.get("version")
    if not isinstance(version, str) or not version:
        raise ManifestError("'version' must be a non-empty string")
    raw_artifacts = data.get("artifacts")
    if not isinstance(raw_artifacts, list):
        raise ManifestError("'artifacts' must be an array")

    artifacts: list[Artifact] = []
    for index, raw in enumerate(raw_artifacts):
        if not isinstance(raw, dict):
            raise ManifestError(f"artifacts[{index}] must be an object")
        for field in ("arch", "filename", "sha256"):
            if not isinstance(raw.get(field), str) or not raw[field]:
                raise ManifestError(f"artifacts[{index}].{field} must be a non-empty string")
        digest = raw["sha256"]
        if len(digest) != 64 or digest.lower() != digest or not all(c in "0123456789abcdef" for c in digest):
            raise ManifestError(f"artifacts[{index}].sha256 is not a lowercase hex sha256 digest")
        size = raw.get("size")
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise ManifestError(f"artifacts[{index}].size must be a non-negative integer")
        artifacts.append(Artifact(arch=raw["arch"], filename=raw["filename"], sha256=digest, size=size))

    arches = [artifact.arch for artifact in artifacts]
    if len(set(arches)) != len(arches):
        raise ManifestError("two artifacts claim the same arch")
    return Manifest(version=version, artifacts=tuple(artifacts))


def is_canonical(text: str) -> bool:
    """True if `text` is the canonical rendering of its own content.

    A manifest that fails this was assembled in more than one pass -- rendered, then
    edited -- so it is not reproducible from its inputs.

    Args:
        text: Manifest contents.

    Returns:
        True when re-rendering the parsed content reproduces the text byte-for-byte;
        False when it differs or the text is not manifest-shaped.
    """
    try:
        manifest = parse_manifest(text)
    except ManifestError:
        return False
    return render_manifest(manifest.version, manifest.artifacts) == text


def sha256_of(path: Path) -> str:
    """Lowercase hex sha256 of a file's bytes, read in chunks."""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(_HASH_CHUNK), b""):
            digest.update(chunk)
    return digest.hexdigest()


def openssl_verifier(manifest: Path, signature: Path, public_key: Path) -> bool | None:
    """Verify a detached Ed25519 signature with the openssl command line.

    Ed25519 is a pure scheme -- it hashes the message itself -- so the raw-input
    interface is used rather than sign-a-digest.

    Args:
        manifest: File whose bytes were signed.
        signature: Detached signature file.
        public_key: PEM public key.

    Returns:
        True when the signature verifies, False when it does not, and None when this host
        has no verifier available -- the caller must not treat None as either verdict.
    """
    try:
        result = subprocess.run(
            ["openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(public_key), "-rawin", "-in", str(manifest), "-sigfile", str(signature)],
            capture_output=True,
            check=False,
        )
    except OSError:
        return None
    return result.returncode == 0


def verify_manifest(
    manifest_path: Path,
    signature_path: Path,
    public_key_path: Path,
    *,
    verifier: Callable[[Path, Path, Path], bool | None] = openssl_verifier,
) -> ManifestVerification:
    """Authenticate a manifest against its detached signature.

    A missing signature is a release defect independent of host tooling, so it discards
    the manifest whether or not a verifier is available. Only an authentic, well-shaped
    manifest is ever returned; every other outcome returns no manifest at all, which is
    what makes "discarded outright" a property of the type rather than of a caller's
    discipline.

    An absent public key and an absent verifier tool are both UNVERIFIABLE but carry
    distinct codes: the first is a release or consumer-configuration defect, the second is
    a property of the host. Both keep the ladder walking to the commit-frozen rung, whose
    trust root is the repository commit rather than this manifest, and both fail the gate.

    Args:
        manifest_path: Manifest file.
        signature_path: Detached signature file.
        public_key_path: PEM public key committed alongside the consumer.
        verifier: Signature verifier; returns None when the host cannot verify.

    Returns:
        The verification, carrying the manifest only on `SignatureVerdict.VERIFIED`.
    """
    if not manifest_path.is_file():
        return ManifestVerification(SignatureVerdict.DISCARDED, "manifest-absent", f"no manifest at {manifest_path}")
    if not public_key_path.is_file():
        return ManifestVerification(SignatureVerdict.UNVERIFIABLE, "public-key-absent", f"no public key at {public_key_path} -- nothing to authenticate the manifest against")
    if not signature_path.is_file():
        return ManifestVerification(SignatureVerdict.DISCARDED, "signature-missing", f"no detached signature at {signature_path} -- manifest discarded, never digest-verified")

    verdict = verifier(manifest_path, signature_path, public_key_path)
    if verdict is None:
        return ManifestVerification(SignatureVerdict.UNVERIFIABLE, "signature-unverifiable", "no signature verifier on this host -- fail open to the raw OS tool, never to unverified bytes")
    if verdict is False:
        return ManifestVerification(SignatureVerdict.DISCARDED, "signature-invalid", f"{signature_path} does not verify against {public_key_path} -- manifest discarded")

    try:
        manifest = parse_manifest(manifest_path.read_text(encoding="utf-8"))
    except (ManifestError, OSError, UnicodeDecodeError) as exc:
        return ManifestVerification(SignatureVerdict.DISCARDED, "manifest-unparsable", f"{manifest_path}: {exc}")
    return ManifestVerification(SignatureVerdict.VERIFIED, "verified", f"{manifest_path} verified against {public_key_path}", manifest)


def check_artifact(path: Path, expected: Artifact) -> str | None:
    """Check an artifact's bytes against a manifest entry.

    Args:
        path: Artifact file.
        expected: Manifest entry for it.

    Returns:
        None when the bytes match, or a description of the mismatch.
    """
    if not path.is_file():
        return f"{path} is not present"
    size = path.stat().st_size
    if size != expected.size:
        return f"{path} is {size} bytes, manifest records {expected.size}"
    actual = sha256_of(path)
    if actual != expected.sha256:
        return f"{path} digest {actual} does not match manifest digest {expected.sha256}"
    return None


def fetch(url: str, destination: Path, *, max_bytes: int = DEFAULT_MAX_ARTIFACT_BYTES, timeout: int = DEFAULT_TIMEOUT_SECONDS) -> str | None:
    """Download a release asset to a local path.

    Only `https://` and `file://` are accepted: a plaintext transport for an artifact this
    process may execute is refused outright rather than compensated for downstream.

    Args:
        url: Asset URL.
        destination: Path to write.
        max_bytes: Refuse a response larger than this.
        timeout: Per-connection timeout in seconds.

    Returns:
        None on success, or a description of the failure.
    """
    if not url.startswith(_ALLOWED_URL_SCHEMES):
        return f"refusing to fetch {url} -- only {' and '.join(_ALLOWED_URL_SCHEMES)} are accepted"
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:  # noqa: S310 - scheme is checked above
            written = 0
            with destination.open("wb") as handle:
                for chunk in iter(lambda: response.read(_HASH_CHUNK), b""):
                    written += len(chunk)
                    if written > max_bytes:
                        return f"{url} exceeds the {max_bytes}-byte ceiling"
                    handle.write(chunk)
    except (urllib.error.URLError, OSError, ValueError) as exc:
        return f"{url} unreachable ({exc})"
    return None


def provision(
    *,
    name: str,
    version: str,
    arch: str,
    cache_dir: Path,
    public_key: Path,
    release_base_url: str | None = None,
    frozen_dir: Path | None = None,
    release_dir_template: str = DEFAULT_RELEASE_DIR_TEMPLATE,
    fetcher: Callable[[str, Path], str | None] = lambda url, dest: fetch(url, dest),
    verifier: Callable[[Path, Path, Path], bool | None] = openssl_verifier,
) -> ProvisionResult:
    """Resolve a verified artifact by walking the provisioning ladder.

    Rungs, first hit wins: a version-keyed cache re-verified in place with no network; the
    current release, accepted only against a signature-verified manifest; a copy frozen
    into a commit, verified against its committed digest sidecar; then nothing. A manifest
    that fails authentication STOPS the ladder -- that is a security event, not a reason to
    reach for an older copy -- while an unreachable or unverifiable release rung continues,
    since the frozen rung's trust root is the repository commit rather than the manifest.

    Args:
        name: Module name, as the cache and release asset spell it.
        version: Version to provision.
        arch: Arch id to resolve.
        cache_dir: Version-keyed cache directory.
        public_key: PEM public key for the release signature.
        release_base_url: Release host root; None skips the release rung.
        frozen_dir: Directory holding commit-frozen artifacts; None skips that rung.
        release_dir_template: Per-release asset directory under the base URL.
        fetcher: Download function, for tests and for callers with their own transport.
        verifier: Signature verifier; returns None when the host cannot verify.

    Returns:
        The provisioning result, including every rung attempted.
    """
    attempts: list[Attempt] = []
    cached = cache_dir / f"{name}-{version}"
    sidecar = Path(f"{cached}.sha256")

    reverified = _reverify_cache(cached, sidecar)
    if reverified is None:
        attempts.append(Attempt(Rung.CACHE, "resolved", f"{cached} re-verified in place"))
        return ProvisionResult(path=cached, rung=Rung.CACHE, sha256=sha256_of(cached), attempts=tuple(attempts))
    attempts.append(Attempt(Rung.CACHE, "skipped", reverified))

    if release_base_url is None:
        attempts.append(Attempt(Rung.RELEASE, "skipped", "no release host configured"))
    else:
        result = _from_release(
            name=name,
            version=version,
            arch=arch,
            cache_dir=cache_dir,
            public_key=public_key,
            release_base_url=release_base_url.rstrip("/"),
            release_dir_template=release_dir_template,
            fetcher=fetcher,
            verifier=verifier,
        )
        attempts.append(result[0])
        if result[1] is not None:
            return ProvisionResult(path=result[1], rung=Rung.RELEASE, sha256=sha256_of(result[1]), attempts=tuple(attempts))
        if result[0].outcome == "discarded":
            return ProvisionResult(path=None, rung=Rung.NONE, sha256=None, attempts=tuple(attempts))

    if frozen_dir is None:
        attempts.append(Attempt(Rung.FROZEN, "skipped", "no frozen artifact directory configured"))
    else:
        attempt, resolved = _from_frozen(name=name, version=version, arch=arch, cache_dir=cache_dir, frozen_dir=frozen_dir)
        attempts.append(attempt)
        if resolved is not None:
            return ProvisionResult(path=resolved, rung=Rung.FROZEN, sha256=sha256_of(resolved), attempts=tuple(attempts))

    attempts.append(Attempt(Rung.NONE, "unavailable", "nothing verified -- fail open to the raw OS tool"))
    return ProvisionResult(path=None, rung=Rung.NONE, sha256=None, attempts=tuple(attempts))


def _reverify_cache(cached: Path, sidecar: Path) -> str | None:
    """Re-verify a cache entry against its digest sidecar, evicting it on failure.

    Returns:
        None when the cached bytes still match, or the reason the entry was not usable.
    """
    if not cached.is_file():
        return f"no cache entry at {cached}"
    if not sidecar.is_file():
        _evict(cached, sidecar)
        return f"{cached} has no digest sidecar -- evicted"
    try:
        expected = sidecar.read_text(encoding="utf-8").split()[0]
    except (OSError, IndexError):
        _evict(cached, sidecar)
        return f"{sidecar} is unreadable -- evicted"
    if sha256_of(cached) != expected:
        _evict(cached, sidecar)
        return f"{cached} failed re-verification against {sidecar} -- evicted"
    return None


def _from_release(
    *,
    name: str,
    version: str,
    arch: str,
    cache_dir: Path,
    public_key: Path,
    release_base_url: str,
    release_dir_template: str,
    fetcher: Callable[[str, Path], str | None],
    verifier: Callable[[Path, Path, Path], bool | None],
) -> tuple[Attempt, Path | None]:
    """Fetch, authenticate, and cache the current release's artifact for `arch`."""
    release_dir = release_dir_template.format(name=name, version=version)
    base = f"{release_base_url}/{release_dir}"
    with tempfile.TemporaryDirectory(prefix="release-transaction-") as tmp:
        staging = Path(tmp)
        manifest_path = staging / "manifest.json"
        signature_path = staging / "manifest.json.sig"

        failure = fetcher(f"{base}/manifest.json", manifest_path)
        if failure is not None:
            return Attempt(Rung.RELEASE, "unreachable", failure), None
        signature_failure = fetcher(f"{base}/manifest.json.sig", signature_path)
        if signature_failure is not None and not signature_path.is_file():
            return Attempt(Rung.RELEASE, "discarded", f"no detached signature at {base}/manifest.json.sig -- manifest discarded, never digest-verified"), None

        verification = verify_manifest(manifest_path, signature_path, public_key, verifier=verifier)
        if verification.verdict is SignatureVerdict.DISCARDED:
            return Attempt(Rung.RELEASE, "discarded", verification.detail), None
        if verification.verdict is SignatureVerdict.UNVERIFIABLE:
            return Attempt(Rung.RELEASE, "unverifiable", verification.detail), None

        manifest = verification.manifest
        assert manifest is not None  # a VERIFIED verification always carries one
        if manifest.version != version:
            return Attempt(Rung.RELEASE, "unreachable", f"release manifest states version {manifest.version}, not {version}"), None
        expected = manifest.artifact(arch)
        if expected is None:
            return Attempt(Rung.RELEASE, "unreachable", f"release manifest carries no entry for arch {arch}"), None

        artifact_path = staging / expected.filename
        failure = fetcher(f"{base}/{expected.filename}", artifact_path)
        if failure is not None:
            return Attempt(Rung.RELEASE, "unreachable", failure), None
        mismatch = check_artifact(artifact_path, expected)
        if mismatch is not None:
            return Attempt(Rung.RELEASE, "discarded", mismatch), None

        cached = _install(artifact_path, cache_dir / f"{name}-{version}", expected.sha256)
        return Attempt(Rung.RELEASE, "resolved", f"current release {version} verified and cached at {cached}"), cached


def _from_frozen(*, name: str, version: str, arch: str, cache_dir: Path, frozen_dir: Path) -> tuple[Attempt, Path | None]:
    """Verify a commit-frozen artifact against its committed sidecar and cache it."""
    candidate = frozen_dir / f"{name}-{arch}"
    sidecar = Path(f"{candidate}.sha256")
    if not candidate.is_file():
        return Attempt(Rung.FROZEN, "absent", f"no frozen artifact at {candidate}"), None
    if _is_lfs_pointer(candidate):
        return Attempt(Rung.FROZEN, "absent", f"{candidate} is an unmaterialized large-file pointer, not the artifact"), None
    if not sidecar.is_file():
        return Attempt(Rung.FROZEN, "rejected", f"{candidate} has no committed digest sidecar"), None
    try:
        expected = sidecar.read_text(encoding="utf-8").split()[0]
    except (OSError, IndexError):
        return Attempt(Rung.FROZEN, "rejected", f"{sidecar} is unreadable"), None
    actual = sha256_of(candidate)
    if actual != expected:
        return Attempt(Rung.FROZEN, "rejected", f"{candidate} digest {actual} does not match committed {expected}"), None
    cached = _install(candidate, cache_dir / f"{name}-{version}", actual)
    return Attempt(Rung.FROZEN, "resolved", f"commit-frozen artifact verified against {sidecar} and cached at {cached}"), cached


def _install(source: Path, destination: Path, digest: str) -> Path:
    """Move verified bytes into the version-keyed cache and write their digest sidecar."""
    destination.parent.mkdir(parents=True, exist_ok=True)
    staged = Path(f"{destination}.incoming")
    shutil.copyfile(source, staged)
    os.chmod(staged, 0o755)
    os.replace(staged, destination)
    Path(f"{destination}.sha256").write_text(f"{digest}\n", encoding="utf-8")
    return destination


def _evict(cached: Path, sidecar: Path) -> None:
    """Remove a cache entry and its sidecar, ignoring what is already gone."""
    cached.unlink(missing_ok=True)
    sidecar.unlink(missing_ok=True)


def _is_lfs_pointer(path: Path) -> bool:
    """True if the file's leading bytes are a large-file pointer rather than an artifact."""
    try:
        with path.open("rb") as handle:
            prefix = handle.read(_SNIFF_BYTES)
    except OSError:
        return False
    return any(marker in prefix for marker in _LFS_POINTER_MARKERS)


def artifacts_from_specs(specs: Sequence[str]) -> list[Artifact]:
    """Build artifact entries from `arch=path` command-line specs.

    Args:
        specs: Strings of the form `arch=path`.

    Returns:
        The artifact entries, digests and sizes read from the files.

    Raises:
        ValueError: a spec is not `arch=path`, or its path is not a file.
    """
    artifacts: list[Artifact] = []
    for spec in specs:
        arch, separator, raw_path = spec.partition("=")
        if not separator or not arch or not raw_path:
            raise ValueError(f"artifact spec {spec!r} is not of the form arch=path")
        path = Path(raw_path)
        if not path.is_file():
            raise ValueError(f"artifact spec {spec!r}: {path} is not a file")
        artifacts.append(Artifact.from_path(arch, path))
    return artifacts
