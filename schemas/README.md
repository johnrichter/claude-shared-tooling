# schemas — generalized, adoptable definitions

Home for **non-proprietary, non-personal schema and format definitions** others can adopt as a best practice, plus the tooling that reads/validates/edits them. Living here (not in a consuming plugin) keeps every dependency **one-way**: consumers depend on the schema; the schema depends on nothing.

## Routing (what belongs here vs. the private lib)

- **Here (public):** standardized, generic definitions — e.g. email and RSS/sitemap formats, and the **workspace frontmatter tagging schema** (the `type:`/`topic:`/`privacy:`/`owner:`/… namespace rules, required fields, and generated/exempt taxonomy), generalized so any workspace can adopt it.
- **Private shared lib:** org-specific or proprietary definitions — e.g. a vendor-specific release-notes schema.

## Planned contents

- **Frontmatter tagging schema (definition)** — the single-source, parameterizable encoding of the tag-namespace validity rules, required-field list, and exempt taxonomy. Generalized from the workspace's `audit_helper.schema`; the workspace's rule-lint and audit validator become one-way consumers of it (repo-split Phase 3).
- **Frontmatter CRUD tooling** *(to build)* — stdlib-only create/read/update/delete operations over a file's YAML frontmatter (add/remove/retag, validate against the schema, bulk-apply). The tooling half of the "adoptable frontmatter best practice" deliverable.

Until these land, this directory is a placeholder that fixes the location and the routing contract.
