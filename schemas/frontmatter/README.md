# Frontmatter validation schema (declarative, adoptable)

The single, data-only source of the workspace frontmatter tagging rules — externalized from the imperative `audit_helper/schema.py` so a Rust validator (navigator, M2.P2.T1b) **and** the stdlib Python gate (`check_rules.py` + the two `audit_helper` copies, M2.P2.T3/T4) both interpret **one** file and agree by construction (SC6 parity). No code encodes the rules; the validators are generic interpreters of this data.

## Layout — core profile + extension pack

| File | Role | Version | Adopt |
|---|---|---|---|
| `frontmatter-core.schema.json` | **Core profile** — MECHANISMS only (cardinality vocabulary, the ordered tag-rule cascade + violation codes/fields/message templates, the required-field / description-cap / file-class / exempt mechanisms). Workspace-agnostic; no concrete names/caps/globs. | `core@1` | Adopt unchanged; embedded in the navigator binary at build (M2.P2.T1b) as the bundled default. |
| `frontmatter-psa-apm.pack.json` | **Extension pack** — the psa-apm CONCRETE vocabulary (the six required fields + authorship, caps, ordered namespaces, report rules + period regex, file-class globs, exempt taxonomy). `extends: core@1`. | `psa-apm@1` | Replace with your own pack; declare it in `navigator.toml` (M3.P2.T1). |
| `frontmatter-profile.meta.schema.json` | **Meta-schema** (JSON Schema 2020-12) — both files validate against it (the M2.P1.T1 acceptance). | — | Reused as-is. |
| `DIVERGENCES.md` | The reviewed deltas vs `schema.py` (owner-singleton, description_caps, file_class, authorship; all code strings now confirmed against the live emitter). Input to the M2.P2.T2 frozen-fixture baseline. | — | — |

**Why split core/pack:** the validator logic (cascade order, codes) is universal; only the vocabulary is workspace-specific. A foreign repo adopts `core@N` (bundled, unchanged) and ships its own `<name>@1` pack — no fork of the mechanisms. The `version`/`extends` strings (`core@1`, `psa-apm@1`) let the M3 sentinel pin a profile+pack pair.

**Why JSON:** tag-cardinality rules aren't expressible in vanilla JSON Schema, so the profile/pack are a purpose-built declarative doc — but still JSON so Rust (`serde_json`) and the **stdlib-only** Python gate (`json`) both read it with zero extra dependencies. The meta-schema is standard JSON Schema.

## Self-validation

Both data files validate against the meta-schema with any JSON Schema 2020-12 validator:

```
python -m jsonschema -i frontmatter-core.schema.json      frontmatter-profile.meta.schema.json
python -m jsonschema -i frontmatter-psa-apm.pack.json      frontmatter-profile.meta.schema.json
```

Well-formedness (valid JSON) is checkable with zero deps: `python3 -m json.tool <file> >/dev/null`.

## Interpreter contract (for M2.P2.T1b / T3)

- Iterate `core.violation_cascade` in **array order**; within each step iterate the pack's namespace/field arrays in **their** declared order. Order is significant — it reproduces `schema.py`'s emission sequence for SC6 parity.
- Emit `{code, field, message}` per the cascade step's templates (placeholders: `{namespace}`, `{count}`, `{tags}`, `{child}`, `{parent}`, `{period_tag}`, `{period_namespace}`).
- **Placeholder rendering (byte-identical contract):** substitute the runtime value verbatim. `{count}` is the integer. `{tags}` renders as a **Python list literal of the offending tag strings** (single-quoted, comma-space separated, e.g. `['type:a', 'type:b']`) — the live emitter interpolates `namespaces[ns]`, a `list[str]`, so a non-Python interpreter MUST reproduce that exact `repr` form.
- Run `independent_checks` (exempt gate → required fields → description cap → file-class) outside the tag cascade, as `schema.py` keeps completeness separate. Their two emitting checks carry message templates on `core.mechanisms.required_fields.message` and `core.mechanisms.description_caps.message` (placeholders: `{field}` for the missing field; `{length}` = `len(description)`, `{file_class}` = the derived class string, `{cap}` = the integer cap). Render these the same way for byte-identical output.
