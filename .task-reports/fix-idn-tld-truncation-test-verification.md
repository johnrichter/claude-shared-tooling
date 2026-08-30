# Test verification: fix-idn-tld-truncation

Independent verification of commit `200b24a` ("Fix punycode IDN TLD
truncation in employeeEmailPattern") in `go/githooks/privacy.go` and
`go/githooks/adversarial_test.go`. All checks below were run fresh, not
copied from the implementer's report.

## 1. Diff read
Confirmed via `git show 200b24a -- go/githooks/privacy.go go/githooks/adversarial_test.go`.
Pattern changed from:
```
(?i)\b[\w.+-]+@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}\b
```
to:
```
(?i)\b[\w.+-]+@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+(?:xn--[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,57}[a-zA-Z0-9])?|[a-zA-Z]{2,63})\b
```
Only the final-label alternation changed; local-part and repeated-label group
untouched. Doc comment rewritten to describe both branches. 4 new tests added
in `adversarial_test.go`. **PASS**.

## 2. Full punycode match + emailDomain extraction
Ad hoc probe (not the implementer's test) against the built package:
- `employeeEmailPattern.FindString("jane@acme.xn--80ak6aa92e")` → `"jane@acme.xn--80ak6aa92e"` (full, untruncated).
- `emailDomain(match)` → `"acme.xn--80ak6aa92e"` (full domain, not `"acme.xn"`).
**PASS**.

## 3. End-to-end ScanPrivacy allow-list exemption
Ran the implementer's `TestScanPrivacyEmployeeEmailAllowedIDNDomainConfigDoesNotFlag`
directly (not just read it): `AllowedDomains: ["acme.xn--80ak6aa92e"]`,
fixture contains `jane@acme.xn--80ak6aa92e` and `root@other.com`. Result: exactly
one warning (`root@other.com`); `jane@...` correctly exempted. **PASS**.

## 4. Bare `xn--` does not match as complete TLD
Ad hoc probe: `employeeEmailPattern.FindString("foo@bar.xn--")` → `"foo@bar.xn"`
(falls back to the pre-existing 2-letter letters-only match; the trailing
`--` is never consumed as part of a complete TLD). Confirmed this is the
same behavior any 2-letter alpha TLD produces, not a new gap. **PASS**.

## 5. `{0,57}` boundary arithmetic (63 vs 64 total label length)
Constructed independently, not reusing the report's examples:
- 63-char label (`xn--` + 59 `a`s, len verified = 63): address
  `jane@acme.xn--aaaa...a` (63-char label) matches **in full** via the
  punycode branch. Confirmed length of the label with `len()` before
  asserting.
- 64-char label (`xn--` + 60 `a`s, len verified = 64): full match does
  **not** occur; engine falls back to the truncated letters-only match
  `jane@acme.xn`, exactly the documented pre-existing fallback behavior
  and not a silent match past the DNS 63-char label limit.

**PASS**.

## 6. Original false-positive exclusions re-confirmed
`foo@1.0.0`, `tool@v0.5.0`, `cache@127.0.0.1`, `bar@a1` — none match
`employeeEmailPattern.MatchString`. Independently probed via
`MatchString`, not `FindString`, to be certain no partial match sneaks
through. **PASS**.

## 7. Alternation order is load-bearing
Made a temporary edit to the committed `privacy.go` (reverted after, not
committed): swapped the alternation to
`(?:[a-zA-Z]{2,63}|xn--[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,57}[a-zA-Z0-9])?)\b`
(letters-only branch first). Re-ran the same ad hoc probes:
- `jane@acme.xn--80ak6aa92e` → truncated to `jane@acme.xn` again (bug
  reproduced).
- `emailDomain` → `acme.xn` (truncated, wrong).
- 63-char boundary case → truncated to `jane@acme.xn` instead of matching
  in full.
This confirms the order is genuinely load-bearing under Go's leftmost-first
alternation, not cosmetic. Change reverted immediately after
(`git diff --stat` and `git status --porcelain` on `privacy.go` both empty
post-revert — confirmed the working tree is byte-identical to HEAD). No
scratch change was committed. **PASS**.

## 8. Fresh full build/vet/test run
All run fresh from `go/githooks` in the worktree:
```
gofmt -l .                          -> no output (clean)
go build ./...                      -> success
go vet ./...                        -> success
go test ./... -count=1 -v           -> ok, all tests PASS, 0 failures
```
Confirmed the 4 new tests plus the pre-existing
`TestScanPrivacyEmployeeEmailIDNAndHyphenatedDomainsStillFlag` all appear
and PASS in the verbose output:
- `TestEmployeeEmailPatternPunycodeTLDMatchesInFull` — PASS
- `TestEmployeeEmailPatternBareXNPrefixDoesNotMatchAsTLD` — PASS
- `TestScanPrivacyEmployeeEmailAllowedIDNDomainConfigDoesNotFlag` — PASS
- `TestScanPrivacyEmployeeEmailFalsePositiveExclusionsStillHold` — PASS
- `TestScanPrivacyEmployeeEmailIDNAndHyphenatedDomainsStillFlag` (pre-existing) — PASS

Note: `go/githooks` is its own Go module (each `go/*` directory is a
separate module in this repo); `go test ./...` scoped to `go/githooks` is
the correct invocation per the task instructions, not a narrowed scope.

**PASS**.

## Coverage / gaps
- All 8 verification points from the dispatch pass with independent,
  freshly-constructed probes (not copy-pasted from the implementer's
  report or tests).
- No coverage gap found. The only residual note: the 253-character
  full-domain-name limit (mentioned as out of scope in the report) was not
  re-verified here since it is unchanged by this diff and outside this
  task's acceptance criteria.
- No flakes observed; all runs deterministic (regex/string tests, no
  concurrency or time dependency).

## Verdict
**PASS** — all 8 independent verification points confirmed. Fix is correct,
alternation order is genuinely load-bearing (proven by reverting it and
reproducing the bug), boundary arithmetic is correct at the 63/64-char
edge, original exclusions and end-to-end `ScanPrivacy` allow-list behavior
all hold. Full suite green with `gofmt`/`build`/`vet`/`test` all clean.
