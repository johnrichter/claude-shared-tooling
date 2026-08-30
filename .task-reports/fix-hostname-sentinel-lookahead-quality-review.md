# Quality review: fix-hostname-sentinel-lookahead

> **Literal-breaking convention.** Hostname examples below write the leading
> label as `<h>` and any URL scheme as `<scheme>://`, so this report does not
> itself trip the privacy scanner it reviews. Read `<h>` as an ordinary host
> label. Same precedent as commit 7b5c923 ("Break the literal Slack
> example-token string in these reports"). Finding D explains why this convention
> is necessary rather than merely tidy — it is itself a review finding.

## Verdict
**FIX-APPLIED.** The production change in `go/githooks/privacy.go` is correct and
semantically exact against the Python reference — I changed no production code
and `privacy.go` is byte-identical to what the implementer committed. I fixed a
real test-adequacy gap in `go/githooks/adversarial_test.go`: the committed suite
left the security-relevant half of the fix (disguise rejection) and two of the
filter's four reserved TLDs entirely unpinned, proven by mutation testing.

Separately I found two pre-existing defects in the same module, neither
introduced by this diff and neither fixed here: an uncapped caveat array that
hard-fails the sanctioned merge/push/tag path (finding B, section 5), and a
terminator character class that omits the delimiters markdown uses, so this
release's own exclusion does not fire for backticked hostnames (finding D). Both
are plan-feedback items with concrete follow-ups; finding D bounds what this
release actually delivers, so read it before announcing the fix.

## 1. Correctness of the fix

### RE2 compatibility — CONFIRMED
`grep -rn '(?=|(?!|(?<|\1' go/githooks/` returns nothing: no lookahead,
lookbehind, or backreference syntax anywhere in the module. `reservedSentinelSuffix`
is a plain anchored pattern applied by the caller against `text[span[1]:]`.
`regexp.MustCompile` would panic at package init on non-RE2 syntax, and all 58
tests initialize it. `go build` / `go vet` clean.

### Semantic equivalence to Python's negative lookahead — PROVED
The risk in replacing a negative lookahead with a post-match filter is not the
regex text, it is scanner-loop semantics. Python's blocked match consumes
nothing, so its engine resumes at start+1 and can find an *overlapping* match
inside the blocked span. Go's `FindAllStringIndex` returns non-overlapping
matches and the filter then discards one, so an overlapping alternative inside
that span is never seen. Equivalence holds only if no such match can exist.

*By construction:* inside a blocked span `X.LABEL`, the only interior `\b` is at
the start of `LABEL`. A match from there needs `LABEL.(corp|internal|intranet|lan)`,
but the run after `LABEL` is `.sentinel` with sentinel ∈ {invalid, test, example,
localhost} — disjoint from {corp, internal, intranet, lan}. `X` cannot span a `.`,
so no alternative `X` split exists either. No interior match is reachable.

*By differential execution:* I ran a 40-string adversarial corpus through both
implementations and compared **match counts per string**, not booleans.

- Python side: the live `_RESERVED_SENTINEL_LOOKAHEAD` + hostname pattern lifted
  verbatim from `marketplace/scripts/check_privacy.py:145-185`.
- Go side: real `ScanPrivacy(dir, TierPublic, ...)` calls over one-line temp
  files, counting only warnings whose `Detail` carries `internalHostnameLabel`.

`diff` of the two count columns: **identical on all 40 strings.** Properties the
corpus exercises, with the count both implementations produce:

| property | count | example |
|---|---|---|
| each of the four reserved TLDs excluded | 0 | `<h>.internal.test`, `<h>.internal.invalid` |
| RFC 2606 second-level domains excluded | 0 | `<h>.internal.example.com` / `.net` / `.org` |
| near-miss second-level domain not excluded | 1 | `<h>.internal.example.co` |
| reserved label as a mere prefix not excluded | 1 | `<h>.internal.testing` |
| disguised sentinel, further host label | 1 | `<h>.corp.test.attacker.io` |
| disguised sentinel behind a bogus port | 1 | `<h>.corp.test:8080.attacker.io` |
| non-numeric `:` is not a port | 1 | `<h>.corp.test:notaport` |
| real port at true end of host | 0 | `<h>.corp.test:8080` |
| **sentinel sandwiched between two internal labels** | **2** | multi-match overlap case |
| sentinel behind a non-sentinel label | 1 | — |
| alternation-order backtracking | 1 | — |
| case-insensitivity, both halves | 0 / 1 | upper-case sentinel and upper-case disguise |
| every `hostTerminator` delimiter: `/ ? # " ' ( ) ,` whitespace, EOF | 0 | sentinel in quotes, parens, path, query, fragment |
| sentinel-then-sentinel, neither at host end | 1 | `<h>.internal.localhost.example.com` |
| bare dot / bare host / `\b` boundary on the label | 1 / 1 / 0 | `<h>.internal.`, `<h>.internal`, `foo.internalize.test` |

The sandwiched-sentinel row returning 2 in both is the load-bearing result: it is
precisely the case where a naive post-match filter could have diverged from a
lookahead, and it does not.

### `$` semantics — checked, not assumed
Go's `$` without `(?m)` is `\z`; Python's `$` also matches before a single
trailing newline. The difference is unreachable because `hostTerminator`'s
character class contains `\s`, covering `\n` and `\r`. Verified directly: content
with no trailing newline, with `\n`, with `\r\n`, and with the host mid-file all
yield 0 for a sentinel hostname.

### Scope — genuinely confined to the hostname pattern
`grep -rn 'internalID|hostTerminator|reservedSentinel|internalHostnameLabel'`
over the module: `internalID` has exactly one consumer, the loop at
`privacy.go:314-325`. No second scanning path, no per-line variant, and no other
module in the repo duplicates the hostname pattern (`grep -rn intranet` hits only
`privacy.go`). No wiring gap — one producer, one consumer, both keyed off the same
`internalHostnameLabel` constant rather than a duplicated literal, which is the
right call and is why `sanity_test.go`'s two label assertions still pass unchanged.

## 2. `privateNetworkURL` claim — verified true, with a caveat both reports missed

Both reports claim `privateNetworkURL` "already handles the sentinel case
correctly" and needed no change. **True for this task**, but the mechanism is
broader than either report states, and the breadth is a live divergence.

I ran Python's private-network pattern (`check_privacy.py:188-190`, which carries
the same `_RESERVED_SENTINEL_LOOKAHEAD`) against Go's:

