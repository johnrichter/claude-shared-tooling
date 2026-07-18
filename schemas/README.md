# schemas — generalized, adoptable definitions

Home for **non-proprietary, non-personal schema and format definitions** others can adopt as a best practice, plus the tooling that reads/validates/edits them. Living here (not in a consuming plugin) keeps every dependency **one-way**: consumers depend on the schema; the schema depends on nothing.

## Planned contents

- **Frontmatter tagging schema (definition)** — the single-source, parameterizable encoding of the tag-namespace validity rules, required-field list, and exempt taxonomy. A rule-lint tool and an audit validator become one-way consumers of it.
- **Frontmatter CRUD tooling** *(to build)* — stdlib-only create/read/update/delete operations over a file's YAML frontmatter (add/remove/retag, validate against the schema, bulk-apply). The tooling half of the "adoptable frontmatter best practice" deliverable.

Until these land, this directory is a placeholder that fixes the location and the routing contract.
