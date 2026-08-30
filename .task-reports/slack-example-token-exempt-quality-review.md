---
name: Slack example-token exemption — quality review
description: "Final quality gate on the generalized exact-match secret exemption in go/githooks/secrets.go. Rules the Python mirror gap in scope and fixes it: the branch as committed failed this repo's own CI. Adds four boundary tests and identifies the real marketplace-datadog blocker as a different file. Verdict: ACCEPT WITH FIXES."
id: doc:ai-shared-lib:slack-example-token-exempt-quality-review
tags:
  - type:knowledge
  - topic:git-governance
  - topic:secret-scanning
  - status:complete
  - privacy:public
  - owner:operator
links: []
updated: 2026-08-30T11:20:00Z
---

# Slack example-token exemption — quality review

## Verdict

**ACCEPT WITH FIXES.** The Go generalization under review is correct, idiomatic, and
free of widening. Three fixes were applied, one of them a blocking CI failure the two
prior agents both missed.

## 1. The Go generalization — correctness and design

Clean. No changes needed to `go/githooks/secrets.go`.

| Lens | Finding |
| --- | --- |
| Generalization | Real. `matchesSecretPattern` (`secrets.go:82`) does one `exactExemptions[p.label]` lookup, no per-label `if`. Both labels register into the same map. |
| Widening risk | None. Exemption is a Go map lookup on the full regex match — exact bytes, case-sensitive, never substring. |
| Case sensitivity | Non-issue. Both regexes require the literal lowercase prefix (`xox[baprs]`) or uppercase (`AKIA`), so no case variant reaches the lookup. |
| Label-drift | Prevented by construction. `labelSlackToken` (`secrets.go:29`) is now the single source for both the pattern table and the exemption key, matching how `labelAWSAccessKeyID` already worked. |
| Wiring | Confirmed end-to-end, not assumed. `privacy.go:282-283` shares `secretPatterns` and `matchesSecretPattern`, so `ScanPrivacy` inherits the Slack exemption automatically. Pinned by the pre-existing `TestScanPrivacyHonorsAWSDocPlaceholderAllowlist`. |
| Doc comments | Accurate, at the file's own density. No cross-language archaeology, no dead references. |

### The one real edge case, now pinned

The Slack regex `\bxox[baprs]-[0-9A-Za-z-]{10,}` has **no trailing `\b`**, unlike the
`\b`-anchored AWS regex. So `FindAllString` relies on greedy matching to consume the whole
token-character run. A longer token starting with the placeholder
(`<placeholder>` + `DEADBEEF`) therefore yields the full string, not the exempt prefix, and
is still flagged. Behavior is correct — but it was **unproven**.

The implementer's report argued Slack edge-case tests were redundant "since both patterns
now run through the identical code path" (report lines 61-66). That reasoning is wrong for
this case: the *code path* is shared, but the *regexes differ in anchoring*. No AWS test can
cover an open-ended tail. Test added on both sides.

## 2. Python-mirror scope decision: IN SCOPE, fixed now

**Decision: fixed in this task.** The reasoning is not the one the dispatch anticipated.

### What the dispatch expected, and what is actually true

The dispatch asked whether `marketplace-datadog` CI invokes `check_secrets.py` on the corpus
file. **It does not.** Verified evidence:

- `marketplace-datadog/.github/workflows/ci.yml` runs `check_privacy.py`,
  `check_no_bare_tool.py`, `check_no_raw_binary.py`. No `check_secrets.py`.
- `marketplace-datadog/.githooks/pre-commit` runs the same three. No `check_secrets.py`.
- The only `marketplace-datadog` references to `check_secrets.py` are prose in the
  `oss-release/scrub-for-release` skill, which invokes it by path as an **agent-run release
  gate**, not automated CI.

So the dispatch's stated trigger did not fire. The gap is in scope anyway, for a stronger
reason the dispatch did not anticipate.

### The actual blocker: this branch failed this repo's own CI

