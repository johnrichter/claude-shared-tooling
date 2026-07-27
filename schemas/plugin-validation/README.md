# plugin-validation — the capability manifest

Phase 1 of the Go-reimagined `plugin-validation` harness (characterize → deterministic conform → behavioral) is a read of a plugin's own files that produces one document: the capability manifest. This directory is that document's contract. Phase 2 (deterministic conform, $0, no model) and Phase 3 (behavioral, metered) are downstream consumers, not shipped here.

## Layout

| File | Role | Version |
|---|---|---|
| `capability-manifest.schema.json` | **The contract** (JSON Schema 2020-12) — every surface entry's shape, the could-not-determine gap shape, and the coverage accounting fields. | `plugin-validation.capability-manifest@1.0.0` |
| `examples/golden-manifest.json` | A hand-authored, schema-valid manifest for a fixture plugin (`example-plugin`) — illustrates every entry kind: a hook surface with a weak spot, a skill surface, a could-not-determine gap, and populated coverage. Not a characterization of any real shipped plugin. |

## What the manifest carries

- **`surfaces`** — one entry per contributed surface (`command`, `agent`, `skill`, `hook`, `mcp-server`, `statusline`, `output-style`, `other`), each stating its `trigger` (the declared invocation condition) and a `citation` into the plugin's own files. **A surface with no citation is not a valid entry** — the schema requires `citation.path` on every surface, so a claim not traceable to a real file cannot be expressed.
- **`weak_spots`** (per surface) — a concern derived from *reading* the surface, not from running it: each one states its `basis` (the reasoning, not just the conclusion) and carries its own `citation`.
- **`could_not_determine`** — a document-level array, first-class and required (present, possibly empty, never omitted): an aspect the read could not settle, with why (`reason`) and, when the read looked somewhere first, `attempted_citation`. A gap belongs here, never folded into `surfaces` as a guess.
- **`coverage`** — two id sets, `manifest_case_ids` (every case id any surface names) and `executed_case_ids` (case ids a later tier reports as run). `coverage = manifest_case_ids - executed_case_ids` (set difference) is computed by whichever consumer needs it; this document stores both sides of the subtraction, never the computed result. `executed_case_ids` is empty at manifest-generation time and is written back as Phase 2/3 tiers run.

## Validation

```sh
python3 -m venv .venv && .venv/bin/pip install jsonschema==4.26.0
.venv/bin/python -m jsonschema --output plain -i schemas/plugin-validation/examples/golden-manifest.json schemas/plugin-validation/capability-manifest.schema.json
```

Exit `0` and no output on a valid document. Install the validator into an isolated venv, never the system interpreter (`jsonschema` is already pinned in `schemas/invariant-registry/requirements.txt` for this checkout; reuse that pin rather than floating a second version).

## Known limits

- **`citation.path` resolving inside `plugin.path` is a convention, not schema-enforced.** JSON Schema 2020-12 cannot express "every surface citation path is a prefix-match of the sibling `plugin.path` field" without a consumer-side check; the characterization engine (M9.P1.T2) is that check, and a manifest whose citation escapes its own plugin's tree is the engine's defect to catch, not this schema's.
- **`case_ids` / `manifest_case_ids` consistency is a convention, not schema-enforced.** The schema does not cross-validate that every id in a surface's `case_ids` also appears in `coverage.manifest_case_ids` (or vice versa) — same limit as above, same owner.
- **A could-not-determine gap has no required citation.** By definition the read could not establish the fact the citation would support; `attempted_citation` is optional because some gaps (e.g. a referenced file absent from the checkout) have nowhere to point.

## Versioning

A language-agnostic contract module: it releases as `schemas/plugin-validation/vX.Y.Z` — the tag prefix equals the module's path from the repository root, with no language segment. The schema carries its own `version`/`$id` and a `schema` const embedding the same version, so a consumer pins a contract version rather than a checkout.
