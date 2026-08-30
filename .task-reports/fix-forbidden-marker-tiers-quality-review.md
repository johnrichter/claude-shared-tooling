# fix-forbidden-marker-tiers - quality review

## Verdict

**FIX-APPLIED.** The two regex changes in `go/githooks/privacy.go` are correct, complete, and precisely scoped; both prior reports are right about the production behavior. I applied two fixes of my own: a committed regression test closing three spec clauses that had **no** test coverage (proven by mutation testing), and a correction to a factually-backwards claim the implementer's report carries about `git-tools`.

## Scope reviewed

Isolated change is `17185ad..9cc8419` (commit `9cc8419`): `go/githooks/privacy.go` (2 lines) + `go/githooks/sanity_test.go` (1 rewrite, 2 new tests). `git diff main...HEAD` is merge-base-relative and clean of `main`'s unrelated advance.

## Verification (points 1-7 of the review brief)

### 1-4. Tier semantics - confirmed by direct probe of the compiled configs

Not by reading the regexes: I compiled a throwaway in-package test that enumerated every tier against 14 values (removed after running, tree left clean).

| `privacy:` value | public | confidential | private |
|---|---|---|---|
| `public` | allowed | allowed | allowed |
| `internal` | **forbidden** | allowed | allowed |
| `confidential` | **forbidden** | allowed | allowed |
| `private` | **forbidden** | **forbidden** | allowed |
| `PRIVATE` / `Private` / `CoNfIdEnTiAl` | **forbidden** | **forbidden** (`private` only) | allowed |
| `internally`, `privateish`, `confidentially`, `publik`, `restricted`, `internal_ops` | allowed | allowed | allowed |

- `TierPublic` forbids exactly `internal`, `confidential`, `private` — met.
- `TierConfidential` forbids exactly `private`; `confidential` is **not** forbidden — met. Marker count is 1, so no stray alternative survives.
- `TierPrivate` marker count is **0** — forbids nothing, entry untouched by the diff.
- Word boundaries hold: no false match on `internally`, `privateish`, `confidentially`, `internal_ops`.
- Frontmatter scoping holds: `privacy: private` in the **body** of a file with no frontmatter is not a violation at the confidential tier.

### 5. Full suite, run fresh

From `go/githooks` (own Go module), after my edits:

```
gofmt -l .                  -> no output
go build ./...              -> clean
go vet ./...                -> clean
go test ./... -count=1      -> ok  .../go/githooks  0.055s
```

### 6. Live end-to-end - verified against the POST-MERGE tree, not the branch

Both prior agents built against the branch alone and had to hand-patch `git-tools`' `internal/cli/scan.go` to make it compile. That masks the state that actually ships. I instead computed the real merge result with `git merge-tree --write-tree main HEAD` (exit 0, tree `abac88b` — **merge is clean**; the regex lines and `main`'s `EmployeeEmailCheck` region do not overlap), extracted that tree to scratch, and built from it:

- merged `go/githooks`: `gofmt`/`build`/`vet`/`test` all clean; carries **both** the two fixed regexes and `main`'s `AllowedDomains` rename.
- `git-tools` (copied from its primary checkout at `main`, `.git` excluded, `go.mod replace` -> merged module): builds **unpatched**. No source edit was needed, which is the direct disproof of the report's `git-tools`-is-stale claim.

Scan of a target holding the real production file plus three fixtures:

| tier | result |
|---|---|
| `confidential` | **exactly 1** violation: `private-case.md` `forbidden_marker "privacy:private"`. Real `design.md` (`privacy:confidential`) does **not** flag. `internal-case.md` does not flag. exit 30 |
| `public` | 7 violations: `design.md` + `internal-case.md` + `private-case.md` each `forbidden_marker` + `not_public_pair`; `nearmiss-case.md` (`privacy:privateish`) gets **only** `not_public_pair`, no false marker match. exit 30 |
| `private` | 0 violations, exit 0, status `success` |

