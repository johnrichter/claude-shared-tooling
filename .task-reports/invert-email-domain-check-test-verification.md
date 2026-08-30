# Test verification — invert employee-email domain check to allow-list

Verified independently against `go/githooks/privacy.go` and
`adversarial_test.go` at commit `9ba9184` (branch
`chore/invert-email-domain-check`), not against the report's own claims.
Ad-hoc probe tests were run from a scratch file (`verify_check_test.go`),
then deleted before finishing — not part of the committed diff, per the
dispatch's instruction to commit only this report.

## 1. Diff read

Read `privacy.go` in full and the `adversarial_test.go` diff
(`17185ad..HEAD`). Confirms the report's description: `employeeEmailPattern`
is now a single package-level regex; `EmployeeEmailCheck.AllowedDomains`
replaces `Domains`/`Allowlist`; `defaultAllowedEmailDomain = "example.com"`
is seeded unconditionally in `allowedDomains()`; `emailDomain()` extracts the
domain from a match; `ScanPrivacy` appends the pattern unconditionally at
public tier and looks up the matched domain, not the full address. PASS.

## 2. Adversarial non-email `@` strings

Probed `retry@3`, `foo@bar`, `attempt@1 of 3`, `user@localhost`,
`@example.com`, `a@b` — none produced a warning. The domain side of the
pattern (`[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+`)
requires at least one `.`-joined pair of DNS-shaped labels, so a bare
single-label right-hand side (`3`, `bar`, `localhost`, `b`) never matches.
`@example.com` also didn't match — the local-part class `[\w.+-]+` requires
one or more characters before `@`, and there are none at start-of-string, so
no email-shaped match exists there either. PASS.

## 3. `user@example.com`, any case, nil config

`user@example.com`, `USER@EXAMPLE.COM`, `User@Example.Com` — zero warnings
in all three, with a bare `PrivacyOptions{SkipRules: DefaultSkipRules}` (no
`EmployeeEmail` set, i.e. the zero value). `allowedDomains()` seeds
`defaultAllowedEmailDomain` unconditionally and both sides of the lookup are
lowercased, so this holds independent of caller config. PASS.

## 4. Real non-allowlisted address flags by default

`jane.doe@acmecorp.io` in a public-tier scan with no `EmployeeEmail` config
produced exactly one warning (`internal_identifier` / "internal employee
email"). PASS.

## 5. `AllowedDomains` case-insensitive exemption; blank entry not a wildcard

- `vendor@Vendor.COM` exempted via `AllowedDomains: []string{"vendor.com", ""}`,
  while `other@untouched.io` in the same file still flagged — exactly one
  warning, matching case-insensitive domain exemption plus one blank entry
  correctly dropped rather than corrupting the set.
- `AllowedDomains: []string{"", "   "}` (both entries blank/whitespace) against
  `someone@somewhere.io` still produced exactly one warning — confirms a
  blank entry is dropped, not indexed as `""` matching every unrecognized
  domain (which `emailDomain()` never returns anyway, but this closes the
  possibility a stray blank key in the map could accidentally satisfy some
  other lookup path).
PASS.

## 6. Confidential tier never runs the check

`jane.doe@acmecorp.io` and `root@other.com`, scanned at `TierConfidential`
with `EmployeeEmail.AllowedDomains` explicitly configured — zero warnings.
`privacyTierConfigs[TierConfidential].checksEmployeeEmail` is unset (false),
and `ScanPrivacy` gates the whole append-and-allowlist block behind that
flag, so configuring `AllowedDomains` has no way to turn the check on at
this tier. PASS.

## 7. Realistic documentation corpus — false-positive-volume assessment

Constructed a Markdown fixture combining: a support contact
(`support@datadoghq.com`), a vendor contact
(`procurement@vendor-example-corp.net`), an RFC-5322/RFC-2606-style citation
including a genuine `jsmith@example.com` example address, a security-policy
contact (`security@apache.org`), and two role addresses at the doc's own
company domain (`sales@our-company.com`, `abuse@our-company.com`). Scanned
at `TierPublic` with no config.

Result: 5 warnings — every address except `jsmith@example.com` (correctly
exempt as the RFC-2606 domain). This matches the polarity by design: every
real-looking email address not on the allow-list is treated as suspicious,
full stop, regardless of whether it is plausibly a legitimate third-party
contact.

**Assessment (design tradeoff, not a defect):** this inversion is
functionally a full email-address-shaped-string detector for the public
tier, with a single exemption (`example.com`) plus whatever an operator
enumerates. Any real support/vendor/security-contact email address embedded
in public documentation — the kind that legitimately belongs in a README,
SUPPORT.md, SECURITY.md, or third-party citation — now generates a warning
by default. For the two repos this check is slated to run against
(`marketplace`, `knowledge-public-datadog`), the volume observed here (5/6
addresses in a six-line realistic sample) suggests this will fire routinely
on ordinary public documentation content — not just on genuine internal
leaks. Because the finding is a *warning*, not a *failure* (per the report's
own note, severity unchanged), it is non-blocking by default and only
becomes a build-blocker if a caller opts into `--strict`. So the practical
risk is bounded to: (a) noisy hook output that reviewers learn to ignore
(alert fatigue, the classic failure mode of an over-broad warning check), or
(b) an operator turning on `--strict` for these repos without first
building out a substantial `AllowedDomains` list covering every legitimate
third-party/role address the docs reference (support, security, vendor,
citation addresses), at which point every new legitimate address requires a
config edit before it can ship. This is a real, non-trivial adoption cost —
worth the quality-reviewer's and operator's attention before enabling
`--strict` on either public-tier repo, but it is exactly the tradeoff the
task's own wording asked for ("any real-looking email address not on the
allow-list is suspicious"), and the mechanism to soften it
(`AllowedDomains`) exists and works correctly (see point 5). Not a defect in
the implementation; a genuine operational cost of the requested design.

## Sanity commands (re-run fresh, `go/githooks` module)

```
$ gofmt -l .
(no output)
$ go build ./...
(ok)
$ go vet ./...
(ok)
$ go test ./... -count=1 -v
ok  	github.com/johnrichter/claude-shared-tooling/go/githooks	0.048s
(all pre-existing tests pass, including the 7 rewritten employee-email tests)
```

Note: `gofmt -l .` run from the repo root (not this module) additionally
flags `go/toolchain/golang_e2e_probe_test.go` — pre-existing, untouched by
this change (last modified in an unrelated Go-adapter commit,
`0471fd6`), and outside the `go/githooks` module this task touched. Not a
finding against this diff.

## Findings

No defects found. All 7 verification points pass against the diff as
written, independent of the report's own claims. Point 7's answer is a
design-tradeoff observation, not a pass/fail — flagged above for the
quality-reviewer and operator, per the report's own hand-off request.

## Verdict

**PASS** — acceptance criteria hold under adversarial probing beyond the
existing test corpus; build/vet/gofmt/test all clean in `go/githooks`; the
false-positive-volume tradeoff is real but is the requested design, not an
implementation bug.
