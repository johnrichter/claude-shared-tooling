# invariant-registry — every enforcement invariant declares its rung and its fail direction

An invariant that lives only in a script header is not governance: nobody can ask which rung actually enforces it, which way it fails, or why it isn't stronger. This directory is the one place those answers live as data, and the place a consumer reads them from.

## Layout

| File | Role | Version |
|---|---|---|
| `invariant-registry.schema.json` | **The contract** (JSON Schema 2020-12) — entry shape, the per-rung consumer field, and `x-verification-model`: the normative statement of what is actually checked at each rung. | `invariant-registry@1.0.0` |
| `invariant-registry.json` | **The registry** — the seed entries, the gate map every entry keys into, and the discovery roots the completeness check reads. | — |
| `check.py` | **The registry lint** — validity, unique ids, no restatement, path resolution, published-declaration agreement, and gate completeness. | — |

## The ladder

Pick the strongest rung the invariant can actually reach, and record why a stronger one is out of reach whenever it is weaker than a deny gate.

| Rung | Mechanism | Consumer field | Who checks it |
|---|---|---|---|
| 1 | impossible in code — a fail-fast where the value is produced | `fail_fast_symbol` (`file:symbol`) | `ai-shared-lib/tooling/invariant-lint` resolves the symbol |
| 2 | deny gate — the tool call is refused | `trigger` + `gate_id` | `ai-shared-lib/go/gate` exposes the declared trigger; `bandcheck` compares it against actual firing |
| 3 | a check in the shipped suite | `test_id` | `ai-shared-lib/tooling/invariant-lint` asserts it exists and runs |
| 4 | advisory with a measured compliance rate | `compliance_floors` + `measurement_status` + `register_entry_id` | nobody — completeness only |
| 5 | doc that shapes the approach before any gated call | `doc_path` | nobody — completeness only |

**Three of five rungs are verified; two are not.** Rungs 4 and 5 are checked for the presence and shape of their fields and nothing more. A complete rung-4 entry means a floor has been declared, never that anything honors it. That limit is declared in the schema itself (`x-verification-model`, plus `x-verification` on every field) so no reader has to infer it from a green run.

**The reason is prose.** `reason_lower_rung` is judged by a reviewer; no consumer checks it and none can. Its value is that the reason is queryable in one place instead of buried in a script header. The field is annotated `reviewer-checked` so a passing lint is never mistaken for a validated reason.

## Rules that make the registry more than a list

- **Declare an invariant once.** A weaker rung covering ground a stronger rung already holds points at that rung's target by path in `references`. Restating it instead fails the lint. The worked example is the comment policy: rung 2 refuses the call that adds a bad comment, rung 3 catches whatever arrives by another route, and rung 5 states the policy where the author reads it before writing anything — three entries, one invariant, each pointing at the stronger targets rather than repeating them.
- **Fail direction is per invariant, chosen by blast radius.** Fail closed when the wrong answer costs work that cannot be reconstructed; fail open when it costs a re-run. Neither is a default, and both sides of the cost belong in `blast_radius`.
- **A missing data artifact is a packaging defect, not a signal.** It drives no verdict in either direction — in a gate, and in this lint: a discovery root whose checkout is absent is reported `NOT CHECKED`, never passed.
- **A consumer asserts a `shipped` entry only.** A `planned` entry's consumer field names the intended target and is reported unasserted; the task that ships the target flips the status in the same change. A `retired` entry is history.
- **A rung-4 floor value is null until it is measured.** The behavioral tier's first calibration run sets it; declaring the floor with a null value and `declared-unmeasured` is the specified interim state. `measured` requires a real number and a measured rate for every model declared.

## Completeness is discovered, not asserted

`check.py` finds gates through the artifact that **runs** them — a plugin's `hooks.json` (PreToolUse hooks only: nothing else can deny), a CI workflow file (a guard no workflow runs gates nothing) — and names any in-scope gate carrying no declaration. A hand-kept list of gate paths would be a second place the set could drift, which is the defect this closes rather than repeats.

Scope is per root: a gate owned by a plugin outside `in_scope_owners` is reported as **unclaimed** and fails nothing, so the incumbent surface stays visible without an unbounded retroactive backfill. Promoting one is a two-line change: add its owner to the root, add its entries.

A gate may also publish its own declaration between `INVARIANT-REGISTRY-BEGIN` / `INVARIANT-REGISTRY-END` marker lines in its header, where a reader of that gate sees it. The lint compares every published entry against the registry, so the two copies cannot drift.

## Seed scope

The seed carries every invariant with a **named artifact**: the gates in the plugins this build ships or edits, the required CI guards in this repository, the advisories and docs those gates degrade to, and the planned gates whose mechanism and home the design already fixes. It is deliberately not a pre-population of every invariant a later milestone might ship — a speculative entry pointing at an invented symbol is the bureaucracy this registry exists to avoid. Growth is enforced instead: a task shipping a gate adds its entry in the same change, and the completeness check names it if it does not.

## Running it

```sh
python3 -m venv .venv && .venv/bin/pip install -r schemas/invariant-registry/requirements.txt
.venv/bin/python schemas/invariant-registry/check.py                          # this checkout only
.venv/bin/python schemas/invariant-registry/check.py --repo marketplace=../marketplace
.venv/bin/python schemas/invariant-registry/check.py --require-roots          # every root must resolve
```

Exit codes: `0` clean, `1` violations (one line each), `2` the harness could not run — an unreadable registry, or the pinned validator missing. Never a silent pass.

Install the validator into an isolated venv, never the system interpreter. It is pinned in `requirements.txt` and recorded in `LICENSE-3rdparty.csv` with its transitive set.

## Known limits

- **Restatement detection is word overlap, not meaning.** Threshold 0.50, set between two measured bands on the seed: the most similar pair of genuinely distinct invariants scores 0.40, while restatements score 1.00 verbatim and 0.55–0.70 reworded. A heavily reworded duplicate scores as low as 0.07 and reaches the reviewer instead — the check removes the easy half and claims nothing about the other half.
- **CI-wired-guard discovery is a text scan** of workflow files, not a YAML-semantic one: a guard path that appears anywhere in a workflow counts as run by it.
- **Path resolution is per checkout.** Paths are repo-qualified from the platform root; a repo defaults to a sibling directory of this checkout and `--repo NAME=PATH` overrides it. In CI, where only this repository is checked out, the other roots report `NOT CHECKED`.

## Versioning

A language-agnostic contract module: it releases as `schemas/invariant-registry/vX.Y.Z` — the tag prefix equals the module's path from the repository root, with no language segment. The schema carries its own `version`/`$id`, so a consumer pins a contract version rather than a checkout.