`ai-shared-lib` runs `python3 scripts/check_secrets.py --root .` in **three** workflows
(`ci.yml:61`, `codegov.yml:38`, `agent-contract.yml:42`) and in `.githooks/pre-commit:7`.
Run against the branch as committed at `9d0a6db`:

```
FAIL — 3 possible secret(s):
  - .task-reports/slack-example-token-exempt-report.md: possible Slack token
  - .task-reports/slack-example-token-exempt-test-verification.md: possible AWS access-key id
  - .task-reports/slack-example-token-exempt-test-verification.md: possible Slack token
(exit 1)
```

The Go code and Go tests were correctly fragment-assembled. **The two committed task-report
files were not** — they hold the doc string, plus two deliberately non-exempt synthetic
fixtures, as plain literals. The branch was unmergeable on its own terms. Deferring the
Python mirror to a follow-up would have shipped a red branch.

This is the same class of failure the implementer's own report cites as historical
precedent (report lines 103-109): a prior Go-only fix broke CI via the Python mirror. The
precedent recurred inside the very task that documented it.

### Cross-repo plan feedback: the marketplace-datadog blocker is a different file

The original corpus blocker is **not** solved by this task, and would not have been solved
by any amount of `check_secrets.py` work. Evidence, from running the real scanner
read-only against the real corpus directory:

```
$ python3 marketplace-datadog/scripts/check_privacy.py --tier datadog --strict \
    --root .../datadog-code-knowledge-agent/corpus
FAIL — 3 privacy violation(s):
  - chunks.jsonl: possible AWS access-key id
  - chunks.jsonl: possible Slack token
  - chunks.jsonl: possible GitHub token
```

`marketplace-datadog/scripts/check_privacy.py` carries its own private copy of all four
secret regexes (lines 120-125) and **no exemption mechanism at all** — not even the AWS one
that has existed on the `ai-shared-lib` side for two releases. Its own docstring states
"Whole-file, all tiers, no exemptions" (line 22).

Consequences for the orchestrator:

1. A separate task is required against `marketplace-datadog` to port the
   label-to-exemption-set mechanism into its `check_privacy.py`. Out of this worktree's
   write boundary.
2. That task is **larger than a Slack exemption**. The corpus trips three of four patterns.
   The AWS and GitHub hits need the same operator provenance confirmation the Slack string
   already received before either can be exempted.
3. Five further sibling repos (`marketplace`, `knowledge-public-datadog`,
   `knowledge-private-datadog`, `knowledge-private-personal`, `ai-shared-lib-datadog`) each
   hold a `check_privacy.py` with the same duplicated regex set. The duplication itself is
   the root defect: six copies of a security-critical pattern set with divergent exemption
   support. Worth a consolidation task.

## 3. All findings

### Blocking (fixed)

- **`scripts/check_secrets.py:29-60` — no Slack exemption; branch failed this repo's own
  CI.** Three findings on the committed tree, as shown above. The Python mirror is
  explicitly documented as staying "behaviorally identical" to the Go side
  (`check_secrets.py:44-45`), and it had silently diverged.
- **`.task-reports/slack-example-token-exempt-test-verification.md:69,88` — two
  deliberately non-exempt synthetic secrets committed as plain literals.** These are correct
  as test fixtures and must stay non-exempt, so the exemption set could not absorb them.
  Fixed by applying the repo's own fragment-split convention to the prose.

### Major (fixed)

- **`go/githooks/sanity_test.go` — no test for the Slack regex's missing trailing `\b`.**
  Per section 1. The implementer's stated rationale for omitting Slack edge-case tests does
  not hold for anchoring differences.
- **`tests/test_check_secrets.py` — Slack exemption arrived untested on the Python side.**
  Three tests added, mirroring the AWS trio.

### Minor (fixed)

