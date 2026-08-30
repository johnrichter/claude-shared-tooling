---
name: C2 owner cardinality — independent test verification
description: "Independent SDET re-verification of the C2 at_most_one cardinality fix across ai-shared-lib, ai-shared-lib-datadog, and workspace. Finds one real regression the implementer's own report did not surface: ai-shared-lib-datadog's Python structural test still hardcodes owner as singleton and now fails against the edited real pack."
id: doc:ai-shared-lib:c2-owner-cardinality-test-verification
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

# C2 owner cardinality — independent test verification

VERDICT: **FAIL** (one real, reproducible regression found; everything else PASSES)

The Rust crate-side fix is correct, complete, and its own test suite is green. But
`ai-shared-lib-datadog`'s Python structural guard test for the real pack was not
run by the implementer and now fails against the edited pack — a genuine,
un-caught test failure, not a flake.

## Check 1 — diff completeness across all three repos

Confirmed via `git diff main...HEAD` in each worktree plus manual `grep` for every
`Cardinality::` match site (not trusting the implementer's own completeness claim):

- `ai-shared-lib` (`rust/frontmatter/src/profile.rs:644-650`): `Cardinality::AtMostOne`
  variant added, doc comment distinguishes it from `Singleton`/`Optional`.
- Meta-schema (`schemas/frontmatter/frontmatter-profile.meta.schema.json:179`):
  `"at_most_one"` added to `namespaces[].cardinality` enum.
- Core schema (`schemas/frontmatter/frontmatter-core.schema.json`): new
  `at_most_one` entry in `cardinality_types` (min 0, max 1) and new
  `at_most_one_cardinality` cascade step (fires `MULTIPLE_SINGLE_VALUE_TAGS`
  only on `count > 1`).
- All 5 real match sites on `Cardinality::` in the crate found by grep:
  - `fix.rs:283` — `dedupe_single_valued` (renamed from `dedupe_singletons`):
    `Cardinality::Singleton | Cardinality::AtMostOne` — correct, both cap at 1.
  - `fix.rs:354` — `add_required_placeholders`: `Cardinality::Singleton |
    Cardinality::AtLeastOne` — correctly EXCLUDES `AtMostOne` (absence is not
    a violation for this cardinality, so no placeholder should be stubbed in).
  - `validate.rs:413` — `singleton_cardinality_phase` (unchanged, filters
    `Singleton` only — correct, not touched).
  - `validate.rs:456` — `at_least_one_cardinality_phase` (unchanged).
  - `validate.rs:485` — new `at_most_one_cardinality_phase`, wired into
    `tag_rule_violations` right after `at_least_one_cardinality_phase`.
  - No exhaustive `match cardinality { ... }` block exists anywhere in the
    crate (all sites use `matches!`/`==`/`filter`), so there was no
    compiler-enforced completeness backstop to rely on — the 5 sites above
    were found and checked by hand.
- New drift-pin test `meta_schema_namespaces_cardinality_enum_matches_the_cardinality_variants`
  (`profile.rs`) pins the Rust enum's snake_case spellings, the meta-schema
  enum, and the embedded core profile's `cardinality_types` keys against
  each other — genuinely new; the crate previously had this drift pin only
  for the `type` enum, not `cardinality`.

**Result: PASS.** No missed match site. `AtMostOne` correctly excluded from
required-placeholder logic (it is optional-by-absence, not required).

## Check 2 — crate test suite, run myself

Convention confirmed from `rust/README.md`: `cd rust && cargo test`.

```
$ cd rust && cargo fmt --check && cargo clippy -p frontmatter --all-targets --all-features -- -D warnings && cargo test -p frontmatter
...
test result: ok. 263 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 1.49s
     Running tests/query_sdet_adversarial.rs ...
test result: ok. 18 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
   Doc-tests frontmatter
test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
```

263 passed matches the report's claimed count. Isolated the 5 new
cardinality tests and re-ran them alone:

```
$ cargo test -p frontmatter at_most_one
running 5 tests
test validate::tests::at_most_one_namespace_with_exactly_one_occurrence_is_not_a_violation ... ok
test fix::tests::at_most_one_namespace_dedupes_to_its_first_occurrence ... ok
test validate::tests::at_most_one_namespace_with_two_or_more_occurrences_is_a_violation ... ok
test validate::tests::at_most_one_namespace_absent_is_not_a_violation ... ok
test fix::tests::at_most_one_namespace_absent_gets_no_placeholder ... ok
test result: ok. 5 passed; 0 failed
```

Confirmed the three required scenarios are each independently tested:
- **zero occurrences** → `at_most_one_namespace_absent_is_not_a_violation`: asserts
  `entry.is_valid` and no violation on the `lead` field.
- **exactly one** → `at_most_one_namespace_with_exactly_one_occurrence_is_not_a_violation`:
  asserts `entry.is_valid`.
- **two or more** → `at_most_one_namespace_with_two_or_more_occurrences_is_a_violation`:
  asserts `!entry.is_valid`, finds a violation with `field == "lead"`,
  `code == "MULTIPLE_SINGLE_VALUE_TAGS"`, and the message contains both
  "at most once" and the literal count "2".

`cargo fmt --check` and `cargo clippy -p frontmatter --all-targets
--all-features -- -D warnings` both show pre-existing, unrelated drift only
(confirmed by diffing against `main`):
- fmt: `profile.rs:1350,2353,2375`, `validate.rs:1894` — all pre-existing
  comment/formatting drift, none inside this task's diff hunks.
- clippy: `profile.rs:1688` `assert!(x == 6)` → `manual_assert_eq` — line
  exists verbatim on `main` (confirmed via `git show main:...`), predates
  this branch.

**Result: PASS.** 263/263, all three required scenarios independently
covered with correct assertions (not mirroring implementation, but the
acceptance behavior: absence-ok, one-ok, two-plus-fails-with-correct-code-
and-message).

## Check 3 — byte-identity of real pack vs. vendored copy

```
$ diff -u ai-shared-lib-datadog/.../frontmatter-datadog-psa.pack.json \
          workspace/.../frontmatter-datadog-psa.pack.json
(no output)
$ md5sum both files
ed5a7c071c337c1d3734a84133d50592  (ai-shared-lib-datadog copy)
ed5a7c071c337c1d3734a84133d50592  (workspace vendored copy)
```

**Result: PASS.** Identical MD5, zero diff, post-change.

## Check 4 — README fix and cross-link

`workspace/.claude/navigator/packs/datadog-psa/README.md` diff:
- Added: `` `owner`'s cardinality is `at_most_one` — zero or one, never two
  or more. `` directly above the existing "no owner: tag means unowned"
  prose — now states the same rule the schema enforces, in the schema's own
  vocabulary. No remaining contradiction (the schema no longer requires
  `owner` at all, and the prose never claimed it did).
- Added cross-link paragraph pointing at
  `workspace/.dat/privacy-ownership-scan-gap.md`.

Verified target file exists:
```
$ ls -la workspace/.../c2-owner-cardinality/.dat/privacy-ownership-scan-gap.md
-rw-r--r-- 1 bits dog 5374 ... privacy-ownership-scan-gap.md
```

**Result: PASS.** README no longer contradicts the schema; cross-link
target is real and present.

## Check 5 — real, run validation proving no-owner passes and two-owner fails

Wrote a throwaway integration test (`rust/frontmatter/tests/throwaway_real_pack_owner_verify.rs`),
ran it, then deleted it (not part of the permanent suite; confirmed absent
via `git status --short` post-cleanup). It read the REAL, edited
`ai-shared-lib-datadog` pack file live off disk (not the crate's synthetic
fixture), built a `Profile` via `Profile::from_packs(embedded_core_json(),
&[embedded_pack_json("default@1.0.0"), &real_pack_json])` — `datadog-psa`
alone is not self-sufficient (`from_pack_json` alone fails with
`IntegrityViolation: file_class.default 'context' has no description_caps
entry` because `context`'s cap lives in the `default` base pack layer, not
in `datadog-psa` itself; this matches the report's own description of a
"minimal base pack" layer) — then validated two synthetic files through
this crate's real `validate()` function end to end:

```
running 2 tests
test owner_less_file_validates_with_zero_owner_violations ... ok
test two_owner_tags_fail_with_multiple_single_value_tags ... ok
test result: ok. 2 passed; 0 failed
```

- Owner-less file (`type:knowledge`, `status:complete`, `privacy:public`,
  `topic:t`, no `owner:` tag): zero violations with `field == "owner"`.
- Two-`owner:` file (adds `owner:alex`, `owner:sam`): exactly one violation
  with `field == "owner"`, `code == "MULTIPLE_SINGLE_VALUE_TAGS"`.

Test file deleted after the run; confirmed not present in `git status`.

**Result: PASS.** Live, run proof against the real pack (not source-level
reasoning), matching the report's own throwaway-test claim.

## Check 6 — drift search for any remaining hard-coded owner-singleton assumption

Searched all three repos for `owner` co-occurring with `singleton`/
`SINGLE_VALUE_NAMESPACES`.

**FOUND — real, reproducible regression:**
`ai-shared-lib-datadog/tests/test_datadog_psa_pack.py:24` still hardcodes
`("owner", "singleton")` in its `_EXPECTED_NAMESPACES` structural-guard list.
This test was not run by the implementer (their report never mentions this
Python test file, only the crate's Rust suite and a throwaway Rust
integration test) and now fails against the edited real pack:

```
$ cd ai-shared-lib-datadog/.../c2-owner-cardinality && python3 -m unittest tests.test_datadog_psa_pack -v
test_namespace_order_and_cardinality ... FAIL

AssertionError: Lists differ: [...('owner', 'at_most_one')...] != [...('owner', 'singleton')...]
First differing element 1:
('owner', 'at_most_one')
('owner', 'singleton')
...
Ran 9 tests in 0.003s
FAILED (failures=1)
```

Repro: `cd /home/bits/Development/workspaces/psa-platform/ai-shared-lib-datadog/.claude/worktrees/c2-owner-cardinality && python3 -m unittest tests.test_datadog_psa_pack -v`

Location to fix: `tests/test_datadog_psa_pack.py:24`, change
`("owner", "singleton")` to `("owner", "at_most_one")` in
`_EXPECTED_NAMESPACES`.

Note: this test is stdlib-only (no venv needed) and is NOT wired into this
repo's CI (`.github/workflows/ci.yml` only runs `check_privacy.py --tier
datadog --strict` and a package build/install job — `test_datadog_psa_pack.py`
is not invoked by any CI job in this repo as configured). So this would not
have been caught by CI either; it is a repo-local structural-guard test that
must be run manually, and the implementer's own claimed verification (crate
tests + throwaway Rust integration test) did not include it.

Other `owner`+`singleton` co-occurrences found and confirmed NOT drift:
- `ai-shared-lib/rust/frontmatter/tests/query_sdet_adversarial.rs:37` and
  `rust/frontmatter/src/test_support.rs:49`: the crate's own generic
  synthetic test fixture, deliberately left as `singleton` per the
  implementer's own stated rationale (unrelated to the real pack; changing
  it would disturb unrelated pre-existing cascade-order test assertions).
  Confirmed this is intentional and scoped correctly — not a missed site.
- `ai-shared-lib/rust/frontmatter/src/validate.rs:1421,1458`: doc comment
  and a test's inline comment describing the crate's own synthetic fixture
  behavior (same fixture as above) — not the real pack, not drift.
- `workspace/.dat/navigator-redesign/*` and `.dat/datadog-code-agent/*`:
  historical planning/design/spike documents describing the PRE-fix state
  of the schema (the original singleton-owner design that C2 corrects).
  These are archival narrative, not live schema/code/enforcement — flagged
  here for completeness but not counted as a regression; nothing in them is
  executed or asserted against at runtime. `check_privacy.py` in
  `ai-shared-lib-datadog` was also checked: it regex-matches `owner:` values
  but makes no cardinality/count assumption, so it is unaffected by this
  change.

**Result: FAIL.** One real, reproducible, un-caught test failure
(`ai-shared-lib-datadog/tests/test_datadog_psa_pack.py`); all other
candidate sites checked and confirmed non-issues.

## Summary by check

| # | Check | Result |
|---|---|---|
| 1 | Diff completeness (all match sites wired) | PASS |
| 2 | Crate test suite, run myself (263/263 + 3 required scenarios) | PASS |
| 3 | Real pack vs. vendored copy byte-identical | PASS |
| 4 | README fix + cross-link real and correct | PASS |
| 5 | Real, run validation (no-owner passes, two-owner fails) | PASS |
| 6 | No remaining hard-coded owner-singleton assumption | **FAIL** |

## Overall verdict: FAIL

The crate-level fix (schema, Rust enum, cascade logic, dedupe/placeholder
wiring, drift-pin test) is correct and thoroughly proven, both by the
crate's own suite and by an independent throwaway integration test against
the real pack. But the task's own acceptance bar implicitly includes not
leaving a stale hard-coded assumption anywhere in the affected repos, and
one was found and is a live, reproducible test failure right now in
`ai-shared-lib-datadog`. This is a one-line fix
(`tests/test_datadog_psa_pack.py:24`) but it is a real gap the implementer's
report did not disclose and did not run.

## Files reviewed / touched during verification

- `ai-shared-lib` worktree: `rust/frontmatter/src/{profile,validate,fix,test_support}.rs`,
  `schemas/frontmatter/frontmatter-core.schema.json`,
  `schemas/frontmatter/frontmatter-profile.meta.schema.json`,
  `.task-reports/c2-owner-cardinality-report.md` (read only).
  Throwaway test file `rust/frontmatter/tests/throwaway_real_pack_owner_verify.rs`
  was created, run, and deleted — confirmed absent from `git status` before
  writing this report.
- `ai-shared-lib-datadog` worktree:
  `frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json` (read),
  `tests/test_datadog_psa_pack.py` (read, run — found the failure).
- `workspace` worktree:
  `.claude/navigator/packs/datadog-psa/frontmatter-datadog-psa.pack.json` (read),
  `.claude/navigator/packs/datadog-psa/README.md` (read),
  `.dat/privacy-ownership-scan-gap.md` (existence confirmed).

No files outside the three designated worktrees were modified. No merge, no
push, no tag.
