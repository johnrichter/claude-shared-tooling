# fix-forbidden-marker-tiers - test-engineer verification

## Deliverable type

Code (Go library, `go/githooks/privacy.go`) plus its test suite. Verification
form: read the isolated diff, exercise the compiled regexes directly, exercise
`ScanPrivacy` end-to-end via adversarial ad-hoc tests, run the full existing
suite, and reproduce the live git-tools binary scan independently.

Note on scope: `git diff main` in this worktree is noisy because `main` has
since advanced past this branch's fork point (`17185ad`) via an unrelated,
already-landed change (`invert-email-domain-check`, commits `6497aad`..`8236d2e`).
The isolated change for this task is `git diff 17185ad..9cc8419` (commit
`9cc8419`), touching only `go/githooks/privacy.go` (2 regex lines) and
`go/githooks/sanity_test.go` (test rewrite + 2 new tests). All findings below
are against that isolated diff.

## What I tested

- Direct regex probe (`go run`, throwaway file, deleted after use) against
  both `TierPublic` and `TierConfidential` compiled patterns for all listed
  values plus two negative-boundary probes (`publik`, `privateish`).
- Three ad-hoc `ScanPrivacy` tests, temporarily added to the `githooks`
  package and removed after running, covering: confidential-tier scan of
  `privacy:confidential` content (expect zero violations), confidential-tier
  scan of `privacy:private` content (expect a violation), and private-tier
  scan of content carrying `internal`, `confidential`, and `private`
  simultaneously (expect zero violations).
- Read-through of the rewritten `TestScanPrivacyTierIsParameterized` and the
  two new tests (`TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker`,
  `TestScanPrivacyPublicTierForbidsPrivateMarker`) in
  `go/githooks/sanity_test.go`.
- Full suite: `gofmt -l .`, `go build ./...`, `go vet ./...`,
  `go test ./... -count=1 -v`, run from `go/githooks`.
- Independent live e2e: built a git-tools binary from a scratch, git-stripped
  copy outside any tracked repo, `go.mod replace` pointed at this worktree's
  `go/githooks`, scanned a copy of `workspace`'s
  `.dat/feature-request-reporting/design.md` (`privacy:confidential`) plus a
  constructed `privacy:private` fixture, at all three tiers.
- Read `main`'s actual current `go/githooks/privacy.go` and `git-tools`'
  actual current `internal/cli/scan.go` to resolve the report's flagged field-
  name inconsistency (point 7).

## Acceptance

1. `TierPublic` forbids `internal`, `confidential`, `private` — **PASS**.
   Direct regex probe: `pub.MatchString` true for all three. False for
   `privacy: publik` and `privacy: privateish` (word boundary holds, no
   false-positive substring match).
2. `TierConfidential` forbids `private` only, never `confidential` — **PASS**.
   Direct regex probe: `conf.MatchString("privacy: confidential")` = false,
   `conf.MatchString("privacy: private")` = true. Ad-hoc `ScanPrivacy` test:
   confidential-tier scan of `privacy:confidential` content → 0 failures.
   Confidential-tier scan of `privacy:private` content → 1+ failures
   (`forbidden_marker`).
3. `TierPrivate` forbids nothing — **PASS**. Diff confirms the `TierPrivate`
   config entry is untouched (still `forbiddenMarkers: nil`). Ad-hoc
   `ScanPrivacy` test: private-tier scan of a tree containing
   `privacy:internal`, `privacy:confidential`, and `privacy:private` across
   three files → 0 failures.
4. Rewritten `TestScanPrivacyTierIsParameterized` asserts correct behavior —
   **PASS**. Traced: fixture is now `privacy: private` (not `confidential`).
   Asserts public-tier scan fails (correct: private is forbidden at public),
   confidential-tier scan fails (correct: private is forbidden at
   confidential, the tier immediately above), private-tier scan passes
   (correct: private forbids nothing). This is the one value simultaneously
   "more sensitive than" both public and confidential, so it is a valid
   cross-tier probe and does not merely restate the old buggy assertion.
5. New tests are behaviorally sound — **PASS**.
   `TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker`: confidential
   tier + `privacy:confidential` content → asserts 0 failures (correct: own
   posture, not forbidden). `TestScanPrivacyPublicTierForbidsPrivateMarker`:
   public tier + `privacy:private` content → asserts a `forbidden_marker`
   failure present (correct: closes the exact gap the fix targets).
6. `gofmt -l .` / `go build ./...` / `go vet ./...` / `go test ./... -count=1
   -v` — **PASS**, all clean, full suite green (see below).

## Coverage

