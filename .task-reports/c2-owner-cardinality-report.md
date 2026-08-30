---
name: C2 owner cardinality — real fix report
description: "Completes the C2 fix left half-done by an earlier task: adds an at_most_one cardinality to the frontmatter schema/crate, applies it to owner in the real datadog-psa pack, reconciles workspace's vendored copy, fixes the README, cross-links the scan-gap doc, and adds a schema-drift test."
id: doc:ai-shared-lib:c2-owner-cardinality-report
tags:
  - type:knowledge
  - topic:frontmatter
  - topic:git-governance
  - status:complete
  - privacy:internal
  - owner:operator
links: []
updated: 2026-08-30T00:00:00Z
---

# C2 owner cardinality — real fix, task report

STATUS: DONE — all 8 items completed on `chore/c2-owner-cardinality` in all three
worktrees. Branches are NOT merged to main, NOT tagged, NOT pushed (orchestrator's
call per the dispatch contract).

## 1. New cardinality value in schema + crate

`ai-shared-lib` (worktree `c2-owner-cardinality`):

- `schemas/frontmatter/frontmatter-core.schema.json` — added `at_most_one` to
  `cardinality_types` (min 0, max 1); added the `at_most_one_cardinality`
  cascade step (emits `MULTIPLE_SINGLE_VALUE_TAGS` only when count > 1, never
  on absence); broadened the `MULTIPLE_SINGLE_VALUE_TAGS` code's `meaning`/
  `source` to cover both `singleton` and `at_most_one`.
- `schemas/frontmatter/frontmatter-profile.meta.schema.json` — added
  `"at_most_one"` to the `namespaces[].cardinality` enum.
- `rust/frontmatter/src/profile.rs` — added `Cardinality::AtMostOne`.
- `rust/frontmatter/src/validate.rs` — added `at_most_one_cardinality_phase`
  (zero occurrences: no violation; two or more: `MULTIPLE_SINGLE_VALUE_TAGS`),
  wired into `tag_rule_violations` right after `at_least_one_cardinality_phase`.
- `rust/frontmatter/src/fix.rs` — renamed `dedupe_singletons` to
  `dedupe_single_valued` and broadened it to also dedupe `at_most_one`
  namespaces (both cardinalities cap at one value, so both get the same
  Tier-1 repair). `add_required_placeholders` deliberately left untouched:
  `at_most_one` is never required, so no placeholder logic changes.
- `rust/frontmatter/src/test_support.rs` — added a `lead` (`at_most_one`)
  namespace to the shared synthetic test-fixture pack, used by the new tests
  below. Did NOT touch the fixture's existing `owner` (still `singleton`
  there) — changing that would have altered several pre-existing tests'
  cascade-order assertions for no reason; the real-world `owner` fix lives
  only in the real pack (item 3 below), not in this crate's test fixture.
- `rust/frontmatter/src/validate.rs` tests: `at_most_one_namespace_absent_is_not_a_violation`,
  `at_most_one_namespace_with_exactly_one_occurrence_is_not_a_violation`,
  `at_most_one_namespace_with_two_or_more_occurrences_is_a_violation`.
- `rust/frontmatter/src/fix.rs` tests: `at_most_one_namespace_dedupes_to_its_first_occurrence`,
  `at_most_one_namespace_absent_gets_no_placeholder`.

Evidence: `cargo test` in `rust/frontmatter` — **263 passed, 0 failed** (up
from 257 before this task; 6 new tests, one of which is the drift test in
item 7). `cargo fmt --check` and `cargo clippy --all-targets` show no new
diffs/warnings from this task's edits (the pre-existing unrelated fmt/clippy
drift in `profile.rs` predates this branch and was left alone, out of scope).