| input | Python | Go | agree? |
|---|---|---|---|
| `<scheme>://127.0.0.1.invalid` | 0 | 0 | yes — sentinel correctly excluded |
| `<scheme>://192.168.1.1.example.com` | 0 | 0 | yes |
| `<scheme>://127.0.0.1` | 1 | 1 | yes |
| `<scheme>://127.0.0.1:8080/x` | 1 | 1 | yes |
| `<scheme>://127.0.0.1.invalid.attacker.io` | **1** | **0** | **no** |
| `<scheme>://192.168.1.1.attacker.io` | **1** | **0** | **no** |

Go is right on every genuine-sentinel row, so no change was needed here and none
should have been made. But it gets there by different means, and the difference
matters. Python's tail is `_RESERVED_SENTINEL_LOOKAHEAD + (?::\d+)?\b` — the
terminator lives *inside* the sentinel lookahead only, so nothing is required
after the IP itself. Go replaced that whole tail with the *consuming*
`hostTerminator`, which requires a terminator character after the IP. That is a
strictly narrower match, and it produces Go-only false negatives. See finding D:
the practical trigger is not an exotic `.attacker.io` disguise but ordinary
markdown formatting.

`privacy.go:94-99` documents the `.`-is-not-a-terminator choice deliberately,
calling `192.168.1.1.example.com` "a DNS name and not a private address". That
rationale is sound for the `.`-suffixed case but does not extend to the
`.attacker.io` shape above, nor to the delimiter gap in finding D.

## 3. Findings

### Blocking
None.

### Major
**A. `go/githooks/adversarial_test.go:356-395` (as committed) — the suite did not
pin the disguise rejection or two of the four reserved TLDs.** Mutation-tested,
not asserted:

| single-token mutation of `reservedSentinelSuffix` | committed suite | after my fix |
|---|---|---|
| drop `hostTerminator` | **GREEN** | FAIL, 4 subtests |
| drop `invalid` from the alternation | **GREEN** | FAIL, 1 subtest |
| drop the `example(?:\.(?:com\|net\|org))?` group | **GREEN** | FAIL, 3 subtests |

Mutation 1 is the security-relevant one: dropping `hostTerminator` turns
`<h>.corp.test.attacker.io` into an unflagged internal hostname in a public repo,
and every committed test stayed green. The implementer verified that exact case
**by hand and explicitly declined to commit it**, on the stated grounds that it
"duplicates existing coverage". It does not: the pre-existing
`TestScanPrivacyReservedSentinelHostNotFlaggedAsPrivateNetwork` covers the
*private-network-URL* regex, a different pattern reached by a different code path
with no `reservedSentinelSuffix` involvement at all. The test-engineer ran the
same case in a throwaway test and deleted it. A hand-run check of the one
property most likely to be lost to a future "simplification" is not coverage.

