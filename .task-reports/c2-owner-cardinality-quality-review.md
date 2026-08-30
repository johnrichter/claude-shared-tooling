---
name: C2 owner cardinality — quality review
description: "Final quality gate on the C2 at_most_one cardinality fix across ai-shared-lib, ai-shared-lib-datadog, and workspace. Fixes the Python structural-guard regression the test-engineer found, plus a fabricated cross-link claim in the pack provenance and the vendored README. Verdict: ACCEPT WITH FIXES."
id: doc:ai-shared-lib:c2-owner-cardinality-quality-review
tags:
  - type:knowledge
  - topic:frontmatter
  - topic:git-governance
  - status:complete
  - privacy:public
  - owner:operator
links:
  - doc:ai-shared-lib:c2-owner-cardinality-report
  - doc:ai-shared-lib:c2-owner-cardinality-test-verification
updated: 2026-08-30T09:10:00Z
---

# C2 owner cardinality — quality review

## Verdict

**ACCEPT WITH FIXES** (harness token: `FIX-APPLIED`)

The core fix is correct, well-designed, and idiomatic. The `at_most_one`
cardinality is genuinely new capability, correctly wired through all five
`Cardinality::` match sites, and the naming is consistent across every holder
of the vocabulary. I applied three fixes across two repos and re-verified.
Nothing is merged, tagged, or pushed.

## Fixes applied

### Fix 1 — the regression (blocking, now closed)

`ai-shared-lib-datadog/tests/test_datadog_psa_pack.py:24`

Confirmed the exact cardinality string from the pack JSON itself before
editing — `"at_most_one"`, read out of
`frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json:12`, not
copied from either prior report.

```
-    ("owner", "singleton"),
+    ("owner", "at_most_one"),
```

One assertion value, nothing else in the file touched. Evidence it now
passes is in Re-verification below.

### Fix 2 — fabricated cross-link claim (major)

`ai-shared-lib-datadog/frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json:12`
and its byte-identical vendored copy at
`workspace/.claude/navigator/packs/datadog-psa/frontmatter-datadog-psa.pack.json:12`.

The `owner` namespace's rewritten `source` string ended:

> See DIVERGENCES.md and workspace/.dat/privacy-ownership-scan-gap.md for the
> migration-sweep this cardinality unblocks.

I read the cross-link target in full. It describes something else entirely:
`git-tools`' privacy scan has never been run against `workspace`, 229 tracked
files fail it because the scanner defaults to "public unless exempted", and the
two deferred root causes are (a) `workspace` has no `git-tools.yaml` scan
posture and (b) the `--privacy-tier` enum is `public`/`datadog`/`personal`
rather than `public`/`confidential`/`private`. The doc proposes **no migration
sweep**, and **nothing in it is blocked by `owner`'s cardinality**. The claim
"the migration-sweep this cardinality unblocks" asserts a causal dependency
that does not exist in the cited source.

The cross-link itself is legitimate and was a required plan item — only its
characterization was wrong. Corrected to state the actual relationship:

```
-See DIVERGENCES.md and workspace/.dat/privacy-ownership-scan-gap.md for the migration-sweep this cardinality unblocks.
+See DIVERGENCES.md, and workspace/.dat/privacy-ownership-scan-gap.md for the related open privacy/ownership scan-posture gap.
```

Both pack copies re-verified byte-identical after the edit (new shared MD5
`4f408c0b25e0a5982ea03132c0666c7a`).

### Fix 3 — same fabricated claim in the vendored README (major)

`workspace/.claude/navigator/packs/datadog-psa/README.md:44-46`

Same defect, worse: the README asserted the target doc tracks a sweep that
"re-tags files still carrying an old `owner:` shape". The target doc never
proposes re-tagging any `owner:` value — it mentions `owner:datadog` only as a
marker the scanner flags. That sentence was unsupported by its own citation.

```
-The migration sweep that closes `privacy`'s vocabulary and re-tags files still
-carrying an old `owner:` shape is tracked separately:
-`workspace/.dat/privacy-ownership-scan-gap.md`.
+A separate open gap concerns how the privacy/ownership scan reads these tags:
+the scanner's `--privacy-tier` values (`public`/`datadog`/`personal`) do not yet
+match `privacy:`'s own values, and this repo has no scan posture of its own.
+Tracked in `workspace/.dat/privacy-ownership-scan-gap.md`.
```

