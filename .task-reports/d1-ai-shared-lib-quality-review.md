status: complete

# D1 quality review — backup-ref migration and LED-033 reachability fix

Reviewed the full `main...HEAD` diff, resolved the open question both prior agents flagged,
and verified the `tag.gpgSign` precondition claim against real git rather than against the
implementer's assertion. Every command below was run fresh in this worktree.

## Verdict

**ACCEPT WITH FIXES.** All three fixes are correct and land where they should. The
backup-ref migration is clean and the `tag.gpgSign`-only claim is confirmed by execution.
LED-033's fix is right, but its regression test was hollow — I reproduced the real failure,
replaced the test with one that genuinely proves the fix's necessity, and closed an unmet
acceptance criterion the brief spells out.

## The LED-033 open question — resolved

Both prior agents were right that the committed regression test did not reproduce the
original false positive. They were also both wrong about why: the implementer's stated
reason ("`rebuild()` is a strict one-to-one remap, so counts are always mathematically
identical, so the removed checks could not misfire") is **false**, and the test-engineer
adopted it. LED-033 is a real, live defect in this exact code, and I reproduced it.

### What LED-033 actually says

From `workspace/.dat/ledger.md` LED-033 (raised 2026-08-08), which neither prior agent
read: an apply refused a branch move after a `commit-tree` rebuild, failing
`commit count preserved` (**558 → 424**) and `merge count preserved` (**93 → 65**), while
`tip tree identical`, `tip content diff empty`, and `all rebuilt trees identical` all
passed. The cause is named as "the rebuild collapsing redundant octopus-merge parent
edges". The failure is in **the rebuild**, not in git's merge-time behaviour.

### The topology that actually trips the old check

The rebuild is **not** shape-preserving. Two mechanisms, both verified against git 2.55.0:

1. `git commit-tree` rebuilds each commit from scratch and does not carry over headers it
   did not generate — notably `gpgsig`. Two old commits that differed **only** in being
   signed therefore rebuild to one and the same new commit.
2. `git commit-tree` **deduplicates duplicate parents**. Once (1) collapses two commits, a
   merge's two parent edges to them dedup into a single edge, and a two-parent merge stops
   being a merge at all.

Probed directly (git 2.55.0):
```
commit-tree -p A -p A  <tree>   -> 1 parent recorded   (duplicates deduped)
commit-tree -p A -p C  <tree>   -> 2 parents recorded   (A ancestor of C: NOT elided)
```
The second line matters: `commit-tree` does **not** elide redundant ancestors, which is why
the committed test's merge-time-elision scenario could never trip the old check.

### The reproduction

Repo shape: one scripted checkpoint recorded twice off the same base — identical tree,
message, author/committer identity and both dates — one signed, one not, then merged
together. Running the tool's real `rebuild()` and both the old and the new check:

```
A (signed)  : b97686c3...  tree 231c81dc...
B (unsigned): 80b4169a...  tree 231c81dc...     distinct commits, same tree
M parents   : [A, B]

mapping: {A -> b97686c3, B -> b97686c3, M -> bc8eafa2}   COLLAPSED (A' == B'): True
M' parents: [A']                                          (2 edges -> 1)

OLD check 'commit count preserved': old=4 new=3 -> FAIL
OLD check 'merge count preserved' : old=1 new=0 -> FAIL
NEW check reachability            : lost=[]     -> PASS
full verify(): every other check PASS
```

This is LED-033's exact symptom set, at n=1 instead of n=558: both count checks refuse, every
content and fidelity check passes, and nothing is lost. Scaled up, a merge-heavy scripted
checkpoint history gives 558 → 424 and 93 → 65 for the same reason.

The collapse is legitimate: the two old objects encode the identical change with identical
metadata and differ only in a signature the tool is there to replace, so after re-signing
they genuinely *are* one commit. Content and reachability both survive intact.

### Verdict on the committed test, and what I did

The committed `test_elided_parent_stays_reachable_after_resign` tests git's **merge-time**
parent elision, which happens before the tool ever sees the history and is reproduced
verbatim by the rebuild. It cannot trip the old check — as the test-engineer proved by
execution. Worse, it asserted otherwise against a formula no version of this tool ever
used (`naive_expected_parents = 1 + 3`), so it read as proof of a necessity it did not
demonstrate. **Inadequate.** Fixed, not merely noted.

## Findings

### Blocking

None.

### Major

1. `tests/test_resign_commits.py:350-353` (pre-fix) — green-but-hollow regression test. The
   `naive_expected_parents = 1 + 3` assertion compared the recorded parent count against a
   locally invented formula, never against the removed code, and the class docstring
   presented that as proof a count-based check would misfire. Nothing in the test could
   fail if the fix were reverted. **Fixed** — see below.
2. `claude_tooling/resign_commits.py:200-205` (pre-fix) — unmet acceptance criterion. The
   remediation brief states LED-033's close condition as "the `resign-commits` check
   compares reachability, **and prints both counts**" (brief lines 228, 598), and the ledger
   asks for a refusal that identifies which commits have no match "so an operator does not
   hand-run format checks across dozens of merges". The refusal printed a bare SHA list with
   no count and no total, and would have dumped all 134 SHAs unbounded on the real incident.
   **Fixed.**
3. `claude_tooling/resign_commits.py:175-184` (pre-fix) — `_lost_commits`' docstring named
   the wrong mechanism ("git elides a parent that is already reachable through another
   parent in the same merge"). That is merge-time elision, which this check is not about and
   which cannot trip a count comparison. A future maintainer reading it would not know what
   the check defends against. **Fixed.**

### Minor

4. `go/git/adversarial2_test.go:15` — doc comment still said "writes a backup **tag**"; the
   rename missed it because the phrase was split across a line break. **Fixed.**
5. `go/git/adversarial_test.go:167` — same, "checking the **tag** exists". **Fixed.**
6. `go/git/adversarial_test.go:162` — the doc comment opened with
   `TestDeleteBranch_BackupRefExistsEvenIfUpdateRefWereToFail`, which is not the name of the
   function it documents (`TestDeleteBranch_BackupRefPointsAtExactPreDeleteSHA`).
   Pre-existing, in this diff's blast radius. **Fixed.**
7. `tests/test_resign_commits.py:14-21` — the file's "Test strategy" list enumerates the
   suite's axes and had no entry for the topology axis. **Fixed.**

### Reviewed and found correct — no action

- **`update-ref`/`refs/backup/` migration.** All four write sites (`ref.go` `MoveRef`,
  `branch.go` `DeleteBranch`, `rebase.go` `Rebase`, and the `resign.go` dry-run name
  computation) moved to `update-ref`. The name is fully qualified, so the earlier
  double-`refs/tags/` bug (commit `3ee51d0`) cannot recur by construction — the name no
  longer passes through anything that prefixes it. Backup-before-rewrite ordering is
  preserved at every site, including the failure paths.
- **`tag.gpgSign`-only precondition — confirmed by execution, not by claim.** Against git
  2.55.0 with a deliberately unusable signing key:

  | config | `git tag <name> <sha>` | `git tag -m msg <name> <sha>` | `git update-ref refs/backup/<n> <sha>` |
  |---|---|---|---|
  | none | exit 0, objtype `commit` | exit 0 | exit 0 |
  | `tag.forceSignAnnotated=true` | exit 0, objtype `commit` | exit 128 (signing) | exit 0 |
  | `tag.gpgSign=true` | **exit 128** | exit 128 (signing) | exit 0 |
  | both | **exit 128** | exit 128 (signing) | exit 0 |

  `tag.forceSignAnnotated` alone leaves a lightweight `git tag <name> <sha>` untouched — it
  only governs tags that are already annotated. `tag.gpgSign` promotes even the lightweight
  form into a signed annotated tag, which then fails outright (`fatal: no tag message?`,
  ahead of any key use). The claim in `ref.go:53-61` is accurate. `update-ref` is unaffected
  by every combination.
- **Naming consistency.** No `backupTagName`, `BackupTag`, `backup_tag`, `backup-tag`, or
  prose "backup tag" survives anywhere in the worktree after fixes 4-6.
- **Python fix parity.** `resign_commits.py`'s backup write is the same shape as the Go one,
  with the docstring, `--no-backup` help text, and the CLI's printed line all updated. Its
  test now asserts both directions: a `refs/backup/` ref exists **and** `git tag -l` is
  empty.
- **Go test adequacy.** `ref_test.go`'s `cat-file -t`-style checks and the
  `TestBackupRefName_*` set are genuinely adversarial: they pin the namespace, the shape,
  the write mechanism, and the dry-run/write agreement, and the three writing paths are
  covered as subtests.

## Fixes applied

| File | Change |
|---|---|
| `claude_tooling/resign_commits.py` | `_lost_commits` docstring restated to the real mechanism (finding 3). Refusal detail now reads `N of M rebuilt commits unreachable: <first 10>, ...` (finding 2). |
| `tests/test_resign_commits.py` | Class renamed `TestOctopusElisionTopologyCheck` -> `TestTopologyCheckReachabilityVsCount`, docstring corrected. Added `test_rebuild_collapse_refused_by_the_old_count_check_not_by_reachability` — the real reproduction. Added `test_genuine_unreachability_is_refused_and_reports_both_counts` — the missing negative test. Removed the invented `naive_expected_parents` assertion and restated what the elision test actually pins. Strategy list entry 5. |
| `go/git/adversarial2_test.go` | Stale "backup tag" doc comment (finding 4). |
| `go/git/adversarial_test.go` | Stale "the tag" doc comment and mismatched function name in the doc comment (findings 5, 6). |

No production behaviour changed except the refusal message; the reachability check's
pass/fail semantics are untouched.

## Re-verification (fresh, after fixes)

Go (module root is `go/git`, which has its own `go.mod`):
```
$ go build ./...          -> exit 0, no output
$ go vet ./...            -> exit 0, no output
$ go test ./... -count=1  -> ok github.com/johnrichter/claude-shared-tooling/go/git  355.181s
```

Python:
```
$ python3 -m py_compile claude_tooling/resign_commits.py tests/test_resign_commits.py  -> clean
$ python3 -m unittest tests.test_resign_commits -v
Ran 22 tests in 7.344s
OK
```
22 tests, up from 20: one replaced in place, two added.

**Mutation check on the new regression test.** I temporarily reinstated the two removed
count checks in `verify()` verbatim and re-ran the class:

```
test_rebuild_collapse_refused_by_the_old_count_check_not_by_reachability ... FAIL
  {'tip tree identical': True, 'tip content diff empty': True,
   'commit count preserved': False, 'merge count preserved': False,
   'every rewritten commit remains reachable from the new tip': True,
   'no unsigned (N) commits remain': True, 'no bad (B) signatures': True,
   'all rebuilt trees identical to originals': True,
   'all author/committer/date/message preserved': True,
   'boundary is ancestor of new tip': True}
test_elided_parent_stays_reachable_after_resign ... ok      (still passes: hollow, as flagged)
```
The new test fails under the old code and passes under the new, with exactly the pass/fail
split LED-033 reported. The mutation was then reverted and the full suite re-run green
(above). Three throwaway probe scripts were deleted; the worktree carries only the four
files listed in Fixes applied.

## Test-suite assessment

Adequate now. Before the fixes it had one real gap and one hollow test:

- The reachability check had **no negative test** — nothing in the suite ever made
  `_lost_commits` return a non-empty list, so a check that always returned `[]` would have
  passed the whole suite. Closed by
  `test_genuine_unreachability_is_refused_and_reports_both_counts`, which also pins the
  refusal message's numbers.
- The only LED-033 test could not fail if the fix were reverted. Closed by the reproduction
  test, which is mutation-verified above.

Remaining gap, deliberately not closed here (out of scope, low value): the collapse
mechanism depends on the signature being deterministic for the twins to land on the same
SHA. The harness uses ssh/ed25519, which is deterministic, and the test asserts
`mapping[signed] == mapping[twin]` so it fails loudly rather than silently degrading if that
ever stops holding. Under GPG with a randomised nonce the twins would not collapse and the
old check would not have misfired on that particular history — the defect is real either
way, since duplicate-parent dedup alone also drops the merge count.

## Residual risk

1. **Cross-repo API break, already planned.** `RewriteOutcome.BackupTag` -> `BackupRef` is a
   breaking rename for `git-tools`, which reads it at
   `git-tools/internal/result/git.go:70` and `git-tools/internal/signing/signing.go:185,187`,
   plus five test files and the `backup_tag` JSON key surfaced through
   `internal/cli/merge.go:312`. The remediation brief already schedules exactly this
   (brief line 220, including the minor version bump for the changed JSON shape), so
   `git-tools` will not compile against this branch until that task lands. Sequencing item,
   not a defect here.
2. **`update-ref` overwrites where `git tag` refused.** `git tag <name> <sha>` fails on an
   existing name; `git update-ref <ref> <sha>` silently overwrites. The nanosecond timestamp
   plus short SHA in `backupRefName` makes a collision effectively impossible, so this is
   noted rather than changed. If it is ever wanted, `git update-ref <ref> <sha> ""` restores
   the refuse-if-exists behaviour in one argument.
3. **`refs/backup/` refs accumulate and nothing prunes them.** True of the old backup tags
   too, so not a regression — and strictly better than before, since these refs are outside
   `refs/tags/` and so are no longer swept up by `git push --tags` or `--follow-tags`. If a
   prune verb is ever added, `refs/backup/` is a new ref class it must enumerate.

## Plan feedback

1. **LED-033's ledger diagnosis is close but imprecise, and its proposed fix is weaker than
   what landed.** "The rebuild collapsing redundant octopus-merge parent edges" is the
   symptom; the cause is `commit-tree` regenerating rather than copying the `gpgsig` header,
   which collapses signature-only-distinct commits, after which duplicate parent edges dedup.
   Worth correcting in the ledger so the next reader does not go looking for merge-time
   elision, as this task's implementer and test-engineer both did. The ledger's proposed fix
   (compare tree-hash **sets**) is weaker than reachability: identical trees recur constantly
   across a real history, so a tree-set comparison would pass a rewrite that genuinely
   dropped a commit whose tree happened to match another. Reachability over the rebuild's own
   old->new mapping is the correct invariant. Recommend closing LED-033 against the
   reachability check and recording that the tree-set proposal was superseded.
2. **The count checks were load-bearing in one direction the fix keeps.** Both removed checks
   only ever compared totals; neither could localise a loss. The replacement is strictly
   stronger — it is per-commit and names the offenders — so nothing was traded away. Noting
   it because a reviewer of the diff alone might read "two checks replaced by one" as reduced
   coverage.
3. **The brief's own counting rule caught a gap the task description did not.** LED-033's
   close condition includes "prints both counts", which the implementation had dropped
   entirely. Task dispatches derived from this brief should carry that clause explicitly, or
   it will keep getting lost between the brief and the implementer.
