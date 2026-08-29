status: complete

# D1 test-engineer verification — backup-ref migration and LED-033 fix

Independent re-verification of `d1-ai-shared-lib-report.md`. All commands below
were re-run fresh in this session, from this worktree. Raw output is quoted or
referenced by log path where lengthy.

## Verdict

**PASS**, with one non-blocking finding for the quality-reviewer to weigh
(see Check 6): the LED-033 regression test proves the *general principle*
(reachability vs. a naive parent-count formula) but does not, and — given how
`rebuild()` works — cannot, reproduce a failure of the actual removed
`verify()` code as it was literally written. This matches and confirms the
implementer's own flagged open question; I verified it by executing the old
code, not just reading it.

## Check 1 — diff matches the three claimed changes

`git diff main...HEAD --stat`:
```
.task-reports/d1-ai-shared-lib-report.md | 118 ++
claude_tooling/resign_commits.py         |  55 +-
go/git/adversarial2_test.go              |  24 +-
go/git/adversarial_test.go               |  12 +-
go/git/branch.go                         |  14 +-
go/git/branch_test.go                    |  10 +-
go/git/rebase.go                         |  10 +-
go/git/rebase_test.go                    |   6 +-
go/git/ref.go                            |  37 +-
go/git/ref_test.go                       | 130 ++++--
go/git/resign.go                         |   4 +-
go/git/resign_signing_test.go            |   6 +-
go/git/resign_test.go                    |  14 +-
tests/test_resign_commits.py             |  59 +++
14 files changed, 358 insertions(+), 141 deletions(-)
```
Read `go/git/ref.go`, `branch.go`, `rebase.go`, `resign.go` and
`claude_tooling/resign_commits.py` in full diff. Confirmed:
- `backupTagName` -> `backupRefName`, now returns a fully-qualified
  `refs/backup/<base>/<ns>-<short>` name (previously an unqualified
  `backup/...` short tag name).
- `RewriteOutcome.BackupTag` -> `BackupRef` field rename, threaded through
  `branch.go` (`DeleteBranch`), `rebase.go` (`Rebase`), `resign.go`
  (`Resign` dry-run path).
- Every write site (`branch.go`, `rebase.go`, `ref.go` `MoveRef`) changed
  from `r.git(ctx, "tag", name, sha)` to `r.git(ctx, "update-ref", name, sha)`.
- `resign_commits.py`: backup write changed from
  `git(["tag", "-f", backup, tip])` with `backup = f"backup/..."` to
  `git(["update-ref", backup, tip])` with `backup = f"refs/backup/..."`.
  Docstring "Safety model" and `--no-backup` help text updated to match.
- LED-033: `verify()`'s two `git rev-list --count` comparisons replaced by
  a single call to new `_lost_commits()`, which walks the rebuild's
  old->new SHA mapping and checks `git merge-base --is-ancestor <new> <new_tip>`
  for every entry.

All three claimed changes are real and match the description. PASS.

## Check 2 — Go build / vet / test, fresh run

Module root is `go/git` (has its own `go.mod`; not the repo root).

```
$ go build ./...   -> exit 0, no output
$ go vet ./...     -> exit 0, no output
$ go test ./... -count=1 -v   -> ok github.com/johnrichter/claude-shared-tooling/go/git 355.809s
```
57 `--- PASS` lines, 0 `--- FAIL` lines, `TEST_EXIT=0`. Full log at
`/tmp/go_test.log` (this session; not preserved past the container). Matches
the implementer's reported 355.417s / all-pass, independently reproduced at
355.809s.

## Check 3 — Python test suite, fresh run

```
$ python3 -m unittest tests.test_resign_commits -v
Ran 20 tests in 6.519s
OK
```
All 20 tests pass, including `TestOctopusElisionTopologyCheck.test_elided_parent_stays_reachable_after_resign`.
`python3 -m py_compile claude_tooling/resign_commits.py tests/test_resign_commits.py` — clean, exit 0.

Searched the whole `tests/` and `claude_tooling/` tree for any other file
importing or referencing `resign_commits`:
```
$ grep -rl "resign_commits" --include='*.py' .
claude_tooling/resign_commits.py
tests/test_resign_commits.py
```
`tests/test_resign_commits.py` is the only test module touching it — no other
suite needed.

## Check 4 — no code path still creates a backup marker via `git tag`

```
$ grep -n '"tag"' go/git/*.go
go/git/ref_test.go:227:  if tags := runGit(t, dir, "tag", "-l"); tags != "" {
go/git/ref_test.go:246:  if tags := runGit(t, dir, "tag", "-l"); tags != "" {

$ grep -n 'git tag\|refs/tags' claude_tooling/resign_commits.py
259: # A plain ref under refs/backup/, written via update-ref -- not `git tag`. A tag
261: # ... (b) live in refs/tags/, a namespace this tool has no business writing to ...

$ grep -rn 'backupTagName|BackupTag|backup_tag' --include='*.go' --include='*.py' .
(no hits)
```
Both `go` hits are `git tag -l` **assertions** in test code (proving no tag
was created), not creation call sites. The two Python hits are a code
comment. No remaining identifier or literal creation call for the old
tag-based shape anywhere in the repo. PASS.