**B. `go/githooks/envelope.go:53-63` — pre-existing, unrelated to this fix: the
warnings-only path builds one caveat per warning with no `maxDiagnostics` cap.**
See section 5. Not fixed here (out of scope); top plan-feedback item.

**D. `go/githooks/privacy.go:100` — `hostTerminator`'s character class omits the
delimiters markdown actually uses, so the exclusion this task adds does not fire
for the documentation hostnames it exists to serve.** Pre-existing; not fixed
here, because fixing it would break this task's own acceptance criterion of
matching the Python reference, and would change `privateNetworkURL` — explicitly
out of scope. Found by running the finished scanner over my own draft of this
report, which is why the report carries a literal-breaking header note.

The class is `[/?#\s"'<>),]`. It has quotes, angle brackets, `)` and `,`, but not
`` ` ``, `*`, `[`, `]`, `{`, `}`, `;`, `|`, or a bare `:`. Measured, one line per
file, sentinel hostname `<h>.internal.test`:

| rendering | warnings | correct? |
|---|---|---|
| bare, space-delimited | 0 | yes |
| markdown table cell, space-padded | 0 | yes |
| inline code, backticks | **1** | **no — false positive** |
| bold, `**...**` | **1** | **no** |
| square brackets, `[...]` | **1** | **no** |
| braces, `{...}` | **1** | **no** |
| trailing `;` | **1** | **no** |
| pipe-delimited table cell, unpadded | **1** | **no** |
| trailing `:` | **1** | **no** |

Backticks are how a hostname is written in a markdown document. The fix therefore
works for prose mentions and fails for the code-span form that dominates real
docs. This half of the gap is **shared with Python** (verified: Python's pattern
flags the backticked sentinel too), so it is a common defect in the shared
terminator class, not a port divergence — and not something to fix on one side
alone.

The `privateNetworkURL` half **is** a Go-only divergence, and it runs the other
way — false negative:

| input | Python | Go |
|---|---|---|
| `<scheme>://192.168.1.1` bare | 1 | 1 |
| `<scheme>://192.168.1.1` in backticks | 1 | **0** |
| `<scheme>://192.168.1.1` in `**bold**` | 1 | **0** |

A private-network URL written as a markdown code span is silently unflagged by
Go and flagged by Python, for the reason in section 2: Go requires a terminator
character after the IP where Python does not. This is the more serious of the two
halves — a privacy scanner missing a real private URL in the single most common
way it appears in a document — and it is entirely pre-existing, untouched by and
unrelated to this diff.

### Minor
**C. `go/githooks/adversarial_test.go:360-362` (as committed) — inaccurate test
comment.** "These are the two real false-positive inputs ... plus the third
reserved TLD for completeness" sat above a three-element list, describing a
filter that covers four reserved TLDs. The count is muddled and the provenance
detail is task archaeology rather than a statement of what the test pins.
Rewritten.

## 4. Fixes applied

`go/githooks/adversarial_test.go` only; `git diff` touches one file.

1. Added a `.invalid` case (`<h>.internal.invalid`) to
   `TestScanPrivacyReservedSentinelHostnameNotFlaggedAsInternal`, so the suite
   carries one case per reserved TLD and dropping any one from the alternation
   fails a named subtest.
2. Rewrote that test's doc comment to state the property it pins (finding C).
3. Added `TestScanPrivacyDisguisedSentinelHostnameStillFlagged`, an 8-case table
   pinning both directions of the end-of-host requirement: further host label
   after the sentinel (1), bogus port then more host (1), reserved label as a
   mere prefix (1), real port at true end of host (0), the three RFC 2606
   second-level domains (0), and a near-miss second-level domain (1).
   Table-driven with `t.Run(tc.host, ...)` per the file's existing style;
   expected counts taken from the Python reference's output, not from Go's.

## 5. Second defect found: uncapped caveats break the release path

Not part of this task, and not fixed. Recorded because I hit it while executing
the release chain and it is a live landmine in the module being released.

`BuildHookResult` (`go/githooks/envelope.go`) caps the *failing* path at
`maxDiagnostics = 50` and folds the remainder into one summarizing caveat
(`envelope.go:67-77`, pinned by `TestBuildHookResultCapsAtFiftyDiagnosticsWithOverflowCaveat`).
The **warnings-only path at `envelope.go:53-63` has no such cap**: it appends one
caveat per privacy warning and hands the lot to `clikit.NewCaveats`, which
rejects a record with more than 50 members.

