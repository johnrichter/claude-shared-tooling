# Dependency spike — `**`-capable glob library

## Decision

**Pin `github.com/bmatcuk/doublestar/v4` at `v4.10.0`.**

```
require github.com/bmatcuk/doublestar/v4 v4.10.0
```

```
github.com/bmatcuk/doublestar/v4 v4.10.0 h1:zU9WiOla1YA122oLM6i4EXvGW62DvKZVxIe6TYWexEs=
github.com/bmatcuk/doublestar/v4 v4.10.0/go.mod h1:xBQ8jztBU6kakFMg+8WGxn0c6z1fTSPVIjEY1Wr7jzc=
```

**Application site:** `go/build-helpers/bh/surface.go` — the shared lib's file-surface verifier. It currently matches allow/deny path patterns with Go's `filepath.Match`/`fs.Glob`, neither of which supports `**` (globstar). Adopting doublestar here is what fixes the gap.

## Problem

Go's standard library has no globstar. `filepath.Match` and `fs.Glob` treat `**` as an ordinary `*` — it matches within one path segment, never recurses across `/`. A pattern like `dir/**` (everything under `dir`, any depth) or `**/child*` (a `child*` file at any depth) cannot be expressed with stdlib matching. This is the shared lib's known hand-rolled-matching gap: a surface pattern with `**` silently matches too narrowly.

## Candidates scored

Scored on four criteria: **durability** (design maturity, test coverage, breaking-change history), **active maintenance** (recent commits/releases, issue responsiveness), **license fit** (per `datadog-oss-license-policy`), **community adoption** (stars, known importers — a proxy for battle-testing). Scale: ✅ strong, ⚠️ acceptable, ❌ weak. Data pulled 2026-07-15 from the Go module proxy and GitHub API.

| Library | Durability | Active maintenance | License fit | Adoption | Verdict |
| --- | --- | --- | --- | --- | --- |
| **`bmatcuk/doublestar/v4`** | ✅ v4 is a from-scratch rewrite (2021) purpose-built for `**`/globstar semantics on `io/fs`; 10 minor releases since (`v4.1`–`v4.10.0`), no breaking changes within v4 | ✅ latest release `v4.10.0` (2026-01-25), last push 2026-05-17 (~2 months old); 14 open issues/PRs, actively triaged | ✅ MIT — pre-approved, no OSRB review | ✅ 713 GitHub stars; ~900 direct importers on pkg.go.dev | **Pinned** |
| `gobwas/glob` | ⚠️ mature, precompiles to a state machine (fast), but `**` support is a documented compatibility mode, not the library's core design | ❌ latest tag `v0.2.3` (2018), last repo push 2024-01-28 — no commits in ~2.5 years | ✅ MIT | ⚠️ 1,022 stars (higher than doublestar) but adoption skews toward pre-2018 projects; ongoing new adoption weaker given the maintenance gap | Rejected — maintenance stalled |
| `mattn/go-zglob` | ⚠️ thin globbing layer, less test surface than doublestar, no `PathMatch`/OS-separator-aware API | ✅ latest `v0.0.6` (2024-09), last push 2026-04-13 — maintained but low release cadence (still `v0.0.x` after years) | ✅ MIT | ⚠️ 201 stars — meaningfully smaller footprint than doublestar | Rejected — thinner API, smaller adoption |
| `yargevad/filepathx` | ❌ a small wrapper that expands `**` by walking the tree with regexp before delegating to `filepath.Glob` per expansion — not a general matcher, no `Match`-only (no-filesystem-walk) API | ⚠️ last push 2023-02; 5 open issues — effectively dormant | ✅ MIT | ❌ 37 stars — smallest of the four | Rejected — thin, dormant, wrong shape for a `Match`-style verifier |

## Why doublestar over the field

- Only candidate combining an actively maintained release cadence (release within the last ~6 months) with a design purpose-built for globstar semantics — the other three either stalled (`gobwas/glob`, `yargevad/filepathx`) or are a much thinner, less-adopted implementation (`mattn/go-zglob`).
- Ships both `Match`/`PathMatch` (no filesystem access — what a file-surface **verifier** needs, since it classifies declared patterns, not just expands globs on disk) and `Glob`/`GlobWalk` (filesystem expansion) off `io/fs`, so it covers both a static allow/deny check and any future filesystem-walk need from one dependency.
- Highest adoption among actively maintained candidates (~900 pkg.go.dev importers), meaning the globstar edge cases the harness cares about (`dir/**`, `**/name*`, `**` alone) are exercised at far larger scale than any alternative here.

## `**`/`dir/**` proof

Go's `filepath.Match` fails on `dir/**`; doublestar's `Match` (same call shape, drop-in style) succeeds:

```go
import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// stdlib: false — filepath.Match treats "**" as a single "*", which cannot
// cross the "/" between "dir" and "sub".
stdOK, _ := filepath.Match("dir/**", "dir/sub/file.go")

// doublestar: true — "**" recurses across path separators.
dsOK, _ := doublestar.Match("dir/**", "dir/sub/file.go")

// doublestar: true — "**" at the front of the pattern also recurses.
dsOK2, _ := doublestar.Match("**/child*", "grandparent/parent/child1")
```

Verified by compiling and running this against a `go.mod` requiring `github.com/bmatcuk/doublestar/v4 v4.10.0`: `filepath.Match` returns `false`, both `doublestar.Match` calls return `true`.

## License clearance

- **License:** MIT (confirmed from the tagged `v4.10.0` `LICENSE` file, copyright Bob Matcuk).
- Per `datadog-oss-license-policy`: MIT is **pre-approved** (no OSRB review) — the only standing obligation is to retain the copyright and permission notice and record the component.
- **Tracking requirement (a consuming change, not this spike):** record in `claude-tooling/LICENSE-3rdparty.csv` (component `doublestar`, origin `https://github.com/bmatcuk/doublestar`, license `MIT`, copyright `Bob Matcuk`). If `go/bh` ships inside the repo's distributed signed binary, the MIT permission/license text must also travel with that binary — reproduce it in `THIRD-PARTY-LICENSES.md` per the repo's existing attribution practice. The consuming change must confirm the distribution status of the Go surface and satisfy whichever obligation applies.

## Consuming this decision

- The consuming change adopts `github.com/bmatcuk/doublestar/v4 v4.10.0` in `go/build-helpers/bh/surface.go`, adds the `require` line + `go.sum` entries above to `go/build-helpers/go.mod`/`go/build-helpers/go.sum`, and adds the `LICENSE-3rdparty.csv` row (plus the `THIRD-PARTY-LICENSES.md` entry if distributed).
- No other capability spike (JSON-schema validation, diffing, semver, CLI parsing) is decided by this doc — each gets its own dependency-selection spike.
