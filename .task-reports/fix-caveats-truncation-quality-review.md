# Quality review: fix-caveats-truncation

**Verdict: PASS** — the fix is correct, exactly as described, correctly
scoped, matches the file's existing conventions, and is verified end-to-end
against a real crash reproduction. No changes needed. Committed as-is.

## Scope reviewed
Uncommitted working tree on `chore/fix-caveats-truncation` in the worktree at
`/home/bits/Development/workspaces/psa-platform/ai-shared-lib/.claude/worktrees/fix-caveats-truncation`:
`go/githooks/envelope.go`, `go/githooks/adversarial_test.go`. This is a
combined test-engineer + quality-reviewer pass per the orchestrator's
scale-down for a small, pattern-mirroring fix.

## 1. Diff matches the described fix, scoped correctly
`git diff --stat main` touches exactly the two expected files, nothing else:
```
 go/githooks/adversarial_test.go | 34 ++++++++++++++++++++++++++++++++++
 go/githooks/envelope.go         | 22 ++++++++++++++++++++--
```

In `envelope.go`'s warnings-only branch (`BuildHookResult`, non-strict,
`len(failing) == 0 && len(outcome.PrivacyWarnings) > 0`), the fix:
- Introduces a local `warnings := outcome.PrivacyWarnings` and truncates it
  to `maxDiagnostics-1` (49) when it exceeds `maxDiagnostics` (50), reserving
  one slot for an overflow-summary caveat — correctly different from the
  failing/errors branch just below, which truncates `failing` to the full
  `maxDiagnostics` (50) because its overflow caveat lands in a **separate**
  `Errors` array, not sharing space with the findings.
- Overflow count math: `overflow := len(warnings) - (maxDiagnostics - 1)`.
  For 220 warnings: `220 - 49 = 171`. Confirmed this exact number appears in
  the live E2E run below (`"171 additional finding(s) omitted..."`).
- Reuses the exact same caveat code (`caveats.githooks.findings_truncated`),
  message template (`"%d additional finding(s) omitted from caveats (record
  cap)"` vs. errors branch's `"... from errors ..."`), and triage advice
  (`clikit.Manual("re-run the underlying scanner directly for the full
  list")`) as the pre-existing errors-branch pattern — same code style,
  same error-handling shape (`if err != nil { return nil, err }` per
  `clikit.NewCaveat` call), same `var caveats []clikit.Diagnostic` idiom
  (not pre-sized, matching the errors branch rather than the old
  `make(..., 0, len(...))` this branch used before the fix — correct, since
  the final length is no longer simply `len(outcome.PrivacyWarnings)`).
- The added doc comment above the new logic is accurate and explains *why*
  this branch's math differs from the errors branch (shared array vs. two
  separate arrays) — the one subtlety a future editor most needs.

Confirmed against `clikit`'s actual enforcement
(`go/clikit/result.go:162`): `return fmt.Errorf("clikit: caveats has %d
members, max 50", len(r.Caveats))` — the exact message from the bug report,
gating on `len(r.Caveats)`, i.e. the single shared array this fix caps.

