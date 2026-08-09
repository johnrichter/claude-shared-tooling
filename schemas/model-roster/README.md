# Model roster (declarative, adoptable)

The single source of record for the selectable Claude model set. Every consumer either **reads this file at runtime** or is **generated from it and currency-checked** — nothing hand-maintains a second copy of the model list, the price table, the effort tables, or the capability ordering.

## Layout

| File | Role |
|---|---|
| `model-roster.schema.json` | JSON Schema 2020-12. Carries the normative contract in its `description`: inputs, every projection a consumer derives, and the invariants (ordering, the two selectability axes, null semantics, forward-refusal, canonical form). Schema `version` is `1.1.0`; its MAJOR is the roster's `_schema_version`. |
| `model-roster.json` | The authored roster — one row per pinned full model ID, keyed by that ID, plus the document-level provenance and the effort-exemption sentinels. The only file a refresh edits. |

## Validation

```
check-jsonschema --schemafile model-roster.schema.json model-roster.json   # any 2020-12 validator; install in an isolated env
python3 -m json.tool model-roster.json >/dev/null                          # well-formedness, zero deps
```

## Canonical form (byte-stable regenerate-and-diff)

`model-roster.json` is canonical JSON: keys sorted, two-space indent, LF endings, one trailing newline, no non-ASCII escaping. Re-emitting the parsed document reproduces the file byte for byte, so a currency check is a plain diff:

```
python3 -c 'import json,sys;d=json.load(open("model-roster.json"));sys.stdout.write(json.dumps(d,sort_keys=True,indent=2,ensure_ascii=False)+"\n")' | diff - model-roster.json
```

The schema file is hand-ordered for readability and is not subject to that rule — it is authored, never generated.

## Consuming it

- **Look up a row** by bare pinned ID. Strip a trailing `-YYYYMMDD` snapshot suffix and any `[1m]` window selector first; neither ever appears in a key.
- **No row** (or no readable roster) is the distinct `roster-stale` outcome naming the refresh action — never "below floor", never "unsupported", never a deny-all.
- **Compare capability** within a family by comparing `generation` element-wise; across families only by `cross_family_rank`, and only when both rows declare one. Everything else is roster-stale.
- **Project, don't enumerate** — the plan-schema model enum, the authoring gate's allowlist, the effort tables, the exemption list and the rate table are each one predicate over the rows, spelled out in the schema `description`.
- `lifecycle` (vendor state) and `selectable` (selection policy) are independent; reading one for the other silently picks the wrong consumer's answer.

## Refreshing it

A released model missing a row is detected, not discovered by a failing gate. The proposed row is emitted for review; a human-approved commit to this directory is the only write, and every derived artifact regenerates from it and fails CI when stale. Fill `_as_of`, extend `_source` with the fetch date and the pages it covers, and leave a vendor fact `null` rather than guessing it — a null means "not sourced", and a consumer that needs it resolves roster-stale. A cross-family ordering claim needs a citation or an explicit `inherited_pairs` entry.
