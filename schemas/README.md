# schemas — generalized, adoptable definitions

Home for **non-proprietary, non-personal schema and format definitions** others can adopt as a best practice, plus the tooling that reads/validates/edits them. Living here (not in a consuming plugin) keeps every dependency **one-way**: consumers depend on the schema; the schema depends on nothing.

## Contents

- **`model-roster/`** — the source of record for the selectable Claude model set: a JSON Schema 2020-12 contract plus the one authored roster. Every consumer reads it at runtime or is generated from it and currency-checked. See its `README.md`.
- **`invariant-registry/`** — the source of record for every enforcement invariant: its rung on the enforcement ladder, its fail direction and the blast radius that chose it, and the per-rung field a named consumer verifies. Ships its own lint for the properties that are internal to the registry — validity, no restatement, gate completeness. See its `README.md`.
- **`plugin-validation/`** — the capability-manifest contract: `plugin-validation`'s Phase-1 (deep characterization) output. Every contributed surface cited to a real plugin file, could-not-determine gaps as first-class entries, and the coverage-accounting fields (`manifest_case_ids` / `executed_case_ids`) a downstream tier computes `coverage = manifest - executed` from. See its `README.md`.

## Planned contents

- **Frontmatter tagging schema (definition)** — the single-source, parameterizable encoding of the tag-namespace validity rules, required-field list, and exempt taxonomy. A rule-lint tool and an audit validator become one-way consumers of it.
- **Frontmatter CRUD tooling** *(to build)* — stdlib-only create/read/update/delete operations over a file's YAML frontmatter (add/remove/retag, validate against the schema, bulk-apply). The tooling half of the "adoptable frontmatter best practice" deliverable.