## 2. New test is load-bearing, not tautological
`TestBuildHookResultWarningsOnlyCapsAtFiftyDiagnosticsWithOverflowCaveat`
(`adversarial_test.go`) builds 220 synthetic `Finding`s, calls
`BuildHookResult` directly with them as `PrivacyWarnings` (no `Strict`, no
other findings), and asserts:
- `ExitCode == 10` (caveats status)
- `len(Errors) == 0` (still non-blocking)
- `len(Caveats) == 50` exactly (not "at least 50" or unchecked)
- `Caveats[0].Code == "caveats.githooks.findings_truncated"` (overflow
  caveat present and, per the fix's prepend-first order, first)
- `assertCanonicalJSON` round-trips the whole record

This is the same shape as the pre-existing sibling test
(`TestBuildHookResultCapsAtFiftyDiagnosticsWithOverflowCaveat`, which covers
the errors branch with 60 secrets) and genuinely exercises the reported bug:
reverting only the fix (restoring the old unbounded
`caveats := make(..., 0, len(outcome.PrivacyWarnings)); for _, f :=
range outcome.PrivacyWarnings { ... }` loop) while keeping the test causes
`BuildHookResult` to return the `"clikit: caveats has 220 members, max 50"`
error instead of a result, which fails the test at the `err != nil`
check — verified by temporarily reverting `envelope.go`'s fix in a scratch
copy and re-running just this test, then restoring. Not tautological: it
does not assert against an input-dependent value that trivially always
holds regardless of the fix.

## 3. Full suite, fresh
Run in `go/githooks`:
```
go build ./...              -> ok
go vet ./...                -> ok
gofmt -l .                  -> no output (clean)
go test ./... -count=1      -> ok  github.com/johnrichter/claude-shared-tooling/go/githooks  0.061s
```
No skips, no failures. The new test also passes individually with `-v`.

## 4. End-to-end re-verification against a real `git-tools` binary
Built `git-tools` with a temporary `replace` directive pointing
`go/githooks` at this worktree's fixed copy:
1. Copied `git-tools/go.mod` and `go.sum` to `/tmp` first.
2. Appended `replace github.com/johnrichter/claude-shared-tooling/go/githooks
   => <this worktree>/go/githooks` to `git-tools/go.mod`, ran `go mod tidy`,
   built `go build -o /tmp/git-tools-fixed ./cmd/git-tools` — succeeded.
3. Ran `/tmp/git-tools-fixed scan privacy --repo
   /home/bits/Development/workspaces/psa-platform/ai-shared-lib`.

Result: exit code **10**, JSON body:
```
"status": "caveats", "exit_code": 10
data.privacy_warnings_found: 220
caveats: 50 total -> 1x "caveats.githooks.findings_truncated"
                      ("171 additional finding(s) omitted from caveats (record cap)")
                      + 49x "caveats.privacy.internal_identifier"
```
No crash, no internal-error envelope — the previously-reported
`"clikit: caveats has 220 members, max 50"` failure is gone. The overflow
count (171 = 220 − 49) matches the fix's arithmetic exactly, confirmed
independently of the unit test.

Cleanup: restored `git-tools/go.mod` and `go.sum` from the `/tmp` copies.
`diff` against the backups is empty and `git status --porcelain` /
`git diff --stat` in `git-tools` are both clean — the primary `git-tools`
checkout was left untouched.

## 5. The 220 warnings themselves — pre-existing, non-blocking, unrelated
`data.privacy_warnings_found` is exactly 220, and every flagged path in the
live scan output is under `.task-reports/`. Spot-checked three:
- `.task-reports/fix-hostname-sentinel-lookahead-report.md` — discusses the
  `internalHostnameLabel` regex fix using RFC 6761 reserved-sentinel test
  hostnames (`foo.internal.test`, `bar.internal.example`,
  `baz.internal.localhost`) as worked examples — documentation about a
  hostname-detection fix, not a real internal host.
- `.task-reports/invert-email-domain-check-quality-review.md` — discusses
  the email-domain regex using example/test addresses
  (`jane@mail.3m.com`, `user@example.com`, `git@github.com:org/repo.git`)
  as test-matrix rows — documentation about an email-regex fix, not a real
  address.
- `.task-reports/fix-idn-tld-truncation-report.md` / `-test-verification.md`
  — same pattern, IDN/punycode TLD examples in a regex-fix writeup.

All self-referential documentation about this repo's own past regex fixes,
using placeholder/example hostnames and emails to describe test cases —
consistent with the task's characterization. Not real secrets or leakage;
correctly out of scope to fix or redact here, and left untouched.

## 6. Scope discipline
`git diff --stat` against this worktree's own `main` shows only the two
files listed in §1 — nothing else touched, no stray formatting changes, no
untracked files (`git status --porcelain` before committing showed only
those two modifications).

## Findings
None. No fixes were needed — the implementation and test were correct on
first read and every independent check (arithmetic re-derivation, style
comparison, revert-and-rerun on the test, live binary run against the real
`ai-shared-lib` tree, git-tools cleanup verification) confirmed it.

## Residual risk
None specific to this fix. Noting for context only: the underlying 220
warnings are themselves a latent finding-volume issue in `ai-shared-lib`'s
own `.task-reports/` corpus (worth trimming/archiving eventually so routine
scans don't sit this close to the cap), but that is explicitly out of scope
here and does not affect this fix's correctness.

## Disposition
Committed on `chore/fix-caveats-truncation` in this worktree. Not merged,
pushed, or tagged, per instructions — that is the orchestrator's next step
via the provisioned `git-tools` CLI.
