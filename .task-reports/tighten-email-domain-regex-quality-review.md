# Quality Review: tighten-email-domain-regex

## Verdict
**FIX-APPLIED** — the test-verification FAIL was correct. The gap is fixed in
`go/githooks/privacy.go` and now covered by a load-bearing test. One
non-blocking major finding is documented in-code and handed to the plan.

## Finding confirmed independently

The asymmetry is real. As authored, the pattern was:

```
(?i)\b[\w.+-]+@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b
                 \_____________ capped at 63 ______________/       \_ uncapped _/
```

Only the repeated group caps a label (1 + 61 + 1 = 63). The trailing
`[a-zA-Z]{2,}` — the TLD — had no upper bound, so
`jane@sub.` + 64×`a` matched. The doc comment's "Every label is also capped
at 63 characters" was therefore false for exactly one label position.

## Fix chosen: option (a), `[a-zA-Z]{2,63}`

Option (a) over (b) because the 63-character cap is the *design intent* the
task and the comment both state, and DNS's limit is not TLD-exempt. A
documentation retreat (option b) would leave the pattern describing a rule it
does not enforce, and the next person to widen the TLD class would inherit
that asymmetry silently.

Of the two forms the task floated, `{2,63}` is the correct one and the other
is a trap:

| form | lengths accepted | verdict |
|---|---|---|
| `[a-zA-Z]{2,63}` | 2–63 | correct |
| `[a-zA-Z](?:[a-zA-Z]{0,61}[a-zA-Z])?` | **1**, 2–63 | wrong — the optional group makes length 1 legal, regressing the letters-only-and-2+ rule that closes the `bar@a1` class |

RE2 bounded repetition verified directly, not assumed:

- `{2,63}` compiles: confirmed.
- RE2's repeat-count ceiling is 1000 — `a{2,1000}` compiles, `a{2,1001}`
  fails with `invalid repeat count`. 63 is far inside the limit.
- Behavior at the `\b` interaction verified: with a 64-character TLD, RE2
  matches 63 letters and then fails `\b` (the next char is a word char), and
  cannot recover by re-anchoring, because `@` fixes where the domain starts.
  Rejection is total, not a truncated partial match. Confirmed for a 64-char
  TLD both at end-of-line and mid-line followed by a space.

### Files changed by this review

- `go/githooks/privacy.go:170` — `[a-zA-Z]{2,}` → `[a-zA-Z]{2,63}`.
- `go/githooks/privacy.go:160-168` — doc comment. The label-cap sentence is
  now true as written, with the `{2,63}` bound explained so a future edit
  does not re-open the asymmetry. The punycode sentence is corrected (see the
  major finding).
- `go/githooks/adversarial_test.go:373-414` — boundary coverage, below.

## Findings

### Blocking
None.

### Major
**`go/githooks/privacy.go:170` — an address at a punycode IDN TLD now yields
a truncated domain, so it cannot be allow-listed.** Introduced by this task's
change, not by my fix, and verified across all three pattern generations
against `contact jane@acme.xn--80ak6aa92e today`:

| pattern | match | `emailDomain(match)` |
|---|---|---|
| pre-task | `jane@acme.xn--80ak6aa92e` | `acme.xn--80ak6aa92e` |
| as authored | `jane@acme.xn` | `acme.xn` |
| with my fix | `jane@acme.xn` | `acme.xn` |

`[a-zA-Z]{2,63}` stops at `xn`, and `\b` is satisfied because the next
character is `-`. Consequence, confirmed end-to-end: with
`AllowedDomains: ["acme.xn--80ak6aa92e"]` the address is *still* flagged,
because the extracted domain is `acme.xn`.

Not blocking, for three reasons:
- Detection is unimpaired. The address is still flagged, so the check never
  goes quiet on a real identifier. The degradation is confined to the
  false-positive-suppression side.
- Blast radius is a punycode IDN TLD specifically. Punycode in any earlier
  label is unaffected (`jane@sub.xn--80ak6aa92e.com` matches in full).
- The correct fix re-admits digits and hyphens into the TLD class (e.g.
  `[a-zA-Z]{2}(?:[a-zA-Z0-9-]{0,60}[a-zA-Z0-9])?`), which is the exact axis
  this task deliberately tightened. That is a scoped design decision needing
  its own false-positive review, not a review-time patch. Folding it in here
  would be gold-plating past acceptance and would risk the `bar@a1` class.

Handled by documenting the behavior precisely in the doc comment so it is
discoverable, plus plan feedback below.

### Minor
- `.task-reports/tighten-email-domain-regex-report.md:21` — says "added four
  regression tests" and then lists three. Cosmetic report inaccuracy. The
  code was correct on this point.
- `go/githooks/adversarial_test.go` (as authored) — the overlong-label test
  was named for "label" generally but exercised only the SLD position. That
  naming is what let the TLD gap sit unnoticed behind a green test. Fixed by
  extending the test to both positions rather than renaming it narrower.

## Fixes applied

1. **Regex bound** — `privacy.go:170`, `{2,}` → `{2,63}`. Confirmed
   behaviorally surgical: across 20 probe cases, the only outputs that
   changed versus the as-authored pattern are the three that should
   (64-char TLD, 64-char TLD mid-line, 100-char TLD: `true` → `false`).
   Nothing else moved.
