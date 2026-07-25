# roster-gen — the one-rendering-pass generator

Renders every model-roster-derived output from a single input, in one pass: the model roster (`schemas/model-roster/model-roster.json`, at whatever tag it's checked out at) is this tool's *only* input. Stdlib-only, no install: run it from a checkout.

Never hand-edit an output — edit the roster and regenerate. Every output carries a generated-by header naming the roster tag; that's the tell if a copy has drifted.

## Outputs

| Output | Derivation |
|---|---|
| both `anthropic-specifications.json` copies (identical) | every roster row with a non-null price (contract preferred over list), `capability_order()` |
| plan-schema.json's `model.enum` | every `selectable == 'new-work'` row in `PLAN_ENUM_ORDER`, plus the roster's `effort_exempt_sentinels` |
| build-engine.workflow.js's `DEFAULT_RATES` | every priced row's list output rate, `capability_order()` |
| hooks/model-roster (the model-gate allowlist) | every `selectable != 'retired'` row in `GATE_ALLOWLIST_ORDER` (sentinels excluded — a sentinel is not a model) |
| governance-model-tiering.md's capability snapshot table | every `selectable != 'retired'` row, `capability_order()` |

The plan-enum and gate-allowlist projections are deliberately different sets (new-work-only vs. everything-but-retired) — a generator collapsing them to one list would reintroduce the drift this tool exists to remove.

## Ordering

`capability_order()` (`roster_gen/order.py`) ranks by `cross_family_rank` across families and `generation` within one — fully derived from the roster, no hand-maintained list.

The plan-enum and gate-allowlist orders are historically hand-curated and have no single roster-field formula that reproduces both (their relative orderings of shared IDs conflict under every derivable sort tried: `cross_family_rank`, price, release date, family+generation). `PLAN_ENUM_ORDER` / `GATE_ALLOWLIST_ORDER` pin those two orders as generator config. A roster ID either table doesn't mention is appended rather than dropped, so a new row is never silently lost — just placed at the tail until the order table is updated.

## Safety

- **No narrowing**: regenerating the gate allowlist against an existing on-disk copy fails loudly (exit 2) if any currently-allowed ID would be dropped.
- **Sentinel presence**: the plan-schema enum build fails loudly if a REQUIRED sentinel (`inherit`) would be missing from the result — checked against a fixed requirement, not against the roster's own `effort_exempt_sentinels`, so a roster edit that drops it there doesn't silently break the enum.
- **Forward version refusal**: a roster declaring a newer `_schema_version` than this tool understands is refused rather than guessed at.

## Usage

```sh
python3 tooling/roster-gen/generate.py \
  --roster schemas/model-roster/model-roster.json --tag v1.2.3 \
  --ai-shared-lib-root . --marketplace-root ../marketplace \
  generate

python3 tooling/roster-gen/generate.py --roster ... --tag ... --ai-shared-lib-root ... --marketplace-root ... check
```

`generate` renders and writes every output. `check` renders in memory and diffs against disk without writing — exit 1 on drift or a missing target; use it as a CI drift guard.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | every target generated/verified |
| 1 | (`check` only) a target drifted from the roster-derived rendering, or is missing |
| 2 | a roster or rendering error — narrowing the gate allowlist, an unsupported roster schema version, a patch target that doesn't exist yet |
