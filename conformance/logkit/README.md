# logkit conformance — one record contract, byte-identical implementations

`schemas/logkit` says three languages emit **the same bytes** for the same event. This suite is where that stops being a claim: one shared input set, one recorded set of goldens, and a gate that fails on any byte difference — language against language, and each language against the goldens.

Stdlib-only Python runner. Requires `go` and `cargo` on PATH.

## Usage

```sh
python3 conformance/logkit/check.py             # run the gate
python3 conformance/logkit/check.py --selftest  # prove the gate catches a planted drift
python3 conformance/logkit/check.py --record    # rewrite the goldens (maintainer action)
```

Exit codes: `0` every implementation emitted identical bytes; `1` a divergence was found, a golden is absent, or a golden is not canonical — **pause** and have a human judge it; `2` the harness could not run (a toolchain absent, an implementation that would not build).

Wired as the required `logkit-conformance` job in `.github/workflows/ci.yml`, on every push to `main` and every pull request. The job runs the gate and then the selftest, so a gate that has stopped discriminating fails red instead of passing quietly.

## Layout

| Path | Role |
| --- | --- |
| `inputs/<nn>-<slug>.json` | The shared input set: one case per file, read in file-name order. A case is the **event as a call site hands it over** — a raw inbound level token, a timestamp, and whatever optional members it carries — not a finished record. |
| `golden/records.jsonl` | The canonical JSON rendering of every case, one record per line, in case order. |
| `golden/records.human.txt` | The human rendering of the same cases, in the same order. A case with a stack spans more than one line, so line numbers do not track case numbers here. |
| `check.py` | The gate: runs every implementation, compares everything against everything, reports the first differing line per artifact pair. |

Each implementation renders the suite from **its own test**, using only what the standard's own API offers:

| Implementation | Test |
| --- | --- |
| `go/logkit` | `TestConformanceSuiteMatchesGolden` |
| `rust/logkit` | `tests/conformance.rs` |

So `go test ./...` and `cargo test` each fail on drift by themselves. The gate adds the comparison neither can make alone — the one between the languages — and is what CI requires.

## What the gate compares

Four independent checks, all of which must pass:

1. **Case coverage** — every implementation rendered every case in `inputs/`, in the same order. An implementation that skips a case fails here rather than passing on a shorter list.
2. **Language against language** — the two implementations' `records.jsonl` and `records.human.txt` are byte-equal.
3. **Language against golden** — each implementation's output is byte-equal to the recorded goldens, so a drift both languages share is caught too.
4. **Goldens are canonical** — each recorded record is re-serialized with an independent canonicalizer (Python's own sorted-key, minimal-separator JSON) and must equal its own bytes. A record that both implementations agree on but that is not canonical JSON fails.

## Goldens are read, never written

Nothing in the gate path creates a golden. An absent golden is a **finding**, not a prompt to generate one: a suite that fills in its own expectations cannot fail, and a self-created golden records whatever the code did on the day it ran. The per-language tests fail the same way — they read `golden/` and never write it.

`--record` is the one writer, and it is a maintainer action outside the gate: it refuses unless every implementation already agrees byte for byte, so a recorded golden is a cross-language agreement rather than one language's opinion. Recording is followed by reading the diff, not by trusting it.

Every file a run generates lands in a temporary directory the runner owns and deletes: the implementations write their rendered output to the path named by `LOGKIT_CONFORMANCE_OUT`, which only the runner sets. Unset — a plain `go test` or `cargo test` — they write nothing and only compare.

## The selftest

`--selftest` proves the gate discriminates rather than merely runs:

1. The clean tree must pass. Nothing after this means anything if it does not.
2. A one-token edit to Go's `warn` level token must fail the gate, naming Go.
3. The same edit to Rust's must fail the gate, naming Rust.
4. A removed golden must fail the gate rather than be regenerated.

Each plant is applied to the working tree and reverted from the bytes read before it was applied; the revert is verified, and a selftest that cannot restore a file says so loudly instead of leaving a patched checkout behind. Two runs on one tree must not overlap.

## What the input set covers

Every level, through every inbound spelling class: a canonical token, a case-and-whitespace variant of an equivalent alias (`  WARNING  ` → `warn`, preserving nothing), and both non-equivalent aliases (`PANIC` → `fatal`, `trace` → `debug`, each preserving its source token in the reserved `native_level` key). Every optional root field, present and absent, alone and all at once. Every JSON value kind a `fields` entry can hold, including an explicit `null`, an array, and a nested object whose own keys must sort at depth. Both sides of the human rendering's bare-or-quoted rule. Non-ASCII keys and values. Number forms where the canonical rendering is interesting: an integral float that collapses to an integer, a fractional value, a magnitude that needs an exponent, and the exact-integer limit of a double.

Adding a case is one file in `inputs/`, then `--record`, then reading the two new golden lines. The case's `id` must equal its file name; the runner and both implementations check that, so a renamed file cannot quietly become a second case.

## Deliberately out of the input set

Each of these would pin behaviour the contract does not yet decide, so a golden here would freeze one implementation's answer instead of the standard's. They are open questions for `schemas/logkit`, not gaps in the gate.

| Excluded | Why |
| --- | --- |
| A non-equivalent level token padded with whitespace (`" trace "`) | The contract says the source token is preserved in `native_level` without saying whether that is the raw token or the trimmed one; the implementations differ, and the trimmed-vs-raw question belongs to the contract. |
| A `fields` value carrying non-ASCII whitespace (`U+00A0` and the like) | The contract's bare-value pattern uses `\s`, which is Unicode-aware where one implementation's check is ASCII-only. A golden either way would ratify an implementation. |
| A `fields` key outside the Basic Multilingual Plane | The canonical JSON sorts by UTF-16 code unit; both human renderings sort by code point. The two orders agree for every BMP key and disagree above it, and the contract does not say which order the human rendering follows. |
| A number below `1e-6`, or an integer past the exact-integer limit of a double | Canonical numbers are ECMA-262 doubles, so these are at or past the edge of what the form can round-trip; the two JCS libraries' behaviour there is untested and is a contract question before it is an implementation one. |
| Validation failures — an over-long message, a control character in a `message`, a `fields` key colliding with a root field | This gate compares the bytes of records that are valid. Each implementation's own adversarial suite owns rejection parity. |
