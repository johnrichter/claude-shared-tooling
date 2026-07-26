# invariant-lint — SC-ENFORCE: rung 1 and rung 3 are verified, not declared

Two of the invariant registry's five rungs (`schemas/invariant-registry`) carry a machine
consumer instead of a completeness check alone. This is that consumer. It asserts every
`shipped` rung-1 and rung-3 entry against the tree and the running suite, not merely that
the entry's fields are present and well-shaped — that is `schemas/invariant-registry/check.py`'s
job, and this tool deliberately does not repeat it.

## What it checks

**Rung 1 — `fail_fast_symbol` (`<repo-qualified file>:<symbol>`).** The named symbol must
resolve as a real definition in the named file: a Go func/method/type/var/const, a Rust
fn/struct/enum/trait/const/static, or a Python def/class (a dotted symbol navigates into a
class body). Any other extension falls back to a comment-stripped whole-word search. A
symbol that only appears in a comment never counts — every resolver is anchored to a
definition, not a substring. A renamed, moved, or deleted symbol fails; so does a file that
no longer exists.

**Rung 3 — `test_id` (`<repo-qualified test file>::Class[::method]`).** The named test is
loaded through `unittest.TestLoader` and run in a fresh subprocess. A selector that doesn't
resolve to a real class or method loads as `unittest.loader._FailedTest` instead of raising,
which is how this tool tells "doesn't exist" apart from "exists and failed" — both existence
and skip status come from one executed result, not from parsing text output. A test that
resolves but is skipped (conditionally or otherwise) or resolves to nothing fails: a skipped
check enforces nothing regardless of why it was skipped.

Currently Python/`unittest` is the only suite type resolved for rung 3 (every rung-3 entry in
the seed points at one). A `test_id` naming a non-`.py` file fails closed rather than passing
silently, so a future non-Python suite is a declared gap, not an unnoticed one.

**Not this tool's job:** registry schema validity, restatement, gate-path resolution, and gate
completeness — `schemas/invariant-registry/check.py`. Rung 2's declared-vs-actual gate firing —
`ai-shared-lib/go/gate`. Rung 4 and rung 5 have no machine consumer at all.

## Path resolution

Every declared path is repo-qualified (`ai-shared-lib/...`, `marketplace/...`); the first
segment names the checkout. This repo resolves to its own root; any other repo defaults to a
sibling directory of it, overridable with `--repo NAME=PATH`. A repo that is not present is
reported `NOT CHECKED` and drives no verdict — a sibling nobody cloned is a checkout condition,
not a signal, same as the registry lint's own rule. No rung-1 or rung-3 entry in the seed
currently names a repo other than this one.

## Running it

```sh
python3 tooling/invariant-lint/check.py
python3 tooling/invariant-lint/check.py --repo marketplace=../marketplace
python3 tooling/invariant-lint/check.py --require-roots   # every named repo must resolve
```

Stdlib only — no venv needed to run the lint itself. The rung-3 subprocess re-executes the
target test module, so any dependency that test needs (e.g. the pinned schema validator for
`tests/test_invariant_registry.py`) must already be installed in the interpreter this script
runs under, same as the ordinary unit-test job.

Exit codes: `0` every shipped entry resolves and runs clean; `1` one or more do not; `2` the
harness itself could not run (unreadable registry, bad arguments — never a silent pass).

## Known limits

- **Go/Rust resolution is regex-anchored to a declaration line, not a parser.** A definition
  split by a `/* ... */` block comment spanning the declaring line is not resolved; this has
  not occurred in the seed. Python resolution is exact (AST-based).
- **A dotted symbol navigates a Python class body only.** Go and Rust names in the seed are
  never dotted; a dotted symbol against either falls through the resolver as a literal
  (near-certain non-match), which is a fail-closed outcome for a case that has not shipped.
- **Rung-3 resolution is Python/`unittest`-only.** A future non-Python suite needs its own
  resolver added here before its rung-3 entries can ship.

## Layout

| file | role |
| --- | --- |
| `check.py` | CLI entry point |
| `invariant_lint/paths.py` | repo-qualified path resolution |
| `invariant_lint/symbols.py` | rung-1: symbol resolution per source language |
| `invariant_lint/testrun.py` | rung-3: test-id existence and run/skip resolution |
| `invariant_lint/cli.py` | orchestration: load registry, run both checks, report |