- **`tests/test_check_secrets.py:7-8` — the fragment-split convention had an undocumented
  trap.** My own first near-miss fixture split one character from the end, mirroring the AWS
  fixture's style, and tripped the scanner: unlike the `\b`-anchored AWS pattern, the
  open-ended Slack pattern matches on any first fragment carrying 10+ post-dash characters,
  so the split must fall within 10 characters of the prefix. Documented in the module
  docstring so the next contributor does not repeat it. Caught only because the guardrail was
  actually run — which is the point, and it recurred while writing this report.

### Not a finding, recorded for the record

- `exactExemptions`'s `map[string]map[string]bool` shape is right for two entries. No change.
- Skipping a byte-for-byte Slack duplicate of the *mixed placeholder-plus-real-key* and
  *word-boundary* AWS tests is a sound call — those genuinely do pin the shared code path.
  Only the anchoring asymmetry needed its own test.

## 4. Fixes applied

| File | Change |
| --- | --- |
| `scripts/check_secrets.py` | Added `SLACK_TOKEN_LABEL`, `SLACK_EXAMPLE_TOKENS` (fragment-assembled), and `EXACT_EXEMPTIONS`; rewrote `matches_pattern` to a label lookup. Mirrors the Go generalization exactly, including the `Mirrors <Go symbol>` comment convention already used for the AWS set. Updated the module docstring. |
| `tests/test_check_secrets.py` | Added `_SLACK_DOC` / `_SLACK_NEAR_MISS` fixtures and three tests: exemption applies, exemption is exact not fuzzy, appended-token-chars still fails. Documented the Slack fragment-split trap. |
| `go/githooks/sanity_test.go` | Added `TestScanSecretsStillFlagsSlackPlaceholderWithAppendedChars`. |
| `.task-reports/slack-example-token-exempt-test-verification.md` | Fragment-split the two non-exempt synthetic secrets. Added a reviewer-correction note to section 6, whose "out of scope — not fixed here" conclusion is now stale. |

`go/githooks/secrets.go` — **unchanged.** The mechanism under review needed no correction.

## 5. Re-verification (fresh, after fixes)

```
$ cd go/githooks && go build ./...          OK
$ cd go/githooks && go vet ./...            OK
$ cd go/githooks && go test ./... -count=1
ok  github.com/johnrichter/claude-shared-tooling/go/githooks  0.041s

$ python3 -m unittest tests.test_check_secrets -v
Ran 19 tests — OK

$ python3 -m unittest discover -s tests -p "test_*.py"
Ran 361 tests in 235.810s — OK (skipped=1)

$ python3 scripts/check_secrets.py --root .
OK — no secrets found.   (exit 0)   [was: FAIL, 3 findings, exit 1]
```

The last line is the load-bearing one: this repo's own required CI check went from red to
green. `go/githooks` exemption tests confirmed individually green, including the new
boundary test and the pre-existing `ScanPrivacy` allowlist test.

## 6. Test-suite assessment

Adequate now; it was not before. The test-engineer's verification was rigorous on the Go
mechanism — independent ad-hoc fixtures, both directions, both labels — and correctly
identified the Python gap. Two gaps in its method:

1. **It never ran the repo's own guardrail against the branch.** It ran
   `check_secrets.py` against a synthetic `/tmp` tree to prove the gap existed, but not
   against `--root .`, which is what CI does. One command would have surfaced the blocker.
   Recommend adding "run every guardrail this repo's CI runs, at `--root .`" to the
   test-engineer's standing checklist.
2. **It accepted the implementer's shared-code-path argument at face value** rather than
   diffing the two regexes' anchoring.

## 7. Residual risk

- **The corpus file is still blocked in `marketplace-datadog`.** Accepted knowingly: outside
  this worktree's write boundary, and larger than this task. Section 2 has the follow-up.
- **The Go and Python exemption sets remain two hand-synchronized copies.** Nothing
  mechanically enforces parity; the comments assert it and this review restored it. A
  drift test is the right fix but is scope beyond this task.
- **Version.** The implementer proposed `go/githooks/v0.6.0`. Still right: additive
  unexported internals plus new scanner behavior, no signature change. The Python-side fix
  ships in the same commit range and needs no separate version, as `scripts/` is untagged.
