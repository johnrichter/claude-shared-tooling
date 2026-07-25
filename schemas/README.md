# schemas — generalized, adoptable definitions

Home for **non-proprietary, non-personal schema and format definitions** others can adopt as a best practice, plus the tooling that reads/validates/edits them. Living here (not in a consuming plugin) keeps every dependency **one-way**: consumers depend on the schema; the schema depends on nothing.

## Contents

- **`model-roster/`** — the source of record for the selectable Claude model set: a JSON Schema 2020-12 contract plus the one authored roster. Every consumer reads it at runtime or is generated from it and currency-checked. See its `README.md`.

## Planned contents

- **Frontmatter tagging schema (definition)** — the single-source, parameterizable encoding of the tag-namespace validity rules, required-field list, and exempt taxonomy. A rule-lint tool and an audit validator become one-way consumers of it.
- **Frontmatter CRUD tooling** *(to build)* — stdlib-only create/read/update/delete operations over a file's YAML frontmatter (add/remove/retag, validate against the schema, bulk-apply). The tooling half of the "adoptable frontmatter best practice" deliverable.