Consequence: a tree with 51+ internal-identifier warnings and zero failures
cannot merge, push, rebase, or tag through git-tools at all. Every write verb
routes through `scanGate` (`git-tools/internal/cli/scan.go:345-377`), which turns
a `BuildHookResult` error into `internal.result.build_failed`, exit 90, remedy
text "retry; if this persists, file an issue" — advice that can never succeed,
because the condition is deterministic. Reproduced directly:

```
git-tools scan privacy
{"errors":[{"code":"internal.result.build_failed",
 "message":"build scan result: clikit: caveats has 75 members, max 50"}],
 "exit_code":90,"status":"internal"}
```

Measured warning counts for this repo (`ScanPrivacy` at `TierPublic` over
`git archive` snapshots, so tracked content only):

| tree | warnings |
|---|---|
| `main` | 9 |
| this branch's HEAD | 36 |
| ...of which the branch's two task reports | 25 |
| my first draft of this review | +26 (would have made 62) |

So the module is two ordinary task reports away from bricking its own release
path, and my own review report would have crossed the line. Recommended fix is
six lines, symmetric with the failing path already there: cap the caveat slice at
`maxDiagnostics` and append one `caveats.githooks.findings_truncated` summary.
That belongs in its own task with its own test, not smuggled into this release.

## 6. Re-verification (fresh, after my fix)

```
cd go/githooks
gofmt -l .              -> (no output)
go build ./...          -> OK
go vet ./...            -> OK
go test ./... -count=1  -> ok  github.com/johnrichter/claude-shared-tooling/go/githooks  0.045s
go test ./... -count=1 -v | grep -c '^--- PASS'      -> 58   (57 pre-fix)
                          | grep -E '^--- FAIL|^FAIL' -> (none)
```

Two dispatch-side notes, neither a finding against the work:

- `go test ./go/githooks/... -count=1 -v` from the repo root fails with
  `directory prefix go/githooks does not contain main module`. `go/githooks` is
  its own module and there is no root `go.work`; the suite must run from inside
  the module directory. Both prior reports show the `cd go/githooks` form.
