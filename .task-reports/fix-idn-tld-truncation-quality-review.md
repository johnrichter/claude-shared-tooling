# Quality review: fix-idn-tld-truncation

**Verdict: FIX-APPLIED** — the regex fix is correct as committed; I added the
missing length-cap regression test and tightened the doc comment. No change to
the pattern itself.

## Scope reviewed
`200b24a` (fix) and `07c9968` (test verification) on
`chore/fix-idn-tld-truncation`: `go/githooks/privacy.go`,
`go/githooks/adversarial_test.go`, both task reports.

## 1. `{0,57}` arithmetic — independently confirmed
Derived, not copied from either report:

| component | chars |
|---|---|
| literal `xn--` | 4 |
| leading `[a-zA-Z0-9]` | 1 |
| `[a-zA-Z0-9-]{0,57}` | 0-57 |
| trailing `[a-zA-Z0-9]` | 1 |
| **max total** | **63** |

4 + 1 + 57 + 1 = 63, exactly DNS's label limit. Confirmed empirically against
the compiled pattern: labels of 5/62/63 chars match in full, 64/65 do not.
Minimum punycode label is 5 chars, so the branch can never match shorter than
the letters-only branch's floor.

## 2. No new false-positive class — proven, not sampled
Ran the new and the pre-fix patterns side by side over ~35 adversarial inputs
(malformed punycode, digit/hyphen-laden TLDs, mixed case, underscores,
version specifiers, IPv4 hosts, path fragments, Unicode). **Every input's
match/no-match state is identical between the two patterns**; only match
*extents* differ.

That is structural, not luck: any position where the punycode branch matches
begins with `xn`, which the letters-only branch already matched (the following
`-` is a non-word char, so `\b` was always satisfied). The punycode branch can
therefore only ever *lengthen* an already-existing match — it cannot make a
non-address start matching. So the fix cannot add a flag, only make an
existing flag's domain accurate. That is the correct direction for an
allow-list-polarity check.

Corollary also checked: a longer match cannot *swallow* a second address,
since the extended region admits only `[a-zA-Z0-9-]` and can contain no `@`.
Warning counts are unchanged for every probed input.

## 3. Cross-contamination with the forbidden-marker tier fix — none
The two regexes are independent values with no shared state, applied on
different inputs (`forbiddenMarkers` against `frontmatterBlock(text)` at
`privacy.go:315`; `employeeEmailPattern` whole-file via `internalID` at
`privacy.go:337`). Full verbose suite run confirms
`TestScanPrivacyForbiddenMarkerTierMatrix` (26 subtests) and every other
marker test passes unchanged.

Also confirmed no parallel implementation of this pattern exists elsewhere in
the repo — `privacy.go` is the sole definition, so the fix is not half-wired
across a second language binding.

## Findings

### Major — test adequacy: the `{0,57}` cap had no regression test
`go/githooks/privacy.go:183` — the punycode branch introduces a **third**
length-bounded label position whose bound is *derived* (`{0,57}`) and
*differs* from the `{0,61}` its two siblings carry. The file's own established
convention, set by the immediately preceding review pass, is to pin that
invariant on both sides in every position
(`adversarial_test.go:437` and `:459` do exactly that for the letters-only
positions). The punycode position had neither half.

Proven, not asserted: mutating `{0,57}` → `{0,61}` — which raises the cap to
4+1+61+1 = **67 characters, 4 past the DNS limit** — left the entire 72-test
suite **green**. A future "make these consistent" cleanup would have silently
reopened the label-length gap that the previous task in this chain was opened
to close.

**Fixed** by adding `TestEmployeeEmailPatternPunycodeLabelLengthCap`
(`adversarial_test.go:520`). Verified the test is load-bearing: re-applying the
`{0,61}` mutation now fails it. Asserted on match extent rather than warning
count, because an overlong punycode label still falls back to the
letters-only `xn` match and is still flagged either way — only the extent
distinguishes correct from incorrect.

### Minor — doc comment omitted the two facts a future editor most needs
`privacy.go:160-180` — the comment described *what* both branches match but
not (a) that the punycode branch's **first** position is load-bearing under
leftmost-first alternation, nor (b) where `{0,57}` comes from. Both are
exactly the things a well-intentioned edit would break. The alternation order
*is* covered by `TestEmployeeEmailPatternPunycodeTLDMatchesInFull`, so this is
maintainability, not correctness.

**Fixed** — added the ordering rationale (including the monotonicity property
from §2, which is the reason this change cannot introduce a false positive)
and the cap derivation.

### Minor — orphaned parenthesis in the comment wrap
`privacy.go:173` (pre-fix) — wrapped as `the address was still (` /
`// correctly) flagged`, breaking the parenthetical across the line break.
**Fixed** by dropping the now-unnecessary parentheses.

## Fixes applied
| file | change |
|---|---|
| `go/githooks/adversarial_test.go` | +`TestEmployeeEmailPatternPunycodeLabelLengthCap` (paired 63/64 punycode-label boundary) |
| `go/githooks/privacy.go` | comment only — alternation-order rationale, `{0,57}` derivation, paren-wrap fix |

The regex itself is **byte-identical to the implementer's commit**. No
behavior change in my edits.

## Re-verification (fresh, after my fixes)
```
gofmt -l .                  -> no output (clean)
go build ./...              -> ok
go vet ./...                -> ok
go test ./... -count=1 -v   -> ok, 73 top-level PASS, 0 FAIL, 0 SKIP
```
(72 before my added test, 73 after.)

## Test-suite assessment
Adequate after the added test. The implementer's four tests are well chosen —
in particular `TestScanPrivacyEmployeeEmailAllowedIDNDomainConfigDoesNotFlag`
proves the bug at the level a caller actually observes it, not just at the
regex, and `...PunycodeTLDMatchesInFull` asserts the exact extent, so it does
catch an alternation reorder (the implementer's hand-off note was
over-cautious here — I confirmed the reorder fails that test). The one real
gap was the length cap, now closed.

## Residual risk
- **Unicode U-label IDN domains are not detected at all.** `jane@acme.рф`
  produces **no match** — the pattern is ASCII-only (`\w` is ASCII in Go's
  regexp). This is a pre-existing false *negative* in a privacy scanner, not
  introduced or worsened by this change, and out of this task's acceptance.
  See plan feedback.
- An invalid-DNS final label containing `_` (e.g. `dom.xn--a_b`) still yields
  a truncated `dom.xn` domain for the allow-list lookup, because `_` is a
  word char so `\b` never fires. The address is still correctly flagged, and
  no operator would allow-list an invalid domain. Accepted.
- A 64+ char punycode label is still flagged (truncated fallback), whereas a
  64+ char letters-only label is not flagged at all. A mild asymmetry, but it
  errs toward flagging, which is the safe direction here. Accepted.

## Plan feedback
Worth a follow-up task, not a change here: **U-label (non-punycode) IDN
addresses are invisible to `employeeEmailPattern`.** An employee address at a
Unicode domain passes the public-tier scan silently. Closing it means moving
every label class off ASCII-only and reckoning with Go's ASCII-only `\b` —
materially larger than this task's "one label's character class" framing, and
it should be scoped and reviewed on its own rather than folded in.
