# agentcontract

Lints an agent-brief roster for two things: a complete discriminator matrix, and the
mechanically-checkable half of the §Agent instruction & return contract (see the toolbelt
design doc). Full rule rationale lives in the package doc (`doc.go`); this file is the format
reference for authoring a brief this lint accepts.

## Roster convention

A roster is every `*.md` file directly inside one directory literally named `agents` — the
convention every plugin here already uses for Claude Code subagents. The sibling set for
completeness is that directory listing minus the brief itself, always — a brief's own
frontmatter can never narrow or substitute it.

## Frontmatter format

Alongside the ordinary Claude Code subagent keys (`name`, `description`, `tools`, `model`,
`effort`), a brief carries a `contract:` block:

```yaml
contract:
  output_schema: path/to/output.schema.json   # resolved vs. the brief's dir, then vs. -schema-root
  edit_proposing: false                       # true brings the FB11 requirement into scope
  large_artifact: false                       # true brings the FB3 requirement into scope
  decisions:
    - name: some-decision-name
      statement: "the single canonical wording of this rule"
  failure_paths:
    - name: some-failure-name
      action: "the terminating action: stop / fall back to X / report and continue"
  discriminators:
    other-agent-name:
      relation: discriminator      # or: not-confusable
      reason: "what tells the two agents apart, or why no discriminator is needed"
      fuzzy: false                 # true requires tie_break below
      tie_break: ""
```

- `output_schema` must be a whitespace-free path ending `.json`/`.yaml`/`.yml` that resolves to
  a real file — not prose describing the shape of the output.
- `discriminators` needs one entry per roster sibling, no more, no fewer; a name outside the
  roster is flagged stale, a missing sibling is a lint failure.
- A decision's `statement` is declared once here; body prose refers to it by `name` (e.g. an
  inline code span) rather than restating the wording.
- `edit_proposing: true` requires, in both the body prose and the referenced output schema, a
  per-edit field naming the other locations asserting the same claim, with an explicit `none`
  reading. The canonical schema field name this lint looks for is
  `other_locations_asserting_claim`, required on the edit object.
- `large_artifact: true` requires the write-bounded-fragments-to-disk / assemble / validate
  rule to be stated (as a decision, in prose, or both) and forbids the superseded
  split-across-dispatches phrasing; stating both is a one-rule-per-decision failure.

## Running it

```
go run ./cmd/agentcontract-lint [-schema-root DIR ...] [ROOT ...]
```

`ROOT` defaults to `.`. `-schema-root` may be repeated; each is tried, after the brief's own
directory, when resolving `output_schema`. Exit 0 clean, 1 one or more findings, 2 usage/IO
error (including any brief that fails to parse — a broken brief is never silently skipped).

## What this lint does not certify

It proves the matrix is **complete** and the named properties are **structurally present** —
not that any cell or decision is **good**. `Report.ReviewerChecked` states this limit
explicitly on every run, pass or fail: discriminator/tie-break/FB11-field *quality*, and
one-rule-per-decision judgment beyond the specific patterns this package matches, stay a
reviewer's job.
