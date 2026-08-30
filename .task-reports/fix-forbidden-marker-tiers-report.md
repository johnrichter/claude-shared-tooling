# fix-forbidden-marker-tiers report

## Task

Fix `go/githooks/privacy.go`'s `privacyTierConfigs` forbidden-marker regexes.
Design intent: each tier forbids a file whose own frontmatter `privacy:` tag
names a MORE sensitive tier than the repo's own declared tier.

- `TierPublic` must forbid `internal` (legacy value, still live), `confidential`, and `private`.
- `TierConfidential` must forbid `private` only, not `confidential` (its own posture).
- `TierPrivate` forbids nothing.

## What changed

`go/githooks/privacy.go`, `privacyTierConfigs`:

- `TierPublic.forbiddenMarkers`: `(?i)\bprivacy:\s*(internal|confidential)\b` → `(?i)\bprivacy:\s*(internal|confidential|private)\b`. Closes the gap where the single most-sensitive value (`private`) could sit untagged-as-forbidden in a public repo.
- `TierConfidential.forbiddenMarkers`: `(?i)\bprivacy:\s*confidential\b` → `(?i)\bprivacy:\s*private\b`. Stops flagging a confidential-tier repo's own legitimate `privacy:confidential` files; now flags only the tier above it.
- `TierPrivate.forbiddenMarkers`: unchanged (`nil`).
- No change to `internalID`, `requirePublicPair`, or `checksEmployeeEmail` for any tier.

`go/githooks/sanity_test.go`:

- `TestScanPrivacyTierIsParameterized` was written against the bug: it asserted a confidential-tier scan of a `privacy:confidential` file must fail. Rewrote it to use `privacy:private` content, which is the correct cross-tier "more sensitive than every other tier" case for this test's purpose (fails at public and confidential, passes only at private).
- Added `TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker`: a confidential-tier repo's own `privacy:confidential` frontmatter is not a violation.
- Added `TestScanPrivacyPublicTierForbidsPrivateMarker`: a public-tier scan of `privacy:private` content is flagged as `forbidden_marker` (the gap this fix closes).

No other existing test in `sanity_test.go` or `adversarial_test.go` asserted the buggy behavior — the remaining `TierPublic`/`TierConfidential`/`TierPrivate` references either test unrelated concerns (internal-identifier warnings, employee-email, owner-tag non-effect, exemption scoping) or use content/tiers unaffected by the regex change.

## Acceptance

1. Read full file + all references before changing — met.
2. `TierPublic` forbids `internal|confidential|private` — met, pattern matches spec exactly, compiles and verified via tests + live binary run.
3. `TierConfidential` forbids `private` only — met.
4. Existing tests exercising the wrong behavior fixed, not deleted — met (`TestScanPrivacyTierIsParameterized` rewritten to assert correct behavior; coverage for cross-tier failure preserved).
5. New regression tests for both real gaps + the already-correct confidential/private case — met (`TestScanPrivacyPublicTierForbidsPrivateMarker`, `TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker`, and the rewritten `TestScanPrivacyTierIsParameterized` covers confidential-tier-flags-private).
6. `TierPrivate` unchanged, forbids nothing — met, verified unchanged in diff and by existing `TestScanPrivacyPrivateTierAllowsInternalMarkers` / rewritten parameterized test.

## Sanity result

Run from `go/githooks` (each module here is its own Go module):

```
gofmt -l .        → no output (clean)
go build ./...    → clean
go vet ./...      → clean
go test ./... -count=1 -v → PASS, all tests including the 3 privacy-tier tests above
```

## Live end-to-end verification

Built a git-tools binary from a scratch checkout (`/tmp`, `.git` stripped so it
is not itself a repo checkout — the worktree-gate otherwise refuses writes into
a "primary checkout") with a `go.mod` `replace` directive pointing at this
fixed `go/githooks` module. git-tools' `internal/cli/scan.go` referenced a
stale `githooks.EmployeeEmailCheck` field name (`AllowedDomains` vs. current
`Domains` — an unrelated, already-landed rename on `main` that git-tools'
checked-out revision predates); patched that one field reference in the
scratch copy only, to unblock the build. This is not part of the deliverable
and touches no tracked file.

Copied the real production file, `workspace` repo's
`.dat/feature-request-reporting/design.md` (frontmatter `privacy:confidential`,
verified in place — repo declares `privacy_tier: confidential` in
`git-tools.yaml`), plus a constructed `privacy:private` fixture, into a
scratch scan target (again outside any tracked repo, to respect the
worktree-gate) and ran the built binary's `scan privacy`:

- `--privacy-tier confidential`: exactly one violation — the constructed
  `private-case.md` (`forbidden_marker`, `"privacy: private"`). The copied
  `design.md` (`privacy:confidential`) does **not** flag. This is the exact
  production case that surfaced the bug, now fixed.
- `--privacy-tier public`: both files flag (`design.md` for `privacy:confidential`,
  `private-case.md` for `privacy:private`), plus `not_public_pair` on both.
- `--privacy-tier private`: neither flags, exit 0.

## Assumptions & deviations

- Chose `privacy:private` (not a new value) for the rewritten
  `TestScanPrivacyTierIsParameterized`, since that is the one value that is
  "more sensitive than" both public and confidential tiers, matching the
  test's original "stricter tier fails, looser tiers pass" framing while now
  being consistent with the corrected confidential-tier posture (confidential
  no longer self-forbids `confidential`).
- Left the stray scratch build directory (`/tmp/git-tools-scratch`) undeleted:
  the worktree-gate refuses to remove it because its copied `.git` entry marks
  it as a "primary checkout." It lives entirely outside this repo/worktree and
  outside `workspace`; no tracked file or worktree is affected. Flagging for
  operator awareness rather than working around the safety gate.

## Hand-off notes

- Test-engineer: the three tier-boundary cases (public forbids private;
  confidential forbids private but allows its own confidential; private
  forbids nothing) are the core surface — consider also probing the `internal`
  legacy alternative still present only at `TierPublic`, and that
  `TierConfidential` truly has no `confidential`-matching branch left (a
  regex that accidentally kept `confidential` as an alternative would still
  pass the currently-added tests only if a case exercises exactly
  `privacy:confidential` at the confidential tier alone, which
  `TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker` does).
- Quality-reviewer: confirm no other module in this repo (or a downstream
  consumer like `git-tools`) hardcodes the old tier semantics or has its own
  copy of a similar tier-marker table that needs the same fix.
- Unrelated finding surfaced during live verification, worth a separate
  ticket: `git-tools`' checked-out `internal/cli/scan.go` still references
  `githooks.EmployeeEmailCheck.AllowedDomains`, which no longer exists on
  `go/githooks` `main` (renamed to `Domains` by the `invert-email-domain-check`
  work) — `git-tools` will fail to build against a `go/githooks` bump past
  that rename until its own `require` is repinned and its call site updated.
