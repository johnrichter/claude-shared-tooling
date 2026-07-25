# Rollback to the pre-M0 build-helpers artifact

The pre-M0 artifact is the tree at commit `dfe52b23aa5d38fe9cd23051c650e06a5125eda9` (M0.P1.T1, the baseline capture). It is reachable from `main`, so recovering it needs nothing but the repository.

## Procedure

```sh
python3 tooling/m0-differential/check.py --rollback /path/to/scratch
```

The command exports the commit with `git archive` into a scratch directory that must not already exist, compiles `go/build-helpers` there, and refuses to hand back the binary unless its `--help` output matches `testdata/pre-m0-baseline/help.txt` byte-for-byte. That match is what makes the recovered binary provably the pre-M0 one and not something merely built from a nearby commit.

`git archive` reads the object database and nothing else: no worktree is registered, no ref is written, and neither the repository nor the current checkout is modified. The scratch directory is the only thing produced, and deleting it undoes the whole operation.

The differential performs this same recovery on every run, so the path stays exercised rather than merely documented.

## Executed

Run once against a scratch checkout on 2026-07-25:

```
$ python3 tooling/m0-differential/check.py --rollback /tmp/m0-rollback-proof
m0-differential: pre-M0 artifact recovered at /tmp/m0-rollback-proof/build-helpers from
dfe52b23aa5d38fe9cd23051c650e06a5125eda9 (help capture sha256 8d383f2762231d3f)
$ echo $?
0
```

The recovered binary's `self-check` usage line carries the four literal band flags and no `--band`, and its output names no `roster_stale` field — the pre-M0 surface, as captured.

## Scope

This restores the binary, not the repository. Reverting the M0 commits themselves is an ordinary `git revert` and is out of this harness's scope; what the harness guarantees is that the artifact M0 replaced can always be rebuilt and identified.
