# release-transaction — the gate

Interpreter and command line for the contract in [`schemas/release-transaction/`](../../schemas/release-transaction/). Stdlib-only, no install: run it from a checkout.

The contract holds the enumerator set; this tool holds none of it. Adding, removing, or renaming an enumerator is a contract edit, and the gate follows.

## Commands

| Command | Does |
|---|---|
| `verify-release` | Judges one release record: every enumerator satisfied or **declared** not-applicable, and no open compliance defect pausing the module. |
| `changed` | Requires every module changed since a base ref to carry a version bump **and** a published release. |
| `render-manifest` | Renders a release manifest in one deterministic pass. |
| `verify-manifest` | Authenticates a manifest against its detached signature; with `--artifact-dir`, checks every artifact's bytes. |
| `provision` | Resolves a verified artifact by walking the provisioning ladder. |

```sh
python3 tooling/release-transaction/check.py verify-release --record release.json --root .
python3 tooling/release-transaction/check.py changed --base origin/main
python3 tooling/release-transaction/check.py render-manifest --version 1.2.3 --artifact linux-x86_64=dist/tool-linux-x86_64 --out dist/manifest.json
python3 tooling/release-transaction/check.py verify-manifest --manifest dist/manifest.json --public-key release/release-pubkey.pem --artifact-dir dist
python3 tooling/release-transaction/check.py provision --name tool --version 1.2.3 --arch linux-x86_64 --cache-dir "$XDG_CACHE_HOME/tool" --public-key release/release-pubkey.pem --base-url https://example.invalid/releases/download
```

`--json` on `verify-release` and `changed` emits the same verdict as a machine-readable document.

## Exit codes

The contract declares them; the tool obeys them.

| Code | Meaning |
|---|---|
| 0 | complete transaction, or provisioning resolved |
| 1 | partial release — an enumerator is missing or its evidence failed |
| 2 | usage or contract error — the record, the contract, or the invocation is malformed |
| 3 | provisioning resolved nothing — the caller fails open to the raw OS tool |
| 4 | paused by an open compliance defect against the released module |

A partial release and a pause are separate codes because they call for different action: one is a release to finish, the other a defect to resolve.

## Two fail directions, on purpose

A host provisioning a binary and a gate judging a release want opposite things from "cannot verify".

- **Provisioning** (`provision`): no verifier available → nothing resolves, exit 3, and the caller falls open to the raw OS tool. Blocking a developer's session over a missing local tool buys no security; running unverified bytes does cost some.
- **Gate** (`verify-release`): no verifier available → the `artifacts` enumerator is UNSATISFIED and the release fails. An artifact nobody can authenticate is not releasable.

Neither path ever accepts a manifest on its digests alone. A missing or invalid signature discards the manifest outright, in both directions.

## Wiring it into CI

Two jobs, both required:

```yaml
- name: Changed modules carry a release
  run: python3 tooling/release-transaction/check.py changed --base "origin/${{ github.base_ref || 'main' }}"

- name: Release transaction is complete
  run: python3 tooling/release-transaction/check.py verify-release --record release.json --pause-register release-pause-register.json
```

`changed` needs history on both refs, so check out with `fetch-depth: 0`. It reads versions from refs rather than the working tree, so uncommitted edits are invisible to it by design — a release is judged on what was committed.

## Layout

| Path | Role |
|---|---|
| `check.py` | Command line. Running it puts this directory on the import path, so the package below resolves with no install. |
| `release_transaction/contract.py` | Loads the contract and one release record; JSON Pointer and template helpers. |
| `release_transaction/evidence.py` | Resolves a record's evidence to per-enumerator statuses and one verdict; reads the release-pause register. |
| `release_transaction/changed.py` | The changed-module gate. |
| `release_transaction/provisioning.py` | Manifest rendering, signature verification, the provisioning ladder. |
| `release_transaction/gitstate.py` | Read-only git access — the only place a git command runs. |

## Tests

`tests/test_release_transaction.py` (import-based) and `tests/test_release_transaction_cli.py` (subprocess) run in the repository's `unit-tests` job. Both use throwaway git repos and `file://` release directories, so neither needs a network; the two cases that need a real Ed25519 round trip skip themselves where `openssl` is absent.

```sh
python3 -m unittest discover -s tests -p "test_release_transaction*.py" -v
```

Between them they plant a release missing each enumerator in turn and assert the failure names that enumerator, and cover a change with no version bump, a discarded manifest, a re-verified cache entry, the ladder's preference for the current release, and the release-pause hook.

## Notes for anyone extending it

- **Signature verification shells out to `openssl`.** Ed25519 is a pure scheme, so the raw-input interface is used rather than sign-a-digest. `openssl` is also what produces these signatures, so producer and consumer agree by construction, and the repository stays free of a compiled crypto dependency. The verifier is injectable: pass your own, returning `True`, `False`, or `None` for "this host cannot verify".
- **A record is validated structurally in Python, not by a schema validator**, so an error can name the offending enumerator instead of a JSON path. The published schemas remain the contract for external validators; keep the two in step when either changes.
- **Fetching accepts only `https://` and `file://`.** A plaintext transport for bytes this process may execute is refused rather than compensated for downstream. `file://` is what makes the ladder testable without a network.