The genuinely correct part of the README change — the new line
`` `owner`'s cardinality is `at_most_one` — zero or one, never two or more.``
at line 35 — was left exactly as authored. It is accurate and it resolves the
prose-versus-schema contradiction the plan called out.

## Findings

### Blocking

None outstanding. The one blocking finding (Fix 1) is closed.

### Major

Both closed by Fixes 2 and 3. Root-cause note for the plan: the pack's `source`
fields are load-bearing provenance that no test asserts, so prose drift and
fabricated citations in them are invisible to CI in every repo. Two independent
reviewers passed this diff before me without reading the cross-link target.

### Minor

1. **`DIVERGENCES.md` is a dangling reference.**
   `frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json:12` (and the
   vendored copy) cite `DIVERGENCES.md`. That file does not exist anywhere:
   verified by `find` across the entire `psa-platform` tree (all repos and all
   worktrees, excluding `.git`) and by `git log --all --diff-filter=A` over
   `*DIVERGENCES*` in `ai-shared-lib-datadog` — no hit in either. This is
   **pre-existing on `main`**, not introduced by this task, so I left it in
   place rather than silently dropping a provenance breadcrumb that may be
   intended-but-unwritten. Flagged for the orchestrator: either write the doc
   or drop the reference repo-wide. Note the implementer's report line 90
   describes it as a "pre-existing reference, unchanged", which reads as though
   it was checked; it resolves to nothing.

2. **Pre-existing `clippy` and `fmt` drift in the crate, all outside this
   diff.** Verified each against `main` rather than trusting the prior report:
   - `clippy`: one error, `profile.rs:1688` `assert!(profile.required_fields().count() == 6)`
     (`manual_assert_eq`). Present verbatim on `main` at `profile.rs:1684` — the
     46 lines this branch adds account for the shift. Pre-existing.
   - `fmt --check`: 5 files drift — `bm25/src/lib.rs:280`,
     `frontmatter/src/profile.rs:1350,2353,2375`, `frontmatter/src/validate.rs:1894`.
     None falls inside any hunk this branch touches (this branch's hunks in
     those files are `profile.rs` ~2183-2225 and `validate.rs` ~467-505,
     ~988-1027). Pre-existing.
   - With those two excluded, this task's new code is `clippy`-clean and
     `fmt`-clean. Confirmed by forcing a full recheck (`touch` on the three
     edited sources) and running `clippy` without `-D warnings`, so the
     pre-existing error could not mask later warnings: total inventory was
     exactly one warning, the pre-existing one.
   Out of scope to fix here; worth a separate hygiene task.

3. **`query_sdet_adversarial.rs:37` keeps its own second copy of a synthetic
   pack** with `owner` as `singleton`, independent of
   `test_support.rs:49`. Both are deliberate and correct (see Design notes),
   but two hand-maintained copies of near-identical fixture JSON with no pin
   between them is latent drift. Not a defect today; noted only.

## Design and idiom review

Everything below I checked directly against the surrounding code, not against
the prior reports.

**Naming consistency across all four holders of the vocabulary — consistent.**

| Holder | Value | Location |
| --- | --- | --- |
| Rust enum variant | `Cardinality::AtMostOne` | `profile.rs:649` |
| serde wire form | `at_most_one` via `#[serde(rename_all = "snake_case")]` | `profile.rs:641` |
| Meta-schema enum | `"at_most_one"` | `frontmatter-profile.meta.schema.json:179` |
| Core schema `cardinality_types` key | `at_most_one` | `frontmatter-core.schema.json:35` |
| Core schema cascade step | `at_most_one_cardinality` | `frontmatter-core.schema.json:98` |
| Pack JSON value | `"at_most_one"` | pack line 12, both copies |
| Rust cascade fn | `at_most_one_cardinality_phase` | `validate.rs:470` |

The `_cardinality` / `_cardinality_phase` suffix pairing matches the two
existing siblings exactly. No spelling variant anywhere.