Full suite run from `go/githooks` (module root): all tests pass, including
the three privacy-tier-boundary tests plus every pre-existing test in
`sanity_test.go` and `adversarial_test.go` unaffected by the change. No gaps
identified in tier-boundary coverage: public/confidential/private are each
exercised both for their own-tier marker (allowed) and the next-tier-up
marker (forbidden), and `TierPrivate`'s "forbids nothing" posture is now
independently reproduced against all three marker values at once (ad-hoc
test, not currently a committed regression test — see Hand-off note below).

```
$ gofmt -l .                              # no output
$ go build ./...                          # clean
$ go vet ./...                            # clean
$ go test ./... -count=1 -v               # PASS (48 tests, 0 failures)
ok  	github.com/johnrichter/claude-shared-tooling/go/githooks	0.047s
```

## Failures

None. No flakes observed. Suite run once at full speed, 0.047s. Regex and
ad-hoc probes are deterministic, with no timing/concurrency surface in this
change.

## Point 7 — resolved, definitively

**The report's claim is backwards.** Checked `main`'s actual current
`go/githooks/privacy.go` directly (`git show main:go/githooks/privacy.go |
grep -n "AllowedDomains\|Domains "`):

```
187:	AllowedDomains []string
197:	for _, d := range c.AllowedDomains {
```

`main`'s current `EmployeeEmailCheck` field is **`AllowedDomains`**, not
`Domains`. `git-tools`' actual checked-out `internal/cli/scan.go:275`:

```go
return githooks.EmployeeEmailCheck{AllowedDomains: cfg.AllowedEmailDomains}
```

This matches `main` exactly — **`git-tools` is not stale against `main`**.

What actually happened, traced through history: `Domains`/`Allowlist` naming
was introduced early (present already at commit `224bde8`, an ancestor of
both this branch and `main`). This worktree's branch forked from `17185ad`,
which still carries that `Domains`/`Allowlist` naming — confirmed by
`grep -n "AllowedDomains\|Domains \[\]string\|Allowlist" go/githooks/privacy.go`
in this worktree, showing `Domains []string` / `Allowlist map[string]bool`.
Separately, on `main`'s own subsequent line of history, commit `6497aad`
("invert employee-email check to allow-list polarity") explicitly *replaced*
`Domains`+`Allowlist` *with* `AllowedDomains` (its own commit message: "...is
replaced by AllowedDomains") — the opposite rename direction from what the
report describes. `main`'s tip (`8236d2e`) still carries that `AllowedDomains`
naming.

Net: it is **this worktree's branch that is stale relative to `main`**, not
`git-tools`. Reproduced directly: building `git-tools` against this worktree's
`go/githooks` via `go.mod replace` fails with
`unknown field AllowedDomains in struct literal of type
githooks.EmployeeEmailCheck` — i.e. `git-tools`' `AllowedDomains` reference is
correct for `main` and only breaks against this older-based branch's
`Domains`/`Allowlist` naming. This is an artifact of this branch forking
before `6497aad` landed on `main`, not a defect in `git-tools` or in this
fix. Flagging for the orchestrator: whoever merges this branch will need to
carry `main`'s `AllowedDomains` rename forward (rebase/merge conflict in the
`EmployeeEmailCheck` region), independent of and unrelated to the
forbidden-marker regex fix itself, which does not touch that struct at all.

## CI / e2e

Independent live e2e reproduction, built from scratch (git-tools binary,
`go.mod replace` → this worktree's `go/githooks`, one-line local patch to
`internal/cli/scan.go`'s scratch copy only — `AllowedDomains:` →
`Domains:` — to unblock the build against this branch's older field naming.
No tracked file touched):

- `--privacy-tier confidential` scan of {`design.md` (real
  `workspace/.dat/feature-request-reporting/design.md`, `privacy:confidential`),
  `private-case.md` (constructed, `privacy:private`)}: exactly one violation
  — `private-case.md`, `forbidden_marker`, `"privacy: private"`. `design.md`
  does not flag. Exit code 30 (precondition_unmet), matches expected schema.
- `--privacy-tier public`: both files flag (`design.md`:
  `forbidden_marker`+`not_public_pair`, `private-case.md`:
  `forbidden_marker`+`not_public_pair`), 4 total violations. Exit code 30.
- `--privacy-tier private`: zero violations, exit code 0, status "success".

All three match the report's claimed output exactly.

## Verdict

**PASS.**

Both regex fixes are correct and precisely scoped (word-boundary-safe, no
over- or under-matching). `TierPrivate` is untouched. The rewritten and new
tests assert genuinely correct behavior, not the prior bug. Full suite is
green with no flakes. The report's field-naming claim in its "Unrelated
finding" section (point 7) is factually backwards and should be corrected
before hand-off: `main`'s current field is `AllowedDomains` (not `Domains`),
`git-tools` is not stale, and it is this branch's fork point that predates
`main`'s `6497aad` rename — a forward-merge/rebase concern for whoever lands
this branch, unrelated to the forbidden-marker fix's correctness.
