# build-helpers

Deterministic mechanics for the `build-with-team` orchestrator — the parts that must be exact,
not LLM guesswork: plan validation, content-addressed reconciliation, tier checks, front-door
routing, execution-state transitions, and rendering the `plan.md` / `execution.md` mirrors.

Go, stdlib-only, no third-party dependencies. Binaries are **pre-compiled and committed to git
LFS** at `../.bin/build-helpers-<goos>-<goarch>`, one per supported OS/arch — the orchestrator
execs the matching one directly (no build-on-the-fly). They do not change at runtime; edit the
sources only when changing behavior, then recompile every target + re-commit.

## Architecture

- **`bh/`** — the API. Pure functions: parsed values in, values/errors out. No file IO, no
  `os.Exit`. Fully unit-tested (`go test ./...`).
- **`main.go`** — the CLI. The only layer that touches the filesystem and sets exit codes.
- **`../.bin/build-helpers-<goos>-<goarch>`** — the committed, LFS-tracked binaries the skill
  invokes (`go/.bin/`, sibling of this module — shared across any future `go/` module).

## Build / recompile

Each binary is platform-specific. After editing the sources, cross-compile every supported target
and re-commit all four (pure Go, no cgo, so this needs no target toolchains beyond the Go compiler
itself):

```sh
GOOS=linux  GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o ../.bin/build-helpers-linux-amd64 .
GOOS=linux  GOARCH=arm64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o ../.bin/build-helpers-linux-arm64 .
GOOS=darwin GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o ../.bin/build-helpers-darwin-amd64 .
GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false -ldflags='-s -w' -o ../.bin/build-helpers-darwin-arm64 .
# .gitattributes routes go/.bin/* through LFS
```

`-buildvcs=false` is mandatory: without it Go stamps the building checkout's VCS revision/time/dirty
state into every binary, which makes the output non-reproducible and breaks the committed==fresh
parity gate (`parity_test.go`). Build with the **pinned toolchain go1.26.x** — a different Go minor
version can emit different bytes and will fail the parity gate as a false "drift"; recut all four
binaries whenever the pinned toolchain is intentionally bumped.

## Canonical state model

Mirrors the `plan.json` / `plan.md` split for the live tracker:

```
plan.json       (immutable spec, product-architect)  ─ render      → plan.md       (human mirror)
execution.json  (canonical live state, this tool)     ─ render-exec → execution.md  (human mirror)
```

`execution.json` is mutated **only** through `init-exec` / `record` / `log-note` / `reconcile-exec`,
then the mirror is re-rendered. `execution.md` is generated — never hand-edited.

## Commands