**Cascade order agrees between schema and code.** Schema
`violation_cascade` steps are `singleton_cardinality → at_least_one_cardinality
→ at_most_one_cardinality → parent_dependency → rule_set_*`; `tag_rule_violations`
(`validate.rs:369-375`) calls the phases in that same order. Since the schema
declares this array order-significant and byte-stable output is an invariant,
placing the new phase third — after both existing cardinality phases, before
`parent_dependency` — preserves emission order for every pre-existing file,
which is what the pack's own ordering note requires.

**`at_most_one_cardinality_phase` is a faithful structural sibling** of
`singleton_cardinality_phase` (`validate.rs:401`), down to the
`cascade_step` early-return, the `filter` on cardinality, the `step.emit(0)`
guard, the `python_repr_list` tags rendering, and the nested `if let`. Reads
like the code around it. The duplicated `group_get` call mirrors the existing
sibling rather than diverging from it — correct call on consistency over
micro-optimization.

**The two deliberate non-changes are both right, and I verified the reasoning
rather than accepting it:**
- `add_required_placeholders` (`fix.rs:354`) correctly still matches only
  `Singleton | AtLeastOne`. `AtMostOne` must never get a stubbed placeholder,
  because absence is a legal terminal state for it. Including it would have
  reintroduced exactly the bug C2 exists to fix, via the fix path instead of
  the validate path.
- `dedupe_single_valued` (`fix.rs:283`, renamed from `dedupe_singletons`)
  correctly matches `Singleton | AtMostOne`. Both cap at one value, so both
  want the same Tier-1 repair. The rename is the right call — the old name
  would have become a lie.
- No exhaustive `match` over `Cardinality` exists in the crate (all sites use
  `matches!`/`==`/`filter`), so there was no compiler backstop for
  completeness. I re-grepped all `Cardinality::` sites by hand and confirm the
  five known sites are the only ones.

**The new drift-pin test is the right scope.** It binds three independent
holders of the vocabulary in one assertion and honestly documents its own
limitation (literal expectations, not reflection) in the same terms as the
sibling `type`-enum pin it mirrors. I confirmed the meta-schema's
`coreProfile.cardinality_types` uses open `additionalProperties` with no key
enum, so there is no fourth holder the pin is missing.

**Comment density is appropriate** — the new doc comments earn their place by
distinguishing three otherwise-confusable cardinalities, which is exactly the
ambiguity that produced this bug. No archaeology, no dead references, no
excess.

## Drift search — re-run independently

I re-ran the same class of search the test-engineer did, across all three
worktrees, rather than trusting the reported result.

Query: `git grep -n -I -i -E 'singleton|SINGLE_VALUE'` in each worktree,
filtered to lines mentioning `owner`; plus a cardinality-specific sweep over
`*.py`/`*.json`/`*.md` in `ai-shared-lib-datadog`.

**Confirmed: `tests/test_datadog_psa_pack.py:24` was the only live miss.** All
other hits classified:

| Location | Classification |
| --- | --- |
| `ai-shared-lib-datadog/tests/test_datadog_psa_pack.py:24` | **The real miss — fixed** |
| `ai-shared-lib/rust/frontmatter/src/test_support.rs:49` | Crate's synthetic fixture; `owner` intentionally left `singleton`. Correct — it is a generic fixture, unrelated to the real pack, and several existing tests assert cascade sequences that include `owner`'s absence as `MISSING_REQUIRED_TAG`. The new `lead` namespace exercises `at_most_one` without disturbing them. |
| `ai-shared-lib/rust/frontmatter/tests/query_sdet_adversarial.rs:37` | Same, second independent fixture copy. See Minor 3. |
| `ai-shared-lib/rust/frontmatter/src/validate.rs:1421,1458` | Comments describing that same synthetic fixture. Not the real pack. |
| `workspace/.dat/navigator-redesign/*`, `.dat/datadog-code-agent/*` | Archival planning/design/salvage documents describing the pre-fix design, including a `.patch` file. Narrative, never executed. Not drift. |
| `ai-shared-lib-datadog/check_privacy.py` | Regex-matches `owner:` values, makes no count or cardinality assumption. Unaffected — confirmed by running it (all 24 of its tests pass). |

