# dist-guard — the SC-DISTRIBUTION no-committed-binaries guard

Stdlib-only, no install: run it from a checkout. CD is the distribution channel — see the
`release-*.yml` reusable workflow templates in [`.github/workflows/`](../../.github/workflows/):
a library release pins a git tag with no archive, a CLI release publishes per-OS/arch archives
+ checksums to the platform release. Nothing built should be a tracked git blob.

## Scope

A candidate is a git-tracked file whose **tracked mode is executable** (`100755`) and whose
content is binary by git's own is-binary heuristic (a NUL byte or a UTF-8 decode failure in its
leading bytes). That precisely targets a committed, ready-to-exec build output — the class
`go/.bin/build-helpers-<goos>-<goarch>` belongs to and the class CD replaces — without touching
a legitimately committed binary *asset* (image, font) that carries no execute bit; those remain
`scripts/check_no_raw_binary.py`'s concern, with its own size threshold and LFS exemption.

## Commands

| Command | Does |
|---|---|
| `scan` | Fails if any tracked executable is binary content and not in `allowlist.json`. |
| `generate` | Renders `allowlist.json` from its producer and writes it. |
| `check` | Renders `allowlist.json` in memory and diffs against disk; exit 1 on drift or missing. |

```sh
python3 tooling/dist-guard/check.py scan --root .
python3 tooling/dist-guard/check.py generate --root .
python3 tooling/dist-guard/check.py check --root .
```

## The allowlist

Empty in the steady state: no committed binary is permitted at all. SC-DISTRIBUTION allows at
most one documented exception — zero or one entry is valid; two or more is a hard error
(`ValueError`, exit 2 from the CLI), never a growable list. The allowlist has a producer —
never hand-edit `allowlist.json`; edit `ENTRIES` in the producer and run `generate`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | clean (`scan`), or current with its producer (`check`) |
| 1 | a committed binary is not allowlisted (`scan`); the allowlist drifted or is missing (`check`) |
| 2 | usage or allowlist-source error (e.g. `ENTRIES` carries more than one item) |

## Wiring it into CI

```yaml
- name: No committed binaries outside the SC-DISTRIBUTION allowlist
  run: python3 tooling/dist-guard/check.py scan --root .
- name: Allowlist is current with its producer
  run: python3 tooling/dist-guard/check.py check --root .
```