Production file used verbatim: `/home/bits/Development/workspaces/psa-platform/workspace/.dat/feature-request-reporting/design.md`, tag `- privacy:confidential` (no space after the colon — the `\s*` in the pattern covers it), in a repo declaring `privacy_tier: confidential`. This is the exact case that surfaced the bug, and it is fixed.

All scratch created for this review was removed.

### 7. Other tier-marker tables - refuted, with a note

- **No other module in this repo** duplicates the semantics. `privacyTierConfigs` is the single table; every other `PrivacyTier` reference in the repo is a test or a pass-through.
- **`git-tools` (downstream consumer) hardcodes nothing.** It passes the tier through as a string (`internal/cli/scan.go`, `hooks.go`, `config.go`) and validates only membership via `tier.Known()`. No marker table of its own. Nothing to fix, and no repin needed for this change.
- **Sibling repos ship a legacy Python ancestor** — `scripts/check_privacy.py` in `marketplace-datadog`, `knowledge-public-datadog`, `knowledge-private-datadog`, `ai-shared-lib-datadog` — with a structurally identical `TIERS` table carrying the same regex shapes. It does **not** need the same fix: its tier vocabulary is different (`public`/`datadog`/`personal`, with `privacy:` in {`internal`,`confidential`} and sensitivity above that expressed via `owner:` in {`datadog`,`personal`}). Under that vocabulary its tables are self-consistent — the `datadog` tier forbidding `confidential` is *correct* there, because `confidential` is the tier above `internal`.
- This is almost certainly the **origin** of the bug: the Go port renamed the middle tier `internal` -> `confidential` and the top `personal` -> `private`, dropped the `owner:` axis entirely (deliberately — see `TestScanPrivacyNoOwnerConceptReachable`), but carried the Python regexes over verbatim. `TierConfidential` thus inherited "forbid `confidential`" (which had meant "forbid the tier above"), and `TierPublic` inherited a list missing `private` (which the old taxonomy covered via `owner: personal`).

## Findings

### Blocking

None.

### Major

**M1 — `go/githooks/adversarial_test.go` (pre-fix state): three named acceptance clauses had zero regression coverage.** The delivered suite tests only what the diff changed, not the rule the diff implements. Proven by mutation testing (disable my new test, mutate `privacy.go`, run the suite as delivered):

| mutation | suite as delivered |
|---|---|
| public tier loses `private` (the original bug) | CAUGHT |
| confidential tier re-adds `confidential` (other half of the bug) | CAUGHT |
| **public tier loses legacy `internal`** | **MISSED** |
| **public tier loses its trailing `\b`** | **MISSED** |
| **confidential tier loses its trailing `\b`** | **MISSED** |

The three misses map directly onto acceptance criteria: "`TierPublic` must forbid `internal` (legacy value)" and "word-boundary-safe, no false match on e.g. `internally` or `privateish`". Word-boundary safety was verified in this task **only** by throwaway probes in both reports — nothing committed defends it. At the public tier the loss is especially quiet, because `not_public_pair` keeps failing the file for an unrelated reason and hides the lost marker alternative. Fixed (see below).

**M2 — `.task-reports/fix-forbidden-marker-tiers-report.md:55-58` and `:105-110`: a factually-backwards claim, committed.** The report states `git-tools` references `EmployeeEmailCheck.AllowedDomains`, "which no longer exists on `go/githooks` `main` (renamed to `Domains`)", and files it as a separate ticket. The rename ran the other way: `main`'s `6497aad` replaced `Domains`/`Allowlist` **with** `AllowedDomains`. `main:go/githooks/privacy.go:187` is `AllowedDomains []string`; `git-tools`' primary checkout at `main`, `internal/cli/scan.go:275`, is `githooks.EmployeeEmailCheck{AllowedDomains: cfg.AllowedEmailDomains}` — correct against `main`. This branch is the stale side. The test-verification doc caught this and said so, but the report is the more discoverable artifact and would have sent someone to file a bogus ticket against `git-tools` and a needless repin. Fixed (see below). Credit to the test-engineer for the correct diagnosis.

### Minor