## Re-verification

All runs below are on the final tree, after all three of my fixes.

**`ai-shared-lib-datadog` — the fixed Python test, and the whole suite:**

```
$ python3 -m unittest tests.test_datadog_psa_pack -v
test_exempt_path_globs ... ok
test_feature_and_product_parents ... ok
test_feature_declared_before_product ... ok
test_has_all_required_pack_keys ... ok
test_namespace_order_and_cardinality ... ok        <-- was FAILING, now passes
test_no_report_block ... ok
test_no_rule_sets ... ok
test_parses_as_json ... ok
test_version_and_extends ... ok
Ran 9 tests in 0.002s
OK

$ python3 -m unittest discover -s tests
Ran 33 tests in 3.711s
OK
```

33/33 across the repo, including `check_privacy.py`'s own 24 tests — so Fix 2's
edit to the pack JSON broke nothing else.

**`ai-shared-lib` — the crate suite:**

```
$ cd rust && cargo test -p frontmatter
test result: ok. 263 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
test result: ok. 18 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out   (tests/query_sdet_adversarial.rs)
test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out    (doc-tests)
```

263 + 18, zero failures. Matches both prior reports' counts.

**Both pack copies byte-identical after my edit:**

```
$ diff -q <ai-shared-lib-datadog copy> <workspace vendored copy>
(no output)
$ md5sum
4f408c0b25e0a5982ea03132c0666c7a   (both)
```

Both also re-confirmed as valid JSON via `json.load`.

**End-to-end proof against the real edited pack, re-run by me.** The permanent
suite never loads the real pack, so I wrote my own throwaway integration test
(`rust/frontmatter/tests/qr_throwaway_real_pack_owner.rs`), ran it, and deleted
it — `git status --short` in `ai-shared-lib` is clean, confirming it is gone. It
read the real pack live off disk and layered it as
`core@2.0.0 + default@1.0.0 + datadog-psa` (the `datadog-psa` pack is not
self-sufficient alone):

```
running 3 tests
test owner_less_file_has_zero_owner_violations ... ok
test exactly_one_owner_has_zero_owner_violations ... ok
test two_owners_emit_exactly_one_multiple_single_value_tags ... ok
test result: ok. 3 passed; 0 failed
```

I added the middle case (exactly one owner), which neither prior report
verified against the real pack.

**Negative control — the part neither prior report did.** A green test proves
nothing unless it can fail. I re-pointed the same three tests at `main`'s
pre-fix pack (`git show main:...`, `owner` = `singleton`) and re-ran:

```
test owner_less_file_has_zero_owner_violations ... FAILED
  unexpected owner violations: [Violation {
    code: "MISSING_REQUIRED_TAG", field: "owner",
    message: "missing required 'owner:' tag" }]
test exactly_one_owner_has_zero_owner_violations ... ok
test two_owners_emit_exactly_one_multiple_single_value_tags ... ok
test result: FAILED. 2 passed; 1 failed
```

This is the decisive evidence. The owner-less case fails on `main` with exactly
the `MISSING_REQUIRED_TAG(owner)` violation the audit named as C2's unmet
acceptance criterion, and passes on this branch. The two-owner case correctly
passes under both cardinalities, since `singleton` also caps at one — so that
assertion alone could never have distinguished the fix from the bug. The fix is
real, and my verification is not vacuous.

## Test-suite assessment

**Adequate, now that the regression is closed.** Coverage is genuinely
adversarial rather than implementation-mirroring: the three required scenarios
(absent / exactly one / two-or-more) are each independently tested at the
validate layer, the fix layer gets its own two tests (dedupe behavior and the
no-placeholder guarantee), and the drift pin guards the vocabulary across three
holders.

One real gap remains, and it is the gap that let this regression through:

- **No permanent test loads the real `datadog-psa` pack.** Every proof that the
  real pack behaves correctly — the implementer's, the test-engineer's, and
  mine — came from a throwaway test that was then deleted. Three throwaway
  tests written and discarded for the same purpose is a standing signal that
  the coverage belongs in the tree. The `ai-shared-lib-datadog` Python test is
  purely structural (it asserts the pack's JSON shape, which is why it caught
  the cardinality string change at all) and cannot exercise validation
  behavior, because the repo takes no Rust dependency by design.