2. **Doc comment** — the "every label capped at 63" claim is now true as
   written, the bound is named so the asymmetry cannot silently return, and
   the punycode sentence, which previously read "A punycode IDN TLD still
   matches, as it starts with the letters 'xn'", now states accurately that
   the address is flagged but only `xn` falls inside the match, so
   `emailDomain` reports a truncated domain and such an address cannot be
   allow-listed. Every clause of the replacement was verified by execution.
3. **Boundary tests** — `TestScanPrivacyEmployeeEmailOverlongLabelIsNotAnAddress`
   extended from one SLD case to both label positions at 64 characters. New
   `TestScanPrivacyEmployeeEmailMaximumLengthLabelStillFlags` covers the
   accepting side (63 characters) in both positions. No test was weakened or
   removed — the original assertion is retained, with a second case added.

**Mutation check** (the new coverage is not green-but-hollow): reverting
`{2,63}` → `{2,}` in an isolated `/tmp` copy of the module makes
`TestScanPrivacyEmployeeEmailOverlongLabelIsNotAnAddress` fail on exactly the
reported gap — `jane@sub.` + 64×`a` produced one `internal_identifier`
warning where zero were expected. The test detects the bug it was written
for.

## Re-verification

Fresh, after the fix, scoped to the `go/githooks` module. This repo is
one `go.mod` per package under `go/`, so the implementer's deviation note is
correct.

```
gofmt -l .                  -> no output (clean)
go build ./...              -> success
go vet ./...                -> success
go test ./... -count=1 -v   -> PASS: 65 --- PASS, 0 --- FAIL
```

65 versus the prior run's 64 top-level passes, matching the one test added.

Content guardrails: `git-tools scan all --staged` reports 0 secrets, 0 raw
binaries, 0 privacy violations. The 49 privacy *warnings* are the unchanged
repo-wide baseline from deliberate test fixtures and prior task reports, not
new.

### Re-verification of the points the test-verification report had passed

Re-run end-to-end through `ScanPrivacy` against the *fixed* pattern, not
assumed to be untouched. All 20 cases as expected:

| case | expected warnings | result |
|---|---|---|
| `foo@1.0.0`, `git-tools@v0.5.0`, `cache@127.0.0.1` | 0 | pass |
| `bar@a1`, `foo@bar.a1` | 0 | pass |
| SLD 63 accepts / SLD 64 rejects | 1 / 0 | pass |
| TLD 63 accepts / TLD 64 rejects | 1 / 0 | pass (new) |
| TLD 64 mid-line, TLD 100, TLD 1 reject | 0 | pass (new) |
| `jane@acme-corp.com` | 1 | pass |
| `(?i)`: uppercase local part, uppercase domain | 1 / 1 | pass |
| punycode in SLD, punycode as TLD | 1 / 1 | pass |
| hyphenated domain, digit-led host label | 1 / 1 | pass |
| `user@example.com` (default exemption) | 0 | pass |

The `(?i)` decision holds. Both case directions still match, and with the
domain half now fully explicit `[a-zA-Z]`/`[a-zA-Z0-9]` and the local part on
`\w`, the flag is behaviorally inert. Keeping it is the right call — it is
zero-risk, and removing it is a change with no upside.

## Test-suite assessment

Adequate now. The label-length boundary is covered on both sides (63 accepts,
64 rejects) in both label positions, which is the shape this class of bug
needs. The as-authored suite tested only the rejecting side in only one
position, which is precisely why a green suite hid the gap. Coverage also
holds the three original false positives, the `bar@a1` class, the digit-led
label carve-out, punycode, and hyphenated domains.

Remaining gap, deliberately not closed here because it belongs with the
punycode finding rather than with this fix: nothing asserts the *matched
text* or the extracted domain, only the warning count. A test on
`emailDomain`'s output would have caught the punycode truncation at authoring
time. Worth adding alongside any follow-up that revisits the TLD class.

## Residual risk

- A punycode IDN TLD cannot be allow-listed (major finding above). Detection
  is unaffected, and the behavior is documented in-code.
- 63 is the DNS *label* limit. The pattern still does not enforce the
  253-character limit on a full domain name. Out of scope, no practical
  false-positive consequence, and it was not in scope before this task
  either.
- Single test run. No flake surface here — the pattern is a pure function of
  its input and the tests are hermetic `t.TempDir()` fixtures.

## Plan feedback

1. **New task worth filing:** decide the TLD class's treatment of punycode
   deliberately. Either widen it to
   `[a-zA-Z]{2}(?:[a-zA-Z0-9-]{0,60}[a-zA-Z0-9])?`, which keeps `bar@a1`
   excluded because two leading letters are still required while matching a
   punycode TLD in full, or accept the truncation and state it as intended.
   The decision needs a false-positive sweep, because it re-admits digits and
   hyphens into the one label position this task just restricted. Add
   domain-extraction assertions with it.
2. **Verification-scope note for the plan:** the task's suggested verify
   commands assume a single-module repo. This repo has one `go.mod` per
   package under `go/`, so verify commands should be stated as module-scoped
   from the outset rather than left for each agent to re-derive.
3. **Pattern for the test-engineer brief:** a length cap expressed in two
   different regex constructs needs boundary tests per *construct*, not per
   *rule*. The as-authored suite tested the rule once and inferred the rest.
   That inference is what failed.
