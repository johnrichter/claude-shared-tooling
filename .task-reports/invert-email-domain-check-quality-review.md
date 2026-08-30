# Quality review — invert employee-email domain check to allow-list polarity

Scope: both halves of the change, plus the operator's post-report config-key
rename, reviewed against the current tree in each worktree — not against
either task report, both of which predate the rename.

- ai-shared-lib `chore/invert-email-domain-check` (`e47bf33` + this review's fix)
- git-tools `chore/wire-email-allowlist` (`33598ce`)

**Verdict: FIX-APPLIED.** One blocking defect found in the inversion itself
(not in the rename) and fixed in ai-shared-lib; everything else holds.

## 1. Rename: complete and consistent

The operator's rename `employee_email_allowed_domains` -> `allowed_email_domains`
(git-tools commit `33598ce`) touches exactly three files and is internally
consistent: `Config.AllowedEmailDomains` with `koanf:"allowed_email_domains"`,
the matching `defaultConfig()` seed key, `employeeEmailCheck`'s read of
`cfg.AllowedEmailDomains`, and the `employeeEmailConfig` test fixture string.
Doc comments were updated with the field.

Repo-wide grep for `employee_email_allowed_domains`, `EmployeeEmailAllowedDomains`,
and the pre-inversion `employee_email_domains` / `employee_email_allowlist` /
`EmployeeEmailDomains` / `EmployeeEmailAllowlist` returns **no hit in either
worktree outside `.task-reports/`**. The surviving `.task-reports/` mentions are
historical records of prior tasks plus these two tasks' own pre-rename reports —
correctly left as-is.

The three-layer koanf key contract (struct tag / `defaultConfig()` seed /
normalized flag name) holds for the new key, and the full git-tools suite —
including its config-resolution tests — passes, so the rename did not desync a
layer.

**Not confused with the githooks-side field.** Two distinct fields, both
correct:

| Repo | Field | `koanf` tag | Renamed? |
| --- | --- | --- | --- |
| ai-shared-lib `go/githooks` | `EmployeeEmailCheck.AllowedDomains` | n/a (library API) | no, and correctly not |
| git-tools `internal/cli` | `Config.AllowedEmailDomains` | `allowed_email_domains` | yes, by the operator |

`internal/cli/scan.go:275` bridges them: `githooks.EmployeeEmailCheck{AllowedDomains: cfg.AllowedEmailDomains}`.

## 2. Operator follow-up decisions: all three confirmed

1. **Domain-only design stands.** `EmployeeEmailCheck` carries `AllowedDomains`
   and nothing else. No address-level exemption survives anywhere: the old
   `Allowlist map[string]bool`, `allowlistIndex()`, git-tools'
   `EmployeeEmailAllowlist` key, and its `Allowlist`-building loop are all gone,
   with no vestigial config key left dead. `privacy.go:317` exempts by matched
   domain only.
2. **No starter allow-list added.** Neither `marketplace/git-tools.yaml` nor
   `knowledge-public-datadog/git-tools.yaml` carries an `allowed_email_domains`
   key, and neither repo was touched — both worktrees are confined to their own
   repos. (`knowledge-public-datadog` carries a pre-existing untracked
   `scripts/__pycache__/`, unrelated to this change.)
3. **Rename reviewed here**, per section 1.

## 3. Blocking defect (fixed): all-numeric TLD matched as an email domain

`go/githooks/privacy.go:159` — the inversion's new package-level
`employeeEmailPattern` required each domain label to be DNS-label-shaped but
placed **no constraint on the last label**, so any all-numeric right-hand side
matched. Confirmed against the real scanner, all at `TierPublic` with nil
config, each producing a spurious `internal_identifier` / "internal employee
email" warning:

```
install foo@1.0.0          -> 1 warning
pin git-tools@v0.5.0       -> 1 warning
npm i @scope/pkg@2.0.0     -> 1 warning
bump to tool@v10.20.30     -> 1 warning
probe cache@127.0.0.1      -> 1 warning
```

This is a **regression introduced by this change**, not a pre-existing gap: the
old deny-list pattern built its domain side as an alternation over
caller-configured literal domains, so a version specifier could never match it.
The prior test-verification did probe non-email `@`-strings but only
single-label ones (`retry@3`, `foo@bar`, `a@b`), which the `(?:\.label)+`
requirement already excluded — the two-or-more-numeric-label shape was the
untested case.

It also fails the task's own acceptance criterion that the regex "still requires
a real email shape ... not a bare `@` substring": no real address sits at an
all-numeric TLD.

Volume in this workspace's own tracked content, measured by grep of the
equivalent pattern:

| Repo | All-numeric-TLD pseudo-addresses in tracked content |
| --- | --- |
| ai-shared-lib | **193** |
| git-tools | ~30 (`@v0.5.0`, `@v0.4.0`, `@v0.1.0`, ...) |

In ai-shared-lib these are not confined to reports — they appear in `go/*.go`
source, `*.json` schemas, and adversarial test corpora. Left unfixed, the very
first public-tier scan after the future `go/githooks` repin would have buried the
real signal under ~200 warnings per repo, which is exactly how a warning check
becomes noise nobody reads.

### Fix applied

`go/githooks/privacy.go` — restructured the pattern so the TLD is explicit and
must start with a letter, with no new constraint on any earlier label:

```go
// before
`(?i)\b[\w.+-]+@[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+\b`
// after
`(?i)\b[\w.+-]+@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z](?:[a-z0-9-]*[a-z0-9])?\b`
```

Why leading-letter on the last label only, and nothing stricter:

- It is the minimum rule that excludes every all-numeric label, and no real TLD
  starts with a digit.
- A punycode IDN TLD still matches (`xn--...` starts with letters), so the fix
  does not narrow international-address detection.
- Constraining only the last label **widens** correct detection: the previous
  shape also failed to match a real address at a digit-leading host label
  (`jane@mail.3m.com`), because its repeated group applied one rule to every
  label. The restructured pattern matches it.

Doc comment on the pattern updated to state the rule and why. Two regression
tests added to `go/githooks/adversarial_test.go` in the existing suite's style:
`TestScanPrivacyEmployeeEmailNumericTLDIsNotAnAddress` (the three shapes above
produce zero warnings) and `TestScanPrivacyEmployeeEmailDigitLeadingHostLabelStillFlags`
(the widened detection is pinned, so a future tightening cannot silently
re-narrow it).

No git-tools-side change was needed: its fixtures (`other.example`,
`acme-corp.example`, `example.com`) all sit at letter-leading TLDs.

## 4. Independent re-verification of the core inversion

Run from a scratch probe file against the current `go/githooks` tree, deleted
before finishing (not in the committed diff). All pass **after** the fix, and
every real-address case passed identically before it — the fix changed only the
non-address shapes.

| Behavior | Result |
| --- | --- |
| `user@example.com` with nil config, three casings | 0 warnings |
| Real non-allowlisted address, nil config | 1 warning |
| `AllowedDomains` exempts, case-insensitive on both sides | 0 warnings |
| Configured domain exempt while a sibling domain still flags | 1 warning |
| `TierConfidential`, nil / empty / populated / blank config | 0 warnings, all four |
| Blank + whitespace-only entries are not a wildcard | 1 warning |
| Subdomain not exempted by its parent (`a@mail.example.com`) | 1 warning |
| `example.org`, `acme.example` not exempt (only `example.com` is) | 1 warning each |
| Single-label RHS (`retry@3`, `user@localhost`, `a@b`), bare `@example.com` | 0 warnings |
| Version specifiers / IPv4 host (section 3) | 0 warnings |
| Trailing-dot prose, `mailto:` link, SSH remote | 1 warning each |
| `require example.com/x/y v1.2.3` (module path) | 0 warnings |

The confidential-tier exclusion is structural, not incidental:
`privacyTierConfigs[TierConfidential].checksEmployeeEmail` is false and
`ScanPrivacy:275` gates both the pattern append and the allow-list build behind
it, so no config value can switch the check on at that tier.

## 5. False-positive-volume finding: still accurately described

Reproduced the prior test-verification's realistic-documentation fixture
verbatim: **5 warnings across 6 addresses**, the lone exemption being the
`example.com` citation. Unchanged by my fix (all six sit at letter-leading
TLDs). The reports' description is accurate and needs no correction.

Treated as the operator's known, accepted tradeoff — not reopened. Note the two
are separable and only one was ever accepted: the accepted cost is *real
third-party addresses in public docs* (support/security/vendor contacts); the
defect in section 3 was *non-addresses* matching, which no report disclosed and
which the operator therefore never accepted.

## 6. Worktree hygiene

| Worktree | State |
| --- | --- |
| ai-shared-lib | Only my two fix files modified (`go/githooks/privacy.go`, `go/githooks/adversarial_test.go`) plus this review. No stray probe files — the prior task's `verify_check_test.go` and my own scratch probes are gone. |
| git-tools | Clean. `go.mod`/`go.sum` restored, **no `replace` directive**, `go/githooks` still pinned `v0.6.1`. |

## 7. Verification results

ai-shared-lib `go/githooks`, after the fix:

```
gofmt -l .              -> clean
go build ./...          -> ok
go vet ./...            -> ok
go test ./... -count=1  -> ok
```

git-tools, built against the fixed githooks via a temporary local `replace`
(added, tested, then `git checkout -- go.mod go.sum`; never committed):

```
gofmt -l .                                                   -> clean
go build ./...                                               -> ok
go vet ./...                                                  -> ok
go test ./internal/cli/... -run "TestScanPrivacy_.*Email.*"  -> 3/3 PASS
go test ./... -count=1                                       -> ok, all 11 packages
```

The three email tests pass under the new key name, confirming the rename
resolves end-to-end through all four koanf layers to the githooks call.

With the `replace` removed, git-tools does not build — `unknown field
AllowedDomains` at `scan.go:275`, against the pinned `go/githooks v0.6.1`. This
is expected and correct for a commit deliberately scoped to config wiring; the
repin to a `go/githooks` release carrying the rename is separate, future work.

## 8. Test-suite assessment

Adequate after the two tests added here. Both suites are behavioral, assert
exact warning counts, and pin polarity from both sides (a configured domain
exempts, an unconfigured one still flags). No stale deny-list test was left
alongside a new one, and no test was weakened to pass.

The one gap was real and cost a blocking defect: both the authored suite and the
independent verification probed non-email `@`-strings only in the single-label
shape, never the multi-label numeric shape that ordinary source and docs are
full of. General lesson for an allow-list inversion — when a check flips from
"matches an enumerated list" to "matches a shape," the *shape's* precision
becomes load-bearing for the first time, and needs adversarial probing against
real repo content, not just hand-written strings. Measuring the candidate
pattern against the actual tracked corpus (one `git grep -oE`) surfaced this in
seconds and is worth doing on any future change to this pattern.

## 9. Residual risk

- **SSH clone URLs flag.** `git@github.com:org/repo.git` warns at the public
  tier — genuinely email-shaped, so no regex refinement separates it from a real
  address. README-heavy public repos will see this. Allow-listing `github.com`
  would work but is blunt. Accepted, not blocking; feeds the deferred
  starter-allow-list decision.
- **`name@major.minor.patch`-style placeholders still flag** (letter-leading
  final label, indistinguishable from a real address by shape). Only 2
  occurrences in ai-shared-lib; not worth further pattern complexity.
- **Accepted third-party-address volume** stands at 5/6 on a realistic docs
  sample (section 5). Bounded to a warning unless a caller opts into `--strict`.
- **The two public-tier repos are not yet scanned under this polarity**, since
  both go through the released git-tools binary pinned to the old `go/githooks`.
  The first real exposure is the future repin, which should re-measure both
  repos before `--strict` is enabled anywhere.

## 10. Plan feedback

1. **Sequence the repin behind a volume measurement.** Before repinning
   git-tools to a `go/githooks` release carrying this inversion, run a public-tier
   scan against `marketplace` and `knowledge-public-datadog` and read the actual
   warning list. That measurement is the input the deferred starter-allow-list
   decision needs, and it is cheap next to discovering the volume through a
   merge gate.
2. **`defaultAllowedEmailDomain` is narrower than the same file's hostname
   posture, deliberately or not.** `reservedSentinelSuffix` (`privacy.go:116`)
   exempts the whole RFC 6761 / RFC 2606 reserved set (`.invalid`, `.test`,
   `.localhost`, `.example`, `example.{com,net,org}`) for hostnames, while the
   email check hardcodes `example.com` alone — so `user@example.org` and
   `user@acme.example` flag even though both are reserved-for-documentation by
   the same RFCs, and the hostname check would let their host forms through.
   Left as-is: the task specified `example.com` as *the* hardcoded default, and
   widening it would flip git-tools'
   `TestScanPrivacy_EmployeeEmailCheckFlagsAnyDomainWithoutConfig`, which uses
   `person@other.example` as its flagging fixture. Worth an explicit operator
   decision rather than an implicit divergence — aligning the two would remove
   documentation-placeholder noise at zero cost to real-leak detection, and
   would need that one git-tools fixture changed to a non-reserved domain.
3. Both task reports' descriptions of the config key are now stale (they predate
   the operator's rename). Accurate as historical record of their own commits;
   this review is the current description.