- Repo-root `gofmt -l .` lists `go/toolchain/golang_e2e_probe_test.go`. Confirmed
  pre-existing and unrelated: last touched at `0471fd6` ("M1.P3.T1 Go adapter —
  seven pairs"), and absent from `git diff --name-only main...HEAD`.

## 7. Test-suite assessment

**Adequate after my fix; inadequate as committed.** The implementer's two tests
pin the happy path (sentinel excluded) and the adjacency scoping (real host
alongside a sentinel still flags). What they did not pin is anything that keeps
the exclusion *narrow* — and narrowness is the entire safety argument, since a
loosened sentinel filter is a silent false negative in a privacy scanner. Three
independent single-token mutations left the committed suite fully green; all three
now fail.

Process note for the test-engineer, not a code finding: the verification report
describes authoring four adversarial tests including exactly the disguise case,
running them green, then deleting them. Deleting a test that pins a property no
committed test pins converts durable coverage into a one-time observation. When a
throwaway test turns out to be the only thing exercising a property, commit it.

## 8. Version decision: `go/githooks/v0.6.1` (patch)

- No API surface change: `reservedSentinelSuffix` and `internalHostnameLabel` are
  unexported; no exported signature, type, or symbol moved.
- The prior tag `v0.6.0` (Slack example-token exemption) took a **minor** bump on
  the reasoning recorded at `.task-reports/slack-example-token-exempt-report.md:76-81`:
  "no signature change, no new exported symbol — but it is new scanner behavior
  (a second precedent-setting exemption class), **not a bug fix**." This change
  is the other side of that same distinction: a bug fix restoring parity with the
  reference implementation, adding no capability and no new exemption class.
- No patch tag exists anywhere in the repo yet, but that reflects the absence of
  a prior pure bug fix, not a policy against them. `git-tools tag create` accepts
  any `X.Y.Z`, so nothing tooling-side constrains the choice.
- The implementer's report is self-contradictory here, proposing "likely a patch
  bump ... to `go/githooks/v0.7.0`". `v0.7.0` is a minor bump and would misreport
  a bug fix as a capability change to every consumer reading the tag series.
  Rejected.

## 9. Release + consumption chain

1. **ai-shared-lib merge** — `git-tools merge chore/fix-hostname-sentinel-lookahead`
   from the primary checkout, then `git-tools push main`, both via the provisioned
   absolute path.
2. **Tag** — `git-tools tag create 0.6.1 --shape go/githooks/vX.Y.Z`: signed,
   annotated, pushed through the same non-force remote-advance path as `push`,
   matching the existing tag objects (v0.6.0 is a signed annotated tag whose
   message is its own name).
3. **git-tools pin bump** — new worktree `.claude/worktrees/consume-githooks-v0.6.1`,
   branch `chore/consume-githooks-v0.6.1` off the verified current main tip;
   `go.mod` pin `go/githooks v0.6.0` -> `v0.6.1`; then `go build ./...`,
   `go vet ./...`, `go test ./... -count=1`.
4. **D-1 divergence closure proof** — the point of the bump is not the version
   string but that git-tools' own `scan privacy` stops emitting the false
   positive the D8 differential-corpus proof found. Proven end-to-end by building
   the binary fresh from the bumped worktree and scanning a file containing a
   reserved-sentinel internal hostname.

Outcomes are in the execution log at the end of this file.

## 10. Residual risk

- **Markdown-delimiter gap** (finding D): shipped unfixed in v0.6.1. The sentinel
  exclusion this release adds does not fire for backticked, bolded, or
  bracket-wrapped hostnames, so the false positive it removes will still be seen
  in real markdown docs. Deliberately not fixed: doing so would violate this
  task's match-the-reference acceptance criterion and would alter
  `privateNetworkURL`. **Set expectations accordingly — this release reduces the
  false-positive rate, it does not eliminate it.**
- **`privateNetworkURL` false negatives vs Python** (sections 2 and finding D):
  Go misses a backticked or bolded private URL entirely, and misses the
  `<scheme>://192.168.1.1.attacker.io` shape; Python flags all of them.
  Pre-existing, out of scope, warning-only. Accepted as-is for this release.
- **Uncapped caveats** (section 5): shipped unfixed in v0.6.1. Latent until a
  consuming tree crosses 50 warnings, then a hard exit-90 on every write verb.
- **Two implementations, one behavior, no shared fixture.** This bug existed
  because a Python-side exclusion was never carried into the Go port, and it was
  found by differential corpus comparison rather than by either suite. Nothing in
  either repo prevents the next divergence.

## 11. Plan feedback

1. **Cap the caveat array in `BuildHookResult`** (section 5). Highest priority of
   the three: it is a deterministic hard failure of the sanctioned merge/push/tag
   path, reported with unactionable remedy text, and this repo is two task
   reports away from it. Six lines, symmetric with `envelope.go:67-77`, plus a
   test mirroring the existing errors-path cap test.
2. **Give the internal-identifier check a path-exemption ruleset, or exempt
   `.task-reports/**`.** `ScanPrivacy`'s own doc (`privacy.go:249-252`) states the
   internal-identifier check "runs whole-file with no path exemption; its only
   exemption is by exact matched value". Marker and secret checks each got an
   exemption ruleset; this one did not. The result is that every task report
   discussing hostname behavior must hand-obfuscate its examples (this report
   does, per the header note; commit 7b5c923 did the same for a secret literal),
   and reports are now the dominant source of warnings in the tree — 25 of 36 on
   this branch. That is a documentation-quality tax and a slow march toward
   finding B's cliff.
3. **Widen `_HOST_TERMINATOR` / `hostTerminator` to the delimiters markdown uses,
   in Python and Go together** (finding D). Candidates: `` ` ``, `*`, `[`, `]`,
   `{`, `}`, `;`, `|`, and a `:` not followed by digits. This is the follow-up
   that makes the present release actually deliver its intent in markdown docs,
   and it is the same one-line change on both sides. It must be a coordinated
   cross-repo task with a shared corpus, never a unilateral edit to one
   implementation — the whole point of this task was convergence.
4. **Reconcile the `privateNetworkURL` divergence** (section 2, finding D).
   Decide deliberately whether Go's consuming end-of-host requirement or Python's
   sentinel-only lookahead is intended, make both match, and document the choice.
   Today Go silently misses backticked private URLs that Python flags.
5. **Promote the differential corpus to a checked-in shared fixture.** The
   40-string corpus in section 1 was built for this review and thrown away; the
   D8 proof's corpus was built for that task and thrown away. A fixture file of
   inputs with expected match counts, consumed by both the Go suite and the
   Python suite, converts "we compared them once" into a standing invariant, and
   would have caught both this bug and the section-2 divergence with no
   differential run needed. This is the structural fix; the present task is the
   point fix.
6. **Dispatch correction:** the module test command is
   `cd go/githooks && go test ./... -count=1 -v` (section 6).

## Execution log