**m1 — over-match on hyphenated unknown values.** `privacy: private-ish` matches at both public and confidential (the `-` satisfies `\b`), as `privacy: confidential-draft` did on `main`. Pre-existing `\b` semantics, unchanged by this fix, and it errs toward flagging — the safe direction for a privacy scanner. No action.

**m2 — unenumerated values are silently allowed at the confidential tier.** `privacy: restricted` fails at public only via `not_public_pair`; at confidential there is no pair check (`requirePublicPair: false`), so an unrecognized-and-possibly-sensitive value passes. Pre-existing design, out of scope for this task, worth a separate ticket if the value vocabulary ever grows.

**m3 — `/tmp/git-tools-scratch` left behind by a prior agent.** Confirmed on disk, and confirmed the worktree-gate refuses removal because the copied `.git` marks it a primary checkout. Report's account is accurate. Housekeeping only, outside every repo; I did not work around the safety gate. My own scratch avoided this by excluding `.git`, and removed cleanly.

## Fixes applied

1. **`go/githooks/adversarial_test.go`** — added `TestScanPrivacyForbiddenMarkerTierMatrix`, a table-driven test pinning the whole rule in one place: 9 values x 3 tiers, counting only `forbidden_marker` findings so the public tier's `not_public_pair` cannot mask a lost alternative. Covers legacy `internal` at public, `private`-only at confidential (not `internal`, not its own `confidential`), case-insensitivity, and word-boundary anchoring for `privateish`/`internally`/`confidentially`. Mutation-verified: **catches all 5** mutations above, including the 3 the delivered suite missed.
2. **`.task-reports/fix-forbidden-marker-tiers-report.md`** — corrected the reversed rename claim in both places; replaced the bogus "separate ticket" hand-off with an accurate merge note (clean `merge-tree`, `git-tools` builds unpatched against the merged module, nothing to repin).

No change to `go/githooks/privacy.go`. The implementer's regexes are exactly right.

## Re-verification

Branch, after my edits, from `go/githooks`: `gofmt -l .` clean, `go build ./...` clean, `go vet ./...` clean, `go test ./... -count=1` **ok** (0.055s). New matrix test: all 27 subtests PASS. Merged tree (`merge-tree main HEAD`) also clean on all four gates, and `git-tools` builds against it unpatched.

## Test-suite assessment

**Adequate after fix 1; inadequate as delivered.** The three delivered tests are behaviorally correct and the rewritten `TestScanPrivacyTierIsParameterized` genuinely asserts corrected behavior rather than restating the bug — `privacy:private` is the right choice, being the one value more sensitive than both other tiers. But the suite tested the delta, not the rule, and left the two most regression-prone properties (legacy `internal`, word boundaries) undefended. Both reports verified those properties with throwaway probes and then discarded the evidence; a probe that is deleted is not coverage. Standing note for the test-engineer: when a report's own verification relies on an ad-hoc probe, that probe is the missing test.

## Residual risk

- m1/m2 accepted as pre-existing and out of scope.
- Landing this branch carries `main`'s `AllowedDomains` naming forward. `merge-tree` says clean and the merged module passes all gates plus the `git-tools` build, so the risk is discharged rather than merely assessed.

## Plan feedback

- **Root cause is a cross-taxonomy port, not a typo.** The Go module was ported from `scripts/check_privacy.py` with the tier vocabulary renamed and the `owner:` axis dropped, but the regexes copied verbatim. Two of the three tiers were wrong as a direct result. Any future port of this table should re-derive the rule from the tier ordering rather than transliterate the patterns — and a matrix test like the one added here makes the rule explicit enough that the next rename cannot silently break it.
- **Consider deriving `forbiddenMarkers` from the tier ordering** instead of hand-writing three regexes. The tiers are already declared strictest-to-loosest; a table that computes "every value above mine" would have made this class of bug unrepresentable. Out of scope here; worth a ticket.
- **The sibling `check_privacy.py` copies are a live divergence risk** even though they need no fix today: two independent implementations of the same guardrail with different tier vocabularies. If those repos are meant to migrate to `git-tools scan privacy`, the mapping between the two vocabularies should be written down before they do.
