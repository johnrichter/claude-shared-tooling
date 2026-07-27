# clikit conformance — one CLI contract, byte-identical implementations

`schemas/clikit` says every CLI in the fleet writes **one result record** and exits with **one of eleven codes**, and that two implementations of that contract produce the same bytes and the same integer for the same outcome. This suite is where that stops being a claim: one shared input set, one recorded set of goldens, and a gate that fails on any difference — language against language, each language against the goldens, and every golden against the contract's own source files.

Stdlib-only Python runner. Requires `go` and `cargo` on PATH.

## Usage

```sh
python3 conformance/clikit/check.py             # run the gate
python3 conformance/clikit/check.py --selftest  # prove the gate catches a planted drift
python3 conformance/clikit/check.py --record    # rewrite the goldens (maintainer action)
```

Exit codes: `0` every implementation emitted identical records and identical exit codes; `1` a divergence was found, a golden is absent, a golden is not canonical, or a golden disagrees with the contract — **pause** and have a human judge it; `2` the harness could not run (a toolchain absent, an implementation that would not build).

Wired as the required `clikit-conformance` job in `.github/workflows/ci.yml`, on every push to `main` and every pull request. The job runs the gate and then the selftest, so a gate that has stopped discriminating fails red instead of passing quietly.

## Layout

| Path | Role |
| --- | --- |
| `inputs/<nn>-<slug>.json` | The shared input set: one case per file, read in file-name order. A case is the **outcome as a command hands it over** — a status name, a command path, and whatever data and diagnostics it produced — not a finished record. |
| `golden/results.jsonl` | The canonical record for every case, one per line, in case order. |
| `golden/exit-codes.txt` | `<case id> <status> <exit code>` per case, in the same order. |
| `check.py` | The gate: runs every implementation, compares everything against everything, and judges the goldens against the contract. |

Each implementation renders the suite from **its own test**, building every record through the same public API a CLI uses:

| Implementation | Test |
| --- | --- |
| `go/clikit` | `TestConformanceSuiteMatchesGolden` |
| `rust/clikit` | `tests/conformance.rs` |

So `go test ./...` and `cargo test` each fail on drift by themselves. The gate adds the two comparisons neither can make alone — the one between the languages, and the one against the contract — and is what CI requires.

## What the gate compares

Seven independent checks, all of which must pass:

