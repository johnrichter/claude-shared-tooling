# Frontmatter validation schema (declarative, adoptable)

The single, data-only source of the frontmatter tagging rules — a Rust validator (embedded in navigator) and a stdlib Python reader both interpret **one** file and agree by construction. No code encodes the rules; the validators are generic interpreters of this data.

## Layout — core profile + extension pack

| File | Role | Version | Adopt |
|---|---|---|---|
| `frontmatter-core.schema.json` | **Core profile** — MECHANISMS only (cardinality vocabulary, the ordered tag-rule cascade + violation codes/fields/message templates, the general conditional `rule_sets` `{match, apply}` mechanism, the required-field / description-cap / file-class / exempt mechanisms). Workspace-agnostic; no concrete names/caps/globs. | `core@2.0.0` | Adopt unchanged; embedded in the navigator binary at build. |
| `frontmatter-default.pack.json` | **Extension pack** — a generic, workspace-agnostic vocabulary (six required fields, description caps, `type`/`status`/`topic`, file-class globs, a small exempt taxonomy). Generic-minus-report: carries NO report vocabulary and NO conditional rule-sets. `extends: core@2.0.0`. | `default@1.0.0` | Adopt as-is, or replace with your own generic pack. |
| `frontmatter-reports.pack.json` | **Extension pack** — the opt-in, isolated report bundle: the four report namespaces (`source`/`period`/`audience`/`cadence`) + ONE `{match, apply}` rule-set matching `type:report` (requires `source`/`period` on a report file; forbids all four off one; format- and calendar-checks `period`). Layer on top of a base pack that supplies `type` (e.g. `default@1.0.0`); omit if a repo needs no report semantics. `extends: core@2.0.0`. | `reports@1.0.0` | Layer after your base pack, or omit entirely. |
| `frontmatter-profile.meta.schema.json` | **Meta-schema** (JSON Schema 2020-12) — every pack/profile file validates against it. | — | Reused as-is. |

**Why split core/pack:** the validator logic (cascade order, codes) is universal; only the vocabulary is workspace-specific. A foreign repo adopts `core@N.0.0` (bundled, unchanged) and ships its own `<name>@1.0.0` pack — no fork of the mechanisms. The `version`/`extends` strings (`core@2.0.0`, `default@1.0.0`) let a consumer pin a profile+pack pair.

**Why JSON:** tag-cardinality rules aren't expressible in vanilla JSON Schema, so the profile/pack are a purpose-built declarative doc — but still JSON so Rust (`serde_json`) and the **stdlib-only** Python gate (`json`) both read it with zero extra dependencies. The meta-schema is standard JSON Schema.

## Self-validation

Every profile/pack file validates against the meta-schema with any JSON Schema 2020-12 validator:

```
python -m jsonschema -i frontmatter-core.schema.json      frontmatter-profile.meta.schema.json
python -m jsonschema -i frontmatter-default.pack.json      frontmatter-profile.meta.schema.json
python -m jsonschema -i frontmatter-reports.pack.json      frontmatter-profile.meta.schema.json
```

Well-formedness (valid JSON) is checkable with zero deps: `python3 -m json.tool <file> >/dev/null`.

## Interpreter contract

- Iterate `core.violation_cascade` in **array order**; within each step iterate the pack's namespace/field arrays in **their** declared order. Order is significant — it determines the emitted violation sequence deterministically.
- Emit `{code, field, message}` per the cascade step's templates (placeholders: `{namespace}`, `{count}`, `{tags}`, `{child}`, `{parent}`, and `{match}` = a rule-set's `namespace:value`). A rule-set `value_formats[]` failure renders the pack-supplied `message` (placeholder `{value}` = the whole offending tag); the code comes from the `rule_set_value_format` cascade step.
- A pack namespace may carry `allowed_values` (`{values, message}`) — a closed vocabulary checked **unconditionally**, outside the cascade and outside `rule_sets`: every file carrying a tag in that namespace must use one of `values` (compared against the part of the tag after its first `:`), with no match criterion gating it. A mismatch emits the fixed code `TAG_VALUE_NOT_ALLOWED` with `field` = the namespace name and the pack-supplied `message` (placeholders `{value}` = the whole offending tag, `{namespace}` = the namespace name). Run it after the cascade, so the cascade's own emission order is unaffected. A namespace without the key is unchecked — no interpreter behavior changes for a pack that omits it. **Not** Tier-1 fixable: a fixer must not guess a replacement from a closed vocabulary, so a bad value (including a `<namespace>:TODO` stub a fixer added for a required namespace) stays for a human to resolve.
- **Placeholder rendering (byte-identical contract):** substitute the runtime value verbatim. `{count}` is the integer. `{tags}` renders as a **Python list literal of the offending tag strings** (single-quoted, comma-space separated, e.g. `['type:a', 'type:b']`) — the live emitter interpolates `namespaces[ns]`, a `list[str]`, so a non-Python interpreter MUST reproduce that exact `repr` form.
- Run `independent_checks` (exempt gate → required fields → description cap → file-class) outside the tag cascade — completeness checks are independent of it. Their two emitting checks carry message templates on `core.mechanisms.required_fields.message` and `core.mechanisms.description_caps.message` (placeholders: `{field}` for the missing field; `{length}` = `len(description)`, `{file_class}` = the derived class string, `{cap}` = the integer cap). Render these the same way for byte-identical output.