Also live-verified against the REAL datadog-psa pack (not just the crate's
synthetic fixture) with a throwaway integration test, run and then deleted
(not part of the permanent suite): loading `core@2.0.0` + a minimal base pack
+ the real, edited `frontmatter-datadog-psa.pack.json` —
(a) a file with `privacy:public` and no `owner:` tag produces **zero**
`owner`-field violations, and
(b) the same file with two `owner:` tags produces exactly one violation,
`field: "owner"`, `code: "MULTIPLE_SINGLE_VALUE_TAGS"`.
Both assertions passed.

## 2. Release

NOT tagged. C1 (this crate's prior real change) was tagged as
`rust/frontmatter/v0.1.0` only AFTER its commit had already landed on `main`
(the audit's C4 row: tag `v0.1.0` = commit `0a265b1`, which is on `main`).
This task's work sits on an unmerged branch (`chore/c2-owner-cardinality`),
matching neither C1's own commit-then-tag sequencing nor a state where
tagging would make sense yet. Per the dispatch's own instruction ("if
unsure, land the code and leave tagging to the orchestrator"), no tag was
cut. The orchestrator should tag a new `rust/frontmatter/vX.Y.Z` release
once this branch (and its sibling `ai-shared-lib-datadog`/`workspace`
branches) merge to `main`.

## 3. Real pack: owner cardinality

`ai-shared-lib-datadog` (worktree `c2-owner-cardinality`),
`frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json`:
`owner`'s `cardinality` changed from `"singleton"` to `"at_most_one"`.
`source` field rewritten to explain the choice (why not `singleton`, why not
plain `optional`) and to point at `DIVERGENCES.md` (pre-existing reference,
unchanged) and at `workspace/.dat/privacy-ownership-scan-gap.md` (new).

Read the pack and its README first, before editing: confirmed the pack's
`privacy` line and the vendored copy were byte-identical to the real pack
(both blob `3307033`, matching the audit's finding) except for this task's
own `owner` line — verified with a live `diff` before and after.

## 4. Workspace vendored copy reconciled

`workspace` (worktree `c2-owner-cardinality`),
`.claude/navigator/packs/datadog-psa/frontmatter-datadog-psa.pack.json`:
copied byte-for-byte from the real pack after item 3's edit. Verified with
`diff` — zero output, confirmed identical.

## 5. README fixed

`workspace`'s `.claude/navigator/packs/datadog-psa/README.md`: the
`owner:` taxonomy section was already correct prose in isolation (its claim
that "no owner: tag means unowned, the normal state" was always the intent)
— it just contradicted the schema before this fix. Added one line naming the
enforcing cardinality explicitly (`at_most_one` — zero or one, never two or
more) so the prose and the schema now state the same rule in the same
vocabulary, plus the cross-link required by item 6.

## 6. Cross-link added

Same README, new paragraph pointing at
`workspace/.dat/privacy-ownership-scan-gap.md` (verified the target file
exists and covers the related migration-sweep topic). This is the cross-link
the plan called for and that never landed in the earlier, incomplete C2 pass.

## 7. Drift-prevention test

Added `meta_schema_namespaces_cardinality_enum_matches_the_cardinality_variants`
in `rust/frontmatter/src/profile.rs`, mirroring the crate's existing pattern
(`meta_schema_namespaces_type_enum_matches_the_facet_type_variants`) for the
`type` enum. This is the drift test that plainly did NOT exist for
cardinality before this task (the crate had a `type`-enum drift pin but no
`cardinality`-enum drift pin). It pins THREE independent holders of the
cardinality vocabulary against each other in one assertion:
`Cardinality`'s Rust variants (as a literal expectation, same limitation the
existing `type`-enum test documents), the meta-schema's
`namespaces[].cardinality.enum`, and the bundled core profile's own
`cardinality_types` map keys.

Scoped narrowly per the dispatch's own instruction: this is NOT the
`githooks`-tier-constants privacy-value-list drift test the plan's Track C
text separately calls for — that concerns a different vocabulary (`privacy`
values) with a different single source of truth (a Go module in a different
repo) and, per the audit, was already flagged as unmet by an earlier task,
not this one. A drift test binding `owner`'s cardinality to a
cross-repo Go source is out of scope for this task's file surface: there is
no external, language-independent authority for "what cardinality values
exist" the way `githooks`' tier constants are the authority for privacy
values — cardinality is intrinsic to this one crate (Rust enum + its own
meta-schema + its own core schema), so the three-way pin added here is the
complete, correct-scoped drift guard for THIS concern.

## 8. Live verification

`"$WORKSPACE_TOOLS_BIN" lint .dat/datadog-code-agent/design.md --json` from
the `workspace` worktree returns:

```
{"code":"precondition_unmet.schema.invalid_pack","message":"extension pack JSON is invalid: unknown variant `at_most_one`, expected one of `singleton`, `at_least_one`, `optional` at line 29 column 1", ...}
```

This is the KNOWN PROVISIONING GAP the dispatch anticipated: this machine's
provisioned `navigator` is `navigator 2.0.0`
(`/home/bits/.claude/plugins/data/governance-workspace-structure-jr-claude-plugins/bin/navigator-2.0.0`),
which predates C1 AND C2's schema changes entirely — it doesn't recognize
`at_most_one` as a valid cardinality string at all, let alone lint correctly
against it. This matches the pattern already documented elsewhere in the
audit (`governance-workspace-structure` 0.5.0 note: "this machine still
provisions navigator-2.0.0 ... so v2.1.0's behavior is committed but not yet
live here").

Correctness was instead verified directly:
1. The crate's own test suite (item 1) proves the schema mechanics are
   correct in isolation.
2. The throwaway integration test (item 1, evidence paragraph) proves the
   REAL, edited datadog-psa pack — not just the crate's synthetic fixture —
   accepts an `owner`-less file and rejects a two-`owner`-tag file, end to
   end, through this crate's actual `validate` function.
3. Read-through: `frontmatter-core.schema.json`'s new `at_most_one_cardinality`
   cascade step only fires `MULTIPLE_SINGLE_VALUE_TAGS` on `count > 1`, never
   on `count == 0` — the exact behavior the audit's "live proof of the failed
   acceptance criterion" (`MISSING_REQUIRED_TAG` on `owner`) needed fixed.

Once the fleet provisions a `navigator` build that vendors this crate's fix
(after a release per item 2, and after `governance-workspace-structure`'s own
repin, mirroring the `v2.1.0` repin pattern already in this session), a live
`navigator lint` run against an owner-less `workspace` file will confirm the
same result the throwaway test already proved.

## Files touched

`ai-shared-lib` (branch `chore/c2-owner-cardinality`):
- `schemas/frontmatter/frontmatter-core.schema.json`
- `schemas/frontmatter/frontmatter-profile.meta.schema.json`
- `rust/frontmatter/src/profile.rs`
- `rust/frontmatter/src/validate.rs`
- `rust/frontmatter/src/fix.rs`
- `rust/frontmatter/src/test_support.rs`
- `.task-reports/c2-owner-cardinality-report.md` (this file)

`ai-shared-lib-datadog` (branch `chore/c2-owner-cardinality`):
- `frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json`

`workspace` (branch `chore/c2-owner-cardinality`):
- `.claude/navigator/packs/datadog-psa/frontmatter-datadog-psa.pack.json`
- `.claude/navigator/packs/datadog-psa/README.md`

## Assumptions

- `owner`'s test-fixture entry in the crate's shared `SYNTHETIC_PACK_JSON`
  (`test_support.rs`) was deliberately left as `singleton` — it is a generic
  crate-internal fixture unrelated to the real pack, and several existing
  tests assert exact cascade-order sequences that include `owner`'s absence
  as a `MISSING_REQUIRED_TAG` violation. Changing it would have been scope
  creep with no acceptance-criterion benefit; a new, dedicated `lead`
  namespace was added instead to exercise `at_most_one` without disturbing
  any existing test's assertions.
- No release tag was cut (item 2) — left to the orchestrator per the
  dispatch's own fallback instruction.
