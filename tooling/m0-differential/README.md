# m0-differential — structural CLI-surface gate for build-helpers

`check.py` builds the pre-M0 artifact and the working tree's `go/build-helpers` side by side, runs one fixed probe corpus through both, and reports every difference a caller could observe programmatically. Any such difference fails the gate and pauses the build for a human.

Stdlib-only. Requires `go` and `git` on PATH.

## What counts as a difference

Compared:

- **argv flag grammar** — each subcommand's defined flags with their types and defaults, read out of Go's own flag-package usage dump; plus which flags the usage synopsis marks required, optional, or mutually exclusive, and how many positional slots it takes.
- **stdin handling** — probes that feed stdin (`--changed -`) alongside probes that do not.
- **stdout JSON schema** — every field path and its type, for every probe that returns JSON, plus whether stdout is JSON, opaque text, or empty at all.
- **exit codes** — per probe.

Not compared, ever: error messages, reason strings, warnings, flag descriptions, help descriptions, rendered Markdown, printed paths — every byte of human-readable text.

The reason is narrow and deliberate. Consumers of build-helpers branch on exit codes and parse named JSON fields; none of them read a sentence. A comparison that failed on wording would fail on harmless work as loudly as on a real break, and would then need a bookkeeping mechanism to route around itself. There is none here: no declared-delta list, no exemptions, no per-line carve-outs.

The line between the two runs through the usage text: everything before a usage line's `->` marker is the argv synopsis and is grammar; everything after it describes behaviour to a human and is prose. `--band` appearing in a synopsis is a finding. The sentence explaining what `--band` does is not.

## Usage

```sh
python3 tooling/m0-differential/check.py            # run the gate
python3 tooling/m0-differential/check.py --record   # ...and rewrite the committed evidence
python3 tooling/m0-differential/check.py --selftest # prove the gate discriminates
python3 tooling/m0-differential/check.py --rollback DIR   # recover the pre-M0 artifact into DIR
```

Exit codes: `0` no structural divergence; `1` a structural divergence was found — pause and have a human judge it; `2` the harness itself could not run (probe corpus out of step with the baseline roster, a tree that would not export, a build failure).

This is a one-shot milestone gate, not a CI job. It compares against a frozen point in history, so it is answered once, by a human, and not re-litigated on every push.

## Coverage

The corpus carries at least one probe for every subcommand `go/build-helpers/testdata/pre-m0-baseline/surface.json` enumerates, and the run aborts with exit 2 if that stops being true in either direction — a shrinking corpus fails loudly instead of quietly passing. Probes cover success paths, business-rule failures, and usage errors; their inputs live in `go/build-helpers/testdata/post-m0-differential/fixtures`.

Each probe gets its own fresh copy of the fixture tree, since several subcommands rewrite their inputs. The copy goes to a fixed path rather than a fresh temp directory, because a couple of subcommands key their output by absolute input path; a fixed path keeps captures reproducible and keeps a temp-directory name from masquerading as a renamed field. One consequence: two runs on one machine must not overlap.

Both binaries run with a scrubbed environment. `self-check` and the accounting commands read the ambient session's model and effort, and an inherited `CLAUDE_*`/`ANTHROPIC_*` would make the capture depend on who ran it.

## The rollback

The pre-M0 artifact is the tree at the commit named in `m0_differential/checkout.py`, recovered with `git archive` — a pure read of the object database that registers no worktree and touches no ref. Every run recovers and rebuilds it, so the rollback is exercised continuously rather than documented and hoped for. Recovery is trusted only once the rebuilt binary reproduces `go/build-helpers/testdata/pre-m0-baseline/help.txt` byte-for-byte; both sides are frozen history, so that one byte-exact check can never be tripped by a future reword.

`go/build-helpers/testdata/post-m0-differential/rollback.md` records the procedure and its execution.

## Selftest

`--selftest` compiles nine deliberately broken copies of the working tree and runs each through the full pipeline: six structural plants (a removed JSON field, a renamed one, a retyped one, a changed exit code, a renamed flag, a flag moved from optional to required) that must each be caught, and three prose-only plants (a reworded error message, a reworded help description, a reworded reason string) that must each be ignored. It is the proof that the exclusion is a designed property rather than an accident of what happens to be in the tree.

## Layout

| file | role |
| --- | --- |
| `check.py` | CLI: the gate, the selftest, the rollback |
| `m0_differential/checkout.py` | export a tree at a ref or from the worktree, patch it, build it |
| `m0_differential/probes.py` | the probe corpus and its coverage check against the baseline roster |
| `m0_differential/capture.py` | read one binary's structural surface by running it |
| `m0_differential/diff.py` | compare two structural surfaces |
| `m0_differential/plants.py` | the planted changes behind `--selftest` |
| `m0_differential/report.py` | render the verdict |