1. **Suite invariants** — every case's `id` equals its file name, so a renamed file cannot quietly become a second case; every case says what it pins; and no case declares a value the contract leaves undecided (see the exclusions below).
2. **Shape coverage** — every outcome class and every diagnostic and triage shape is reached by some case. The class set and the triage-kind set are read from `schemas/clikit`, so a class or kind added there fails the gate until a golden covers it.
3. **Case coverage** — every implementation rendered every case in `inputs/`, in the same order. An implementation that skips a case fails here rather than passing on a shorter list.
4. **Language against language** — the implementations' `results.jsonl`, `exit-codes.txt` and case lists are byte-equal.
5. **Language against golden** — each implementation's output is byte-equal to the recorded goldens, so a drift both languages share is caught too.
6. **Goldens are canonical** — each recorded record is re-serialized with an independent canonicalizer (Python's own sorted-key, minimal-separator JSON) and must equal its own bytes. A record both implementations agree on but that is not canonical JSON fails.
7. **Goldens against the contract** — every recorded record is judged against `result-record.schema.json` and `clikit.contract.json`: the closed root field set, the required fields, the `status`/`exit_code` pairing, which of `errors` and `caveats` the class requires or forbids, the governing error's class, the diagnostic and triage shapes, and the exit code the record claims against the one `exit-codes.txt` records. The rules are read from those files rather than restated here, and the two files must agree with each other.

Check 7 is the only one an agreement between the implementations cannot substitute for: a wrong pairing both languages share is invisible to every comparison and visible here.

## The exit code

`exit-codes.txt` carries the integer taken from the record itself — the value a CLI hands `os.Exit`/`std::process::exit` immediately after writing the record, since the contract makes `exit_code` and `status` one fact in two channels. The gate compares that integer as its own artifact rather than only as a field inside a JSON line, so an exit-code drift is reported as one ("case 13 exits 81 in go and 80 in rust") instead of being buried in a record diff.

No process is spawned: neither implementation ships a CLI binary yet, and the record is a pure function of the invocation, so the integer is the whole fact. When a CLI does land, the exit path it takes is that CLI's own test to make.

## Goldens are read, never written

Nothing in the gate path creates a golden. An absent golden is a **finding**, not a prompt to generate one: a suite that fills in its own expectations cannot fail, and a self-created golden records whatever the code did on the day it ran. The per-language tests fail the same way — they read `golden/` and never write it.

`--record` is the one writer, and it is a maintainer action outside the gate: it refuses unless every implementation already agrees byte for byte **and** the agreed bytes satisfy the contract, so a recorded golden is a cross-language agreement the contract permits rather than one language's opinion or a shared drift. Recording is followed by reading the diff, not by trusting it.

Every file a run generates lands in a temporary directory the runner owns and deletes: the implementations write their rendered output to the path named by `CLIKIT_CONFORMANCE_OUT`, which only the runner sets. Unset — a plain `go test` or `cargo test` — they write nothing and only compare.

## The selftest

`--selftest` proves the gate discriminates rather than merely runs:

1. The clean tree must pass. Nothing after this means anything if it does not.
2. A one-token edit pairing Go's `unsupported` class with an exit code of its own must fail the gate, naming Go.
3. A one-token edit to Rust's record version must fail the gate, naming Rust.
4. Each removed golden must fail the gate rather than be regenerated.
5. A record whose exit code does not pair with its status must be rejected by the contract check — the one check no single-language plant reaches, since a comparison would catch that drift long before.

Each source plant is applied to the working tree and reverted from the bytes read before it was applied; the revert is verified, and a selftest that cannot restore a file says so loudly instead of leaving a patched checkout behind. Two runs on one tree must not overlap.

## What the input set covers

Every one of the eleven classes, and with them every exit code. Every triage kind, and every optional member each kind may carry: a bare `reinvoke`, one carrying an instruction, one carrying a retry floor and an instruction at once, a bare `run_tool`, one carrying an instruction, and `manual`. A diagnostic with a context and one without. `errors` alone, `caveats` alone, and both on one record — including a non-governing error in a different class than the record's own, which only the governing error's class must match. Diagnostic codes at the minimum two segments and the maximum four, and two of the three codes the library reserves to itself. Multi-member arrays given in an order no sort would produce, so the arrays must be emitted as the tool ordered them.

For the record's own bytes: a record with nothing but its four required fields; every JSON value kind a `data` member can hold, including an explicit `null`, an array that keeps its order and a nested object whose own keys must sort at depth; `data` keys given in an order no sort would produce; the number forms where the canonical rendering is interesting (an integral float that collapses to an integer, a fractional value, a magnitude needing an exponent, and the exact-integer limit of a double); and the escaping rule — a quotation mark and a backslash escaped, every other character emitted as literal UTF-8, including one above the Basic Multilingual Plane.

Adding a case is one file in `inputs/`, then `--record`, then reading the new golden lines.

## Deliberately out of the input set

Each of these would pin behaviour the contract does not decide, or duplicate a check that already has an owner. They are open questions for `schemas/clikit` or work for another suite, not gaps in the gate.

| Excluded | Why |
| --- | --- |
| A triage `after_seconds` of `0` | The contract says an absent `after_seconds` means retry immediately, without saying whether a declared `0` is distinguishable from absent; one implementation omits it and the other emits it, and which is right is the contract's call. The gate rejects such a case by name rather than letting it surface as a mystery divergence. |
| A control character inside a `data` or `context` value | The schema constrains messages, instructions and argv tokens to single lines, but places no rule on a `data` value, so a control character there is legal and its escape form is whichever the two canonicalizers happen to choose. A golden either way would ratify a library. |
| A key above the Basic Multilingual Plane nested inside a `data` or `context` value | Canonical JSON sorts by UTF-16 code unit and this suite's independent canonical check sorts by code point; the two agree for every key below that plane and disagree above it. Top-level `data` and `context` names are ASCII by pattern, so only a nested object can reach the disagreement, and a golden would pick a sort order the contract has not. |
| `errors` or `caveats` at the 50-member bound, and with it the reserved `caveats.clikit.diagnostics_truncated` code | The only coherent record carrying that code has `errors` at the bound, and a 50-member golden would dominate the corpus without adding a byte fact the other cases do not already cover. Bound parity is each implementation's own adversarial suite. |
| Validation failures — an over-long message, a control character in a message, a malformed code, a mispaired status | This gate compares the bytes of records that are valid. Each implementation's adversarial suite owns rejection parity. |
| The terminating logkit record | Log output is logkit's contract, gated by `conformance/logkit`. clikit owns only the mapping onto it, and the two implementations attach different detail beyond the required `fields.clikit` members — a question for `schemas/clikit` before it is one for a gate. |
