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
| `generate` | Renders `allowlist.json` from its producer (`dist_guard/allowlist.py`) and writes it. |
| `check` | Renders `allowlist.json` in memory and diffs against disk; exit 1 on drift or missing. |

```sh
python3 tooling/dist-guard/check.py scan --root .
python3 tooling/dist-guard/check.py generate --root .
python3 tooling/dist-guard/check.py check --root .
```

## The allowlist

Exactly one entry: `go/.bin/build-helpers-darwin-arm64`, a Step-0 last resort committed before
any arm64-macOS CI runner existed to produce it through CD. `dist_guard/allowlist.py` is the
producer — never hand-edit `allowlist.json`; edit `ENTRIES` there and run `generate`.
`ENTRIES` carrying more than one item is a hard error (`ValueError`, exit 2 from the CLI):
SC-DISTRIBUTION scopes this as a single documented exception, not a pattern later exceptions
can join.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | clean (`scan`), or current with its producer (`check`) |
| 1 | a committed binary is not allowlisted (`scan`); the allowlist drifted or is missing (`check`) |
| 2 | usage or allowlist-source error (e.g. `ENTRIES` has more than one item) |

## Known residual (out of this tool's scope)

`go/.bin/build-helpers-darwin-amd64`, `-linux-amd64`, and `-linux-arm64` are committed today
alongside the allowlisted `-darwin-arm64` — pre-SC-DISTRIBUTION bootstrap debt from before CD
existed to build and publish them. `scan` correctly flags all three; retiring them is a release-
pipeline task (wiring `build-helpers`'s actual release through `release-cli.yml`), not this
guard's job to paper over.

## Wiring it into CI

```yaml
- name: No committed binaries outside the SC-DISTRIBUTION allowlist
  run: python3 tooling/dist-guard/check.py scan --root .
- name: Allowlist is current with its producer
  run: python3 tooling/dist-guard/check.py check --root .
```