## Check 5 — backup marker is `refs/backup/...`, via `update-ref`, never `refs/tags/...` / `tag` — live exercise

The pre-existing suite already does this live, against real temp git repos
(not mocks): `go/git/ref_test.go` `TestMoveRef_CASSucceedsAndCreatesBackupRef`,
`TestBackupRefName_WrittenViaUpdateRefInEveryWritingPath` (subtests MoveRef /
DeleteBranch / Rebase), `TestBackupRefName_NeverUnderTagsNamespace`,
`TestMoveRef_BackupIsNeverATagObject` — all ran and passed in Check 2's
`go test` run above.

Independently, I authored and ran my own throwaway test (not committed;
written to `go/git/sdet_independent_verify_test.go`, run, then deleted —
worktree is clean) exercising `DeleteBranch` (distinct call site from the
existing `MoveRef`-focused assertions) against a real `t.TempDir()` git repo:

```go
out, err := r.DeleteBranch(ctx, "refs/heads/feature", head, false)
// asserts: strings.HasPrefix(out.BackupRef, "refs/backup/")
// asserts: !strings.Contains(out.BackupRef, "refs/tags/")
// asserts: runGit(t, dir, "rev-parse", out.BackupRef) == head
// asserts: runGit(t, dir, "tag", "-l") == ""
// asserts: runGit(t, dir, "for-each-ref", "refs/tags/") == ""
// asserts: runGit(t, dir, "cat-file", "-t", out.BackupRef) == "commit"  (a tag object would report "tag")
```
Result: `--- PASS: TestSDET_DeleteBranch_BackupIsPlainRefNotTag (5.61s)`.
The `cat-file -t` check is the strongest single proof: git reports the
object type reachable through the ref name itself as `commit`, which is
what a plain ref pointing straight at a commit looks like; an annotated tag
at that ref would report `tag` (the tag object type), regardless of what the
underlying commit is. PASS.

On the Python side, `tests/test_resign_commits.py`
`TestCli.test_apply_moves_branch_and_is_idempotent` performs the equivalent
live check against a real repo (`git for-each-ref refs/backup/` non-empty,
`git tag -l` empty) and ran clean in Check 3.

## Check 6 — LED-033 regression test: real octopus topology, and would it have failed the old check?

**Real topology, confirmed.** `tests/test_resign_commits.py`
`TestOctopusElisionTopologyCheck.test_elided_parent_stays_reachable_after_resign`
builds three real branches `t1`, `t2`, `t3` off a shared base, with `t3`
branched from `t1` (so `t1` is already an ancestor of `t3`), then runs a real
`git merge -S --no-ff -m "octopus merge t1 t2 t3" t1 t2 t3`. This is git's
actual parent-elision behavior, not a faked/synthetic count mismatch — the
test asserts `len(recorded_parents) == 3` (git elided `t1`) and separately
that `t1` is reachable via `git merge-base --is-ancestor t1 old_tip`, both
of which I re-ran (see below) and confirmed independently.

**Would it have failed the actual, literal old check? No** — and this
confirms, by execution rather than by reading, the implementer's own flagged
open question. I extracted the exact pre-fix `verify()` logic
(`git rev-list --count old_tip` vs `new_tip`, and `--merges --count`
likewise) and ran it against this exact scenario, independently, using a
standalone script driving the real `resign_commits` module and the real
`SigningTestCase` repo harness:

```
commit count: old=5 new=5 equal=True
merge count: old=1 new=1 equal=True
OLD LITERAL CHECK WOULD PASS
```
The test's own `self.assertNotEqual(len(recorded_parents), naive_expected_parents)`
assertion (line ~353 of `tests/test_resign_commits.py`) compares recorded
parent count against a **locally-computed, hypothetical** formula
(`1 + len(branches_named_on_merge_command)`), never against the actual old
`verify()` code path. Because `rebuild()` is a strict 1:1 remap that reuses
each commit's original recorded parent list verbatim and never re-runs a
merge, `old_tip` and `new_tip` always have identical full-history commit and
merge counts by construction, regardless of any elision in the source
history — so the two removed checks could never have flagged this scenario,
or realistically any scenario reachable through this tool's own rebuild
path.

**Conclusion for the quality-reviewer:** Fix 3 is a real, defensible
generalization (reachability is the mathematically correct invariant; a
"parents == branches named" count check is a plausible thing a less careful
implementation of this feature could have had, and it would misfire on this
exact history), and the new code and its test are both correct and pass.
But the regression test does not reproduce a regression that this specific
codebase's specific removed code ever actually had — the risk it targets
was already unreachable under the current `rebuild()` design, as the
implementer stated. This is not a code defect; it is a scope/framing
question about what LED-033 was meant to catch here. Flagging, not
resolving, per the dispatch instruction.

## Raw evidence index (session-local paths)

- `/tmp/go_test.log` — full `go test ./... -count=1 -v` output (355.809s, 57 PASS / 0 FAIL)
- `/tmp/go_build.log`, `/tmp/go_vet.log` — both empty (clean)
- `/tmp/py_test.log` — `python3 -m unittest tests.test_resign_commits -v` (20 tests, OK)
- `/tmp/verify_old_check.py` — standalone script proving Check 6's "old check would pass" finding
