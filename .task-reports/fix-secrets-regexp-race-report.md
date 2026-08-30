# Investigate intermittent SIGSEGV in `git-tools scan privacy` — no reproducible bug found

## Task

Test-engineer reported `git-tools scan privacy --repo <workspace> --privacy-tier private`
(released v1.3.0, built against `go/githooks` v0.6.1) crashing 7/11 runs against
the `workspace` repo tree, with a fault signature landing inside
`sync.(*poolChain).popTail` reached from `regexp.newBitState` /
`regexp.(*Regexp).backtrack`, called from `matchesSecretPattern` inside
`ScanPrivacy`'s file-walk callback — consistent, on its face, with a data race
on a shared mutable value across a concurrent file-walk.

## What I did

1. Read `secrets.go`, `privacy.go`, and `walk.go` in full, then the entire
   call graph a `git-tools scan privacy` invocation actually exercises:
   `git-tools/internal/cli/scan.go` (`newScanPrivacyCmd` → `githooks.ScanPrivacy`
   → `walkScannable` → `filepath.WalkDir`).
2. Reproduced locally: built a real `git-tools` binary from this module's
   current `go/githooks` source via a temporary `replace` directive in a
   throwaway `git worktree` of the sibling `git-tools` repo (removed after —
   never landed in either repo's history), same pattern the
   `invert-email-domain-check` task used. Ran it against the real `workspace`
   tree (400 tracked files) repeatedly: **45 runs, 0 crashes** (15 then 30 more,
   split across two batches to rule out a warm/cold-cache artifact).
3. Built the same binary with `go build -race` and ran it once against the
   same tree: **exit 0, no data-race report.**
4. Ran this module's own suite: `go test ./... -race -count=1 -v` inside
   `go/githooks` — **all tests pass, no race detected.**
5. Read every file in the call graph (`go/githooks/*.go`, `git-tools/internal/
   cli/scan.go`) and grepped the full dependency chain actually reachable from
   `scan privacy` — `go/githooks`, `go/fsx`, `go/clikit`, `go/logkit`,
   `go/sysops`, and `git-tools/{cmd,internal}` — for `go func`, `goroutine`,
   `sync.`, `WaitGroup`, `unsafe`, and any post-compile mutation of a
   `*regexp.Regexp` (`.Longest(`, `SetPolicy`, struct-literal reassignment of
   a `regexp.Regexp`).

## Root cause: none found — the call path has no concurrency at all

`walkScannable` (`go/githooks/walk.go:60`) is `filepath.WalkDir` with a plain
synchronous callback — no goroutines, no worker pool. `ScanPrivacy` and
`ScanSecrets` both write to their own local `failures`/`warnings`/`findings`
slices from inside that single callback, on a single goroutine. `matchesSecretPattern`
only ever calls `(*regexp.Regexp).MatchString`/`FindAllString`, which are
themselves documented safe for concurrent use — moot here since there is
exactly one caller. No `*regexp.Regexp` in either file is mutated after
`regexp.MustCompile` (no `.Longest()`, no `SetPolicy`, no struct-literal
reassignment). `git-tools/internal/cli/scan.go`'s `newScanPrivacyCmd` calls
`githooks.ScanPrivacy` once, synchronously, in `RunE`. Nothing upstream of it
(cobra's command dispatch, `loadConfig`) spawns a goroutine either. I grepped
the full transitive dependency set actually reachable from this one code path
(`go/githooks`, `go/fsx`, `go/clikit`, `go/logkit`, `go/sysops`,
`git-tools/{cmd,internal}`) for `go func`/`goroutine`/`sync.`/`WaitGroup`/
`unsafe` and found none outside a doc comment ("keep in sync") and unrelated
`_test.go` files.

Confirmed by diff: the `go/githooks` tag `v0.6.1` the released `git-tools`
v1.3.0 pins is byte-identical, for `go/githooks`, to this worktree's current
`HEAD` (`git diff go/githooks/v0.6.1 HEAD -- go/githooks` is empty) — so the
code the test-engineer ran and the code I built and race-tested are the same
source.

Given no concurrency exists in the call path, a `*regexp.Regexp`-internal
`sync.Pool` corruption from an application-level data race is not possible
here. There is nothing for it to race against. I could not reproduce the
crash (0/45 runs, plus a clean `-race` build run and a clean `-race` test
suite run), which is itself strong evidence against the "data race in this
module" hypothesis — at the reported ~64% flake rate, 45 consecutive
crash-free runs on unmodified code has a probability on the order of 1 in
10^19 if that rate were real and attributable to this code.

## What I did NOT do: apply a fix

No code in `go/githooks` was changed. Constraint 4/5 of the task (fix the
actual root cause, prove it with ~20-30 repro runs) presupposes a root cause
exists in this module to find. The honest result of the investigation is
that it doesn't, at least not one reachable through static reading, a real
`-race` build, and a real `-race` test run. Patching something anyway —
e.g., adding a mutex nothing races on — would be exactly the "papering over
a symptom" the task tells me not to do, and would be indistinguishable from
guessing.

## Acceptance

- Read `secrets.go`/`privacy.go` in full, understand regex construction and
  the file-walk's concurrency model — met: walk is single-threaded, confirmed
  by reading `walk.go` and the full call graph, not assumed.
- Reproduce the crash before fixing anything — **not met**: 45 runs of a
  fresh build against the real `workspace` tree, 0 crashes. Reported
  honestly rather than fabricating a repro.
- Run the module's tests and a `-race` build under the race detector — met:
  `go test ./... -race -count=1 -v` (all pass, no race), plus a `-race`-built
  `git-tools` binary run against `workspace` (clean).
- Fix the actual root cause — **not applicable**: no root cause found in this
  module's code. Nothing patched.
- Prove the fix with ~20-30+ repeated runs — **not applicable** (no fix), but
  the same run count was spent proving the *absence* of the reported
  behavior on unmodified code instead.

## Sanity result

```
cd go/githooks && gofmt -l .        → (no output, clean)
go build ./...                      → ok
go vet ./...                        → ok
go test ./... -race -count=1 -v     → PASS, ok (all tests, no race detected)
```

Plus, outside the module (scratch, not committed): a real `git-tools` binary
built via a temporary `replace` against this worktree, run 45× against
`/home/bits/Development/workspaces/psa-platform/workspace`'s tracked tree
(0/45 crashed), and once as a `-race` build (clean, exit 0).

## Assumptions & deviations

- Assumed "the file-walk" in the task's own framing meant `git-tools scan
  privacy`'s actual, current call path, and verified that assumption by
  reading the code rather than trusting the framing — the framing turned out
  to be inaccurate (there is no concurrent file-walk in this codebase today).
- Did not touch `git-tools`: built a scratch binary from a temporary `git
  worktree` + `replace` directive, both removed/deleted before finishing.
  No commit landed in `git-tools`.
- Made no code change in `go/githooks`, so nothing is staged for this branch
  beyond this report.

## Hand-off notes

- **Orchestrator/test-engineer**: if the crash is real and reproducible on
  the original hardware/CI, the next useful artifact is a captured crash
  dump or `GOTRACEBACK=crash` output from an actual crashing run, plus the
  exact Go toolchain/GOARCH/GOOS that built the released v1.3.0 binary I
  reproduced against (mine: `go1.27.0 linux/arm64`, `go/githooks` v0.6.1
  content-identical to current `HEAD`). Without a captured fault or a
  reproducing environment, there's no further diagnosis to do from source
  reading alone — I would be guessing at a fix with nothing to verify it
  against.
- Worth checking whether the 11 original runs happened on a different
  GOARCH (amd64) or an older Go toolchain than what's pinned now — historic
  Go runtime/`sync.Pool` memory-model bugs on non-x86 architectures existed
  in older Go versions and were fixed upstream. If the release binary was
  built with a stale toolchain, that's an infrastructure/pinning issue, not
  a `go/githooks` code issue, and out of this module's scope to fix.
- If a future run does reproduce, capture the exact repro command's PID/core
  dump before the process exits. `fatal error` crashes terminate immediately,
  and the stack trace in a log is often the only forensic evidence. A core
  dump would let a debugger identify the actually-corrupted allocation rather
  than inferring from the crash site.
