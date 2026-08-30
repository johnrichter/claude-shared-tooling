# Test verification: fix-hostname-sentinel-lookahead

## Deliverable type
Code (Go). Verification form: diff review against Python reference + adversarial
test authoring + build/vet/test tooling + manual bisection (stash-equivalent).

## What I tested
- Read the actual diff (`git diff main -- go/githooks/privacy.go go/githooks/adversarial_test.go`)
  rather than trusting the report's prose.
- Compared `reservedSentinelSuffix` against the live Python reference
  (`_RESERVED_SENTINEL_LOOKAHEAD` / `_HOST_TERMINATOR` in
  `marketplace/scripts/check_privacy.py`, confirmed identical across sibling repos).
- Added 4 throwaway adversarial tests of my own (not committed — removed after
  running) beyond the implementer's two, covering: disguised sentinel with
  trailing host content, case-insensitivity, cross-file non-contamination, and
  same-file double-sentinel-plus-real-host non-double-counting.
- Ran `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test ./go/githooks/... -count=1 -v` fresh.
- Reverted `privacy.go` alone to its pre-fix content (`git checkout HEAD~1 --
  go/githooks/privacy.go`, tests untouched at HEAD) and re-ran the two new
  tests to confirm they fail exactly as claimed, then restored the fix.

## Acceptance
1. **Missing exclusion added, RE2-compatible (no lookahead syntax)** — PASS.
   `reservedSentinelSuffix` is a plain anchored regex (`^\.(?:invalid|test|
   example(?:\.(?:com|net|org))?|localhost)` + `hostTerminator`), applied by
   the caller in `ScanPrivacy` against `text[span[1]:]` (the text after each
   match), not embedded via lookahead in the match pattern itself. Compiles
   and runs under Go's RE2 engine (`go vet`/`go build` clean).

2. **Adjacency-scoped exclusion — sentinel must be the true end of host,
   not just present anywhere** — PASS, verified myself, not just via the
   implementer's claim. My own test
   (`host.corp.test.attacker.io` — sentinel present but followed by
   `.attacker.io`, not end-of-host) still produces exactly 1 warning:
   ```
   TestVerifyDisguisedSentinelStillFlags: PASS (1 warning)
   ```
   Mechanism: `reservedSentinelSuffix` requires `hostTerminator` immediately
   after the sentinel label (optional `:port` then a delimiter/whitespace/end
   — critically, *not* another `.`). `.attacker.io` starts with `.`, which is
   not a `hostTerminator` character, so the suffix regex fails to match and
   the exclusion correctly does not fire.

3. **Real internal hostname flags correctly, side by side with a sentinel,
   no cross-contamination** — PASS, verified with 2 independent constructions
   beyond the implementer's single committed test:
   - Real committed test (`TestScanPrivacyRealInternalHostnameStillFlaggedAlongsideSentinel`,
     same file: `foo.internal.test` + `jenkins-01.internal`) — exactly 1
     warning. PASS.
   - My own cross-*file* variant (sentinel in `a.md`, real host in `b.md`) —
     exactly 1 warning total, confirming the guard is per-match, not a
     file-level short-circuit that could hide a bug. PASS.
   - My own same-file double-sentinel-plus-real-host variant (`foo.internal.test`
     + `bar.internal.example` + `real-01.internal`) — exactly 1 warning
     (not 0, not 3), confirming counts don't cross-contaminate in either
     direction. PASS.

4. **Case-insensitivity matches Python's `re.IGNORECASE`** — PASS.
   `reservedSentinelSuffix` carries its own `(?i)` flag (independent of the
   hostname pattern's own `(?i)`), matching Python's single `re.IGNORECASE`
   applied to the combined pattern+lookahead. Verified with my own test using
   `FOO.INTERNAL.TEST` and `Bar.Internal.Example` (mixed/upper case both in
   the label and the sentinel) — 0 warnings, as expected. PASS.

5. **Regex semantics match Python's `_RESERVED_SENTINEL_LOOKAHEAD` exactly** —
   PASS. Side-by-side comparison of the compiled pattern text:
   - Python: `_HOST_TERMINATOR = r"(?=(?::\d+)?(?:[/?#\s\"'<>),]|$))"`,
     `_RESERVED_SENTINEL_LOOKAHEAD = r"(?!\.(?:invalid|test|example(?:\.(?:com|net|org))?|localhost)" + _HOST_TERMINATOR + ")"`.
   - Go: `hostTerminator = `(?:(?::\d+)?(?:[/?#\s"'<>),]|$))``,
     `reservedSentinelSuffix = (?i)^\.(?:invalid|test|example(?:\.(?:com|net|org))?|localhost)` + hostTerminator.
   - Sentinel-label alternation is character-for-character identical.
     Python's is a *negative* lookahead (embedded in the hostname pattern,
     blocking the match from happening); Go's is a *positive* post-match
     filter (letting the match happen, then discarding it) — same net
     exclusion semantics, different mechanical placement, exactly as the
     report describes and as required by RE2's lack of lookahead support.

## Coverage
`go test ./go/githooks/...` — full package suite, all pre-existing tests plus
the 2 new ones, all pass. No coverage percentage tool configured for this
module; adequacy assessed by adversarial case enumeration above (5 independent
constructions beyond the implementer's own 2 committed tests, all against
acceptance criteria and the Python reference, not against the Go
implementation's own behavior).

## Failures
None found. All acceptance criteria PASS on independent re-derivation.

## Tooling run fresh (not trusted from report)
```
cd go/githooks && gofmt -l .        -> clean, no output
cd go/githooks && go build ./...    -> clean, no output
cd go/githooks && go vet ./...      -> clean, no output
cd go/githooks && go test ./... -count=1 -v  -> PASS, ok (all tests incl. 2 new)
```
Repo-root `gofmt -l .` also flags `go/toolchain/golang_e2e_probe_test.go` —
confirmed pre-existing/unrelated via `git log -- go/toolchain/golang_e2e_probe_test.go`
(last touched in a commit unrelated to this diff); out of scope for this fix.

## Bisection (stash-equivalent) confirming the new tests exercise the fix
```
git checkout HEAD~1 -- go/githooks/privacy.go   # revert fix only, keep new tests at HEAD
go test ./... -run 'TestScanPrivacyReservedSentinelHostnameNotFlaggedAsInternal|TestScanPrivacyRealInternalHostnameStillFlaggedAlongsideSentinel' -v
```
Result: both new tests fail exactly as the report claims —
`TestScanPrivacyReservedSentinelHostnameNotFlaggedAsInternal` fails on all 3
subtests (each reports 1 unexpected warning), and
`TestScanPrivacyRealInternalHostnameStillFlaggedAlongsideSentinel` fails with
2 warnings instead of 1. `git checkout HEAD -- go/githooks/privacy.go`
restored the fix; full suite green again afterward. This proves the new
tests genuinely exercise the fix, not an unrelated code path.

## CI/e2e
Not applicable — no CI/e2e harness defined in `test_strategy` beyond the Go
unit-test suite already run above.

## Verdict
PASS. All 5 independently-checked acceptance points confirmed against the
Python reference and via adversarial tests I authored myself (not the
implementer's), plus a verified bisection proving the tests exercise the
actual fix. gofmt/build/vet/test all clean, no flakes observed (test suite is
deterministic, no goroutines/timing in the changed code path).
