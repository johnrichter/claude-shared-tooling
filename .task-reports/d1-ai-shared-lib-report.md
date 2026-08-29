status: complete

# D1 report — backup-tag to backup-ref migration, plus LED-033 reachability fix

## Fix 1 — go/git: backup marker moves from `git tag` to `git update-ref`

Problem: `go/git` marked the pre-rewrite state of a ref with `git tag <name> <sha>`.
An annotated tag write needs a GPG signature whenever `tag.gpgSign` is on, so this
disposable recovery marker could collide with signing setup. `tag.forceSignAnnotated`
alone does not cause this; only `tag.gpgSign` does.

Fix: the marker is now a plain ref under `refs/backup/`, written with
`git update-ref <ref> <sha>`. A plain ref never needs a signature and never touches
`refs/tags/`.

Renames: `backupTagName` to `backupRefName`; `RewriteOutcome.BackupTag` to
`RewriteOutcome.BackupRef`. The returned name is now fully qualified:
`refs/backup/<base>/<timestamp-ns>-<short-sha>` (the earlier fix in commit `3ee51d0`
had already corrected a double-`refs/tags/`-prefix bug in the old tag-based name; this
change stands on top of that fix, not a revert of it).

Files touched:
- `go/git/ref.go` — `backupRefName`, `MoveRef`
- `go/git/branch.go` — `DeleteBranch`
- `go/git/rebase.go` — `Rebase`
- `go/git/resign.go` — `ResignOptions` doc comment, dry-run path
- `go/git/ref_test.go` — renamed and rewritten tests; regex now matches
  `refs/backup/...`; new test proves no tag object is ever created
- `go/git/branch_test.go`, `go/git/rebase_test.go`, `go/git/resign_test.go`,
  `go/git/resign_signing_test.go`, `go/git/adversarial_test.go`,
  `go/git/adversarial2_test.go` — field/function renames, `git tag -l` checks
  replaced with `git for-each-ref refs/backup/...`

`git-tools` (a separate repo) was not touched. A later task repins it to the new
names.

## Fix 2 — claude_tooling/resign_commits.py: same defect, independent call site

Problem: `resign_commits.py` had its own, separate `git tag <name> <sha>` backup
write. Commit `3ee51d0` never touched this file, so it carried the same
`tag.gpgSign` collision risk independently.

Fix: same pattern as Fix 1. The backup marker is now
`refs/backup/<sanitized-ref>-pre-resign-<UTC-timestamp>`, written with
`git update-ref`. Updated the module docstring's "Safety model" section and the
`--no-backup` help text to match.

Files touched:
- `claude_tooling/resign_commits.py` — docstring, `--no-backup` help text, the
  backup-write block in `main()`
- `tests/test_resign_commits.py` — `test_apply_moves_branch_and_is_idempotent`
  now checks `git for-each-ref refs/backup/` and asserts `git tag -l` is empty,
  instead of checking for a tag

## Fix 3 — LED-033: count-based topology check replaced with reachability

Problem: `resign_commits.py`'s `verify()` checked topology by comparing
`git rev-list --count` on the old and new tip (twice: total commit count, and merge
count). A count-based check is the wrong invariant for merge topology: a legitimate
octopus merge can silently drop a parent from its recorded parent list whenever that
parent is already an ancestor of another parent in the same merge (git's own elision
behavior) — no commit is lost, but a check built around counting parents or commits
around that event is exposed to a false positive.

Fix: `verify()` now checks reachability instead. New helper `_lost_commits(new_tip,
mapping, cwd)` walks every old-to-new commit pair from the rebuild and confirms the
new commit is still an ancestor of `new_tip` via `git merge-base --is-ancestor`. This
replaces both the "commit count preserved" and "merge count preserved" checks with one
check: "every rewritten commit remains reachable from the new tip." The docstring's
verification bullet list was updated to match.

Regression test: `TestOctopusElisionTopologyCheck.test_elided_parent_stays_reachable_after_resign`
in `tests/test_resign_commits.py` builds a real octopus merge (`t1`, `t2`, `t3`) where
`t3` branches off `t1`, so `t1` is already an ancestor of `t3` at merge time. `git
merge` elides `t1` from the recorded parent list — the merge records 3 parents, not
the 4 a naive "HEAD plus 3 named branches" count-based check would expect — while `t1`
remains fully reachable via `t3`. The test asserts the naive expected-count does not
match the recorded count (proving a count-based check would false-flag this), then
runs the tool's actual `rebuild`/`verify` over it and asserts `_lost_commits` reports
nothing lost and `verify()` passes in full.

Files touched:
- `claude_tooling/resign_commits.py` — `_lost_commits`, `verify()`, docstring
- `tests/test_resign_commits.py` — new `TestOctopusElisionTopologyCheck` test class

Scope note: no equivalent count-based topology check exists anywhere in `go/git`.
The Go side's own merge/rebase logic never compares a raw commit or parent count to
detect topology divergence, so Fix 3 is scoped to `resign_commits.py` only.

## Test results

- `go test ./... -count=1` (package `go/git`): `ok
  github.com/johnrichter/claude-shared-tooling/go/git 355.417s` — all tests pass.
- `go build ./...` and `go vet ./...`: clean, no errors.
- `python3 -m unittest tests.test_resign_commits -v`: `Ran 20 tests in 6.571s — OK`
  (includes the new elision regression test).
- `python3 -m py_compile claude_tooling/resign_commits.py tests/test_resign_commits.py`:
  clean.

## Open question

Fix 3's target and its exact framing rested on a judgment call. The literal
count-based checks in `verify()` ("commit count preserved", "merge count preserved")
compare `git rev-list --count` over the FULL history reachable from each tip, not a
per-merge parent count. Given how `rebuild()` works — it is a strict one-to-one remap
that reuses each commit's exact original recorded parent list and never re-runs a
merge — the total reachable-commit count and merge-commit count before and after a
rebuild are always mathematically identical, regardless of any octopus-elision
topology in the original history. This means the two removed checks could not
actually misfire as a false positive in this tool's own current data flow: any
parent elision was already baked into the original commit's recorded parents before
this tool ever sees it. The fix and its regression test were built anyway, per the
task's explicit instruction, by demonstrating the general principle with a
still-realistic scenario: a naive count-based check (recorded parents vs. branches
named on the merge command) does false-flag an elided octopus merge, while the new
reachability-based check does not. Recommend the quality-reviewer confirm this
framing is the intended LED-033 target, since no other count-based topology check
was found anywhere else in the repository (Go or Python).