| Command | In → Out | Exit |
| --- | --- | --- |
| `render <plan.json>` | → `plan.md` (stdout) | 0/2 |
| `diff <old> <new>` | → `{carried,changed,added,removed}` | 0/2 |
| `check-tiers <plan.json>` | → `{ok,issues}` | **1** if not ok |
| `hash <plan.json>` | → `{taskId: contentHash}` | 0/2 |
| `validate <plan.json>` | → `{ok,errors,warnings}` (schema + id-uniqueness + hierarchy + dep-integrity + cycles + tiers) | **1** if not ok |
| `classify <project-dir>` | → `{design,plan,execution,route}` | 0/2 |
| `init-exec <plan.json> --slug S [flags]` | → `execution.json` | 0/2 |
| `render-exec <execution.json> <plan.json>` | → `execution.md` (stdout) | 0/2 |
| `next <execution.json> <plan.json>` | → `{task}` \| `{done}` \| `{blocked,reason}` | 0/2 |
| `batch <execution.json> <plan.json> [--max N]` | → `{tasks}` \| `{done}` \| `{blocked,reason}` — up to N independent, file-surface-disjoint tasks ready for parallel fan-out (default N=4, hard cap 8) | 0/2 |
| `verify-surface <plan.json> <taskId> [--root DIR]` | → `{ok,violations}` — the pre-done assertion: checks taskId's declared `file_surface` against disk rooted at `--root` (default cwd): glob ≥1 match, dir non-empty, a `required` entry additionally non-trivial (non-empty). Run before `record … --status done`. | **1** if not ok |
| `record <execution.json> <taskId> [flags]` | → `execution.json` (per-task transition) | 0/2 |
| `log-note <execution.json> --note "…"` | → `execution.json` (plan-level log entry, e.g. gate results) | 0/2 |
| `reconcile-exec <execution.json> <old> <new> [flags]` | → `execution.json` | 0/2 |
| `retrieve <plan.json\|execution.json> --level L [--id ID --field NAME]` | → level-of-detail projection: `outline` (every entity) → `milestone`/`phase` (one group's tasks) → `task` (one full record) → `field` (one named field). Read-only, deterministic, never decides eligibility. | 0/2 |
| `migrate-project <plan.json> <execution.json> [--dry-run]` | → `{already_v2,dry_run,changes,warnings}`; upgrades an in-flight v1 project to the v2 schema in place | 0/2 |

Exit codes: `0` ok · `1` validation failed · `2` usage/IO error. Flags follow positionals:
`<positionals…> --key value --flag`. `--at <ISO>` overrides the timestamp on state-mutating
commands (default: now, UTC) for reproducibility.

### Flags

- `batch`: `--max` (default 4; clamped to hard cap 8)
- `verify-surface`: `--root` (default `.`)
- `init-exec`: `--slug` (required) `--name --topic --design-updated --plan-updated --pause --budget --rates --override --at`
- `record`: `--status --test --review --commit --cost --note --run-id --override --at`
- `reconcile-exec`: `--design-updated --plan-updated --at`
- `retrieve`: `--level` (required: `outline|milestone|phase|task|field`) `--id` (required for milestone/phase/task/field) `--field` (required for `field`); doc type (plan vs execution) is sniffed from content, not the filename

### Retrieval detail levels

The doc positional accepts either `plan.json` or `execution.json` — the same four levels serve
both, projected honestly per doc: plan.json carries deps but no live status; execution.json
carries status but no deps and no independent milestone/phase objects (only plan.json has those),
so `milestone`/`phase` over execution.json are synthesized from the hierarchical task-ID prefix
(`M<n>.P<n>.T<n>`) plan.json's IDs establish. Every level recomputes fresh from the doc on each
call — nothing is cached or accumulated — and retrieval never decides task eligibility; `next`/
`batch` remain the sole scheduling authority.

| Level | Needs | Returns |
| --- | --- | --- |
| `outline` | — | every milestone/phase/task (plan.json) or every task row (execution.json), id+name(+status/deps as available) |
| `milestone` / `phase` | `--id` | the group's id/name (plan.json) or id (execution.json) + its child tasks at outline granularity |
| `task` | `--id` | one task's full record (the plan spec fields, or the execution state fields) |
| `field` | `--id --field` | `{id, field, value}` for one named field of one milestone/phase/task |

## migrate-project

Upgrades an in-flight v1 project's `plan.json` + `execution.json` to the v2 harness shapes, in
place, resolving the v1→v2 migration-coverage open question.

```sh
build-helpers migrate-project plan.json execution.json --dry-run   # preview, writes nothing
build-helpers migrate-project plan.json execution.json             # applies + writes both files
```

Prints a `MigrateReport` JSON to stdout: `{already_v2, dry_run, changes, warnings}`. Each entry in
`changes` names the target file, entity id, field, from/to value, and why. `warnings` flags values
that need operator review rather than being silently rewritten.

**v2 deltas covered** (derived from the actual v1→v2 schema diff `bh/migrate.go` implements):

- `execution.json` `schema_version` stamp — absent on every pre-v2 file; stamped to the current
  version via the same tested `MigrateExec` upgrade path used elsewhere. A file newer than this
  build supports is a hard error, not a silent field drop.
- Entity `name` backfill — v1 milestones/phases/tasks and the execution doc name can be
  empty or absent; backfilled deterministically (milestone/phase from id, task name from the first
  sentence of its summary, execution doc name from `<project> — Execution`). `ExecTask` has no
  per-row name field in v2, so there is nothing to add there.
- Plan-schema enum deltas (`model`, and by extension any future `effort`/`deliverable_kind`
  additions) — v2 changed these additively only (ids added, none renamed/removed), so no value
  needs rewriting. A `model` id outside the current known set is flagged in `warnings` for operator
  review, never silently remapped; a `legacyModelRenames` table exists in `bh/migrate.go` for a
  future rename but is currently empty.

**Not covered / explicitly out of scope**: accounting, attribution, and `true_usage` fields added
in v2 are never touched by migrate-project. They're nil-safe/omitempty, so an absent field on a v1
file is the tolerated empty case — synthesizing cost/usage data a v1 run never measured would
violate the lossless guarantee below.

**Lossless guarantee**: only additive fields (entity names) and the execution `schema_version`
stamp are ever written. Task status, done state, commit SHA, cost, tokens, test/review verdicts,
dependencies, and the execution log are never touched — a migrated project resumes with byte-true
completed work.

**Idempotency**: running migrate-project on an already-v2 project produces zero changes
(`already_v2: true`) and the CLI writes nothing — a re-run is a true no-op.

## Develop

```sh
go test ./...                                # unit tests (incl. plan-schema.json drift guard)
go vet ./...
gofmt -l .                                   # empty = formatted
../.bin/build-helpers-<goos>-<goarch> --help # CLI usage (pick your platform's binary)
```
