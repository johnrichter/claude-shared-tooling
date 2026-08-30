# Invert employee-email domain check to allow-list polarity

## Task

`go/githooks/privacy.go`'s employee-email internal-identifier check was a
deny-list (`EmployeeEmailCheck.Domains`): an address only flagged if its
domain matched a caller-configured list. Every repo ships an empty list
today, so the check never fires — a real gap. Redesign as an allow-list: any
email-shaped address flags unless its domain is `example.com` (hardcoded,
unconditional) or a caller-configured `AllowedDomains` entry. Wire the new
shape into git-tools' own config, in a second, separate piece of work.

## What changed (ai-shared-lib, `go/githooks`)

- `privacy.go`:
  - `employeeEmailPattern` is now a single package-level regex matching any
    email-address-shaped string (`\b[\w.+-]+@<hostname>\b`), not an
    alternation over caller domains — the check no longer needs to
    recompile per call.
  - `EmployeeEmailCheck` struct: replaced `Domains []string` +
    `Allowlist map[string]bool` (exact-address exemption) with
    `AllowedDomains []string` (domain-level exemption). Zero value still runs
    the check at full strength (only `example.com` exempt).
  - Added `const defaultAllowedEmailDomain = "example.com"` and
    `(EmployeeEmailCheck).allowedDomains()`, which always seeds the returned
    set with `defaultAllowedEmailDomain` before folding in
    `AllowedDomains` (case-insensitive, blank entries dropped).
  - Added `emailDomain(match string) string`, extracting the text after a
    matched address's last `@`.
  - `ScanPrivacy`: the employee-email pattern is now unconditionally appended
    to the public tier's internal-identifier posture (no more "off unless a
    domain is configured" branch); the per-match exemption check now looks up
    the matched address's domain in `allowedEmailDomains` instead of looking
    up the full matched address in an allowlist.
  - Updated every doc comment referencing the old deny-list/exact-address-
    allowlist shape (`internalIDStrict`, `PrivacyOptions.EmployeeEmail`,
    `ScanPrivacy`'s own doc) to describe the new allow-list polarity.
- `adversarial_test.go`: replaced the six employee-email tests pinned to the
  old deny-list semantics with tests for the new polarity — a real,
  non-allowlisted, non-`example.com` address flags by default;
  `user@example.com` never flags (any casing, no config needed); a
  caller-configured `AllowedDomains` entry exempts its domain, case-
  insensitively; an address at neither `example.com` nor any configured
  domain still flags; the confidential tier still never grows this check; a
  blank `AllowedDomains` entry is dropped rather than matching.

No other file in the module referenced the old field names.

## Acceptance

- Rename config field to unambiguous allow-list name (`AllowedDomains`),
  update every internal reference — met.
- Match logic: domain extracted from a detected email-shaped match; flagged
  unless it case-insensitively matches `example.com` or a caller allow-list
  entry — met.
- Severity preserved (still a warning, `internal_identifier` rule, same
  `employeeEmailLabel` detail text) — met, no severity change made.
- False-positive-volume judgment: confirmed no other test in this module's
  corpus asserts an exact warning count against public-tier content carrying
  a non-`example.com`, non-configured email address (checked every `@` in
  `*_test.go`; the only other email-carrying fixture runs at `TierPrivate`,
  which never runs this check) — met.
- Email-detection regex still requires a real email shape (local-part
  `@` label(s) `.` label, each DNS-label-shaped), not a bare `@` substring —
  met.
- git-tools config wiring — met, as a separate, second piece of work (see
  below); cannot fully build until git-tools repins its go/githooks
  dependency to a release carrying this change (see Hand-off notes).
- Tests: real non-allowlisted/non-example.com email flags; `example.com`
  never flags with empty/nil config; caller allow-list domain doesn't flag;
  allow-list is case-insensitive; no-domain-match flags; stale deny-list
  tests updated, not left alongside — all met (see adversarial_test.go).

## Sanity result (ai-shared-lib, `go/githooks`)

```
gofmt -l .          → (no output, clean)
go build ./...      → ok
go vet ./...        → ok
go test ./... -count=1  → ok (all packages pass)
```

## Assumptions & deviations

- Kept `EmployeeEmailCheck` as the struct name (only the field inside it
  changed) — it's still "the employee-email check's config," just with
  inverted-polarity contents; renaming the struct itself would touch call
  sites beyond what the task asked for.
- Dropped the old `Allowlist` (exact-address exemption) concept entirely
  rather than keeping it alongside the new domain-level allow-list: the task
  only asked for a polarity inversion plus one hardcoded default, and an
  address-level exemption on top of a domain-level allow-list would be scope
  creep with no acceptance criterion asking for it. A caller who wants a
  role address's domain exempt now allow-lists that domain outright.
- The `employeeEmailPattern` regex requires each DNS label be alphanumeric
  with internal hyphens (`[a-z0-9](?:[a-z0-9-]*[a-z0-9])?`), at least one
  `.`-joined pair of labels — matches a real email address's domain shape,
  not a bare `word@word` (which would over-flag things like `retry@3`-style
  non-email text if it appeared, though none exists in this module's own
  corpus).

## Hand-off notes

- **git-tools side**: see the matching worktree at
  `/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/wire-email-allowlist`
  and its own report reference below. Its `internal/cli/config.go` and
  `scan.go` are updated to the new `AllowedDomains` shape, but git-tools'
  `go.mod` still pins `go/githooks v0.6.1` (the pre-change API) — this repo's
  own module has not been tagged/released with this change yet. git-tools
  will not compile against the new field until: (1) this ai-shared-lib
  change ships a new `go/githooks` module version/tag, and (2) a follow-up
  repin bumps git-tools' `go.mod` require line to it — matching this repo's
  own established repin pattern (see recent history, e.g. "repin git-tools
  v0.9.0 to v0.10.0"). I verified the git-tools wiring compiles and its
  integration tests pass using a temporary local `replace` directive
  (removed before finalizing) pointed at this worktree; that replace is not
  part of the committed git-tools diff.
- Test-engineer: exercise a broader realistic-content corpus (real
  documentation with third-party contact emails) against `TierPublic` to
  confirm the false-positive-volume judgment holds outside this module's own
  fixture set — I only had this module's existing test corpus to check
  against.
- Quality-reviewer: confirm dropping the address-level `Allowlist` (as
  opposed to keeping it as a secondary exemption layer) is the intended
  scope; the task's own wording ("this is now, in effect, any real-looking
  email address not on the allow-list is suspicious") reads as endorsing
  domain-level allow-listing only, but flag if a role-address-level
  exemption should have survived alongside it.