- **That Python test is in no CI job.** `.github/workflows/ci.yml` runs only
  `check_privacy.py --tier datadog --strict` and a package build/install job.
  A live-failing test sat in the tree and no automation would ever have said
  so. This is why the miss survived two reviews.

Recommendation for the test-engineer, as a follow-up task rather than a demand
on this one: wire `test_datadog_psa_pack.py` into `ai-shared-lib-datadog`'s CI,
and add a permanent crate-side integration test that validates a small corpus
against the real pack. Both are out of this task's scope.

## Residual risk

1. **Not live on this machine.** The provisioned binary is `navigator 2.0.0`,
   which predates the schema change and rejects the edited pack outright
   (`unknown variant `at_most_one``). Correctness is proven at the crate and
   pack level, not through the deployed tool. This resolves only after a
   `rust/frontmatter` release, a `governance-workspace-structure` repin, and a
   fleet reprovision. The implementer documented this accurately; I confirmed
   the mechanism is a version gap, not a defect in the fix.
2. **Any consumer pinned to an older crate will reject the edited pack**, not
   degrade gracefully — `at_most_one` is an unknown serde variant to them, and
   the pack fails to load entirely. Sequencing matters: the crate release must
   land and propagate before the pack change reaches a consumer. The pack and
   the crate live in different repos with no shared gate enforcing that order.
3. **`DIVERGENCES.md` remains dangling** (Minor 1), by deliberate choice, to
   avoid destroying a possibly-intended breadcrumb.
4. **Pre-existing `fmt`/`clippy` drift is still red** (Minor 2), so `cargo fmt
   --check` and `clippy -D warnings` cannot be used as a clean gate on this
   branch without first landing a hygiene pass.

## Plan feedback

1. **Release sequencing is a cross-repo ordering constraint the plan does not
   name.** Residual risk 2 is a real coupling: the pack change is only safe
   once the crate release has propagated. The orchestrator should land and
   release `rust/frontmatter` before the `ai-shared-lib-datadog` and
   `workspace` branches reach anything that lints in anger. Leaving tagging to
   the orchestrator was the right call by the implementer, but the *order*
   needs to be explicit in the plan.
2. **Pack `source` provenance has no gate.** Fixes 2 and 3 were fabricated
   causal claims in provenance prose that no test asserts and that two
   reviewers passed over. If these `source` fields are meant to be trustworthy
   — and the pack's own description says every value is traceable through them
   — they need either a link-resolution check or an explicit review step.
   `DIVERGENCES.md` (Minor 1) is the same class of defect, already latent on
   `main`.
3. **The vendored-copy byte-identity invariant is unguarded.** The workspace
   README states plainly that byte-identity "is **not** asserted by any
   standing gate". This task got it right three times by hand. A trivial CI
   check comparing the two files would retire a recurring manual step.
4. **The `githooks`-tier-constants privacy-value drift test remains unmet.**
   The implementer correctly scoped it out — it concerns a different
   vocabulary with a different authority (a Go module in another repo). It is
   still an open plan item and should not be considered closed by this task's
   cardinality pin.

## Files touched by this review

`ai-shared-lib-datadog` (branch `chore/c2-owner-cardinality`):
- `tests/test_datadog_psa_pack.py` — Fix 1
- `frontmatter-packs/datadog-psa/frontmatter-datadog-psa.pack.json` — Fix 2

`workspace` (branch `chore/c2-owner-cardinality`):
- `.claude/navigator/packs/datadog-psa/frontmatter-datadog-psa.pack.json` — Fix 2, re-vendored byte-for-byte
- `.claude/navigator/packs/datadog-psa/README.md` — Fix 3

`ai-shared-lib` (branch `chore/c2-owner-cardinality`):
- `.task-reports/c2-owner-cardinality-quality-review.md` — this file

No source change was needed in `ai-shared-lib`; the crate-side fix was already
correct. No merge, no push, no tag, in any repo.
