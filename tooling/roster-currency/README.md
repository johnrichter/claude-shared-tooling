# roster-currency — CI staleness gate for roster-derived artifacts

`check.py` regenerates every roster-derived artifact this repo commits (today: just
`go/anthropic-specifications.json`) from `schemas/model-roster/model-roster.json` at HEAD,
using the tag already recorded in that artifact's own `_generated_by` header, and diffs the
result against what's on disk. Any difference fails.

Stdlib-only; imports `tooling/roster-gen`'s render functions directly (no subprocess, no
install) so it renders from the exact roster and target definitions roster-gen itself uses.

## Why regenerate-and-diff, not a set-membership check

The model-set drift class this replaces used to be caught by diffing ID *sets* across
independently hand-scraped sources. Regenerate-and-diff is strictly stronger: it catches a
changed *value* (a price, a capability flag) on an ID already present everywhere, not just a
missing or extra ID, and it traces every failure to the one input that fixes it — the roster
— rather than to "one of these four files disagrees with the others."

## Both staleness directions

- **Hand-edited artifact**: the roster didn't change, but the artifact's content no longer
  matches rendering its own recorded tag — someone edited the output instead of the roster.
- **Roster changed, artifact didn't**: the artifact's recorded tag still parses fine, but
  rendering the *current* roster at that tag no longer reproduces the artifact, because a row
  it projects changed underneath it — someone edited the roster and forgot to regenerate.

Both are exit 1.

## Scope

This script covers only the artifacts committed in *this* repo. The marketplace-side
artifacts (`plan-schema.json`'s `model.enum`, `build-engine.workflow.js`'s `DEFAULT_RATES`,
the model-gate allowlist, the tiering doc's capability table) are this same roster's derived
output too, but they live in the marketplace repo and are currency-checked by that repo's own
CI job against the same roster file.

## Usage

```sh
python3 tooling/roster-currency/check.py
```

Exit codes: `0` every owned artifact is current; `1` one or more drifted, is missing, or
carries no recognizable generated-by tag; `2` the roster itself failed to load.

## CI wiring

`.github/workflows/ci.yml`'s `roster-currency` job runs this on every push/PR, required
alongside the other CI jobs — a stale or hand-edited artifact blocks merge.
