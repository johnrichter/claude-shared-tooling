# Dependency spike — cgo-free SQLite driver for the cost store

## Decision

**Pin `modernc.org/sqlite` at `v1.54.0`.**

```
require modernc.org/sqlite v1.54.0
```

```
modernc.org/sqlite v1.54.0 h1:JCxR4qwkJvOaqAoYcgDoO25Nc+ROg6EJ2LfBVzdrgog=
modernc.org/sqlite v1.54.0/go.mod h1:4ntCLuNmnH8+GNqjka1wNg7KJd5/Hi5FYp8K+XQ7GZw=
```

**Application site:** `go/cost` — `Store` persists cost events, watermarks, and dated rate
history in a `database/sql`-backed SQLite file, queried via `Query`/`Rollup`.

## Problem

`go/cost` needs a real queryable index (filtered/grouped reads across session, project, agent,
tool, error), not a flat file — but the shared-lib's Go binaries are built and cross-compiled
without a C toolchain in the loop. A SQLite driver that requires cgo (the traditional choice)
would reintroduce a C build dependency this repo's Go tooling doesn't otherwise carry, breaking
straightforward cross-compilation and slowing every build that links `go/cost`.

## Candidates scored

Scored on four criteria: **durability** (design maturity, transpilation/rewrite risk, breaking-
change history), **active maintenance** (recent releases, issue responsiveness), **license fit**
(per the project's OSS license policy), **community adoption** (pkg.go.dev importers — a proxy
for battle-testing). Scale: ✅ strong, ⚠️ acceptable, ❌ weak. Data pulled 2026-07-26 from the Go
module proxy, pkg.go.dev, and `cvilsmeier/go-sqlite-bench`'s March 2026 benchmark run.

| Library | Durability | Active maintenance | License fit | Adoption | Verdict |
| --- | --- | --- | --- | --- | --- |
| **`modernc.org/sqlite`** | ✅ transpiles the upstream SQLite C amalgamation to Go via the same project's `ccgo`, rather than a from-scratch reimplementation — inherits SQLite's own decades of correctness testing instead of reproducing it | ✅ latest release `v1.54.0` (2026-07-15); actively cut releases tracking upstream SQLite versions | ✅ BSD-3-Clause (Go wrapper) + public-domain SQLite core — no copyleft, no cgo build-time obligation | ✅ ~3,500 pkg.go.dev importers; the de facto standard cgo-free `database/sql` driver | **Pinned** |
| `mattn/go-sqlite3` | ✅ wraps the official SQLite C library directly — maximum feature parity and, per `go-sqlite-bench`, generally the fastest at concurrency | ✅ actively maintained, the long-standing default choice | ✅ MIT | ✅ highest adoption of any Go SQLite driver | Rejected — requires cgo; reintroduces a C toolchain into every build/cross-compile of a binary linking `go/cost` |
| `zombiezen.com/go/sqlite` | ✅ cgo-free (built on the same modernc libraries), and per `go-sqlite-bench` the fastest of the three on raw query throughput | ✅ actively maintained | ✅ ISC | ⚠️ meaningfully smaller adoption than modernc.org/sqlite | Rejected — does not implement `database/sql`; its custom low-level API would mean `go/cost`'s `Store` couldn't use the standard `sql.DB` connection pooling, `Tx`, and driver-agnostic query helpers this package's `Ingest`/`Query`/`Rollup` are built on |

## Why `modernc.org/sqlite` over the field

- Only cgo-free candidate that implements `database/sql`, so `go/cost`'s `Store` gets standard
  connection pooling, transactions (`Ingest`'s per-file `*sql.Tx`), and prepared-statement query
  helpers for free — no custom driver-specific API to wrap.
- Transpiles the actual upstream SQLite C source rather than reimplementing SQL semantics from
  scratch, so correctness tracks upstream SQLite's own test suite instead of a second
  independent implementation's bug surface.
- Far higher adoption than the other cgo-free option (`zombiezen.com/go/sqlite`), meaning the
  transpilation/driver edge cases a cost-accounting store depends on (transaction semantics,
  `UNIQUE`/`ON CONFLICT`, concurrent-writer busy-timeout handling) are exercised at much larger
  scale.
- `mattn/go-sqlite3`'s cgo requirement is the one disqualifying factor, independent of its
  otherwise-strong maturity and raw performance: this repo's Go binaries are built and cross-
  compiled without a C toolchain in the loop, and a cgo dependency would break that for every
  binary linking `go/cost`.

## License clearance

- **License:** BSD-3-Clause (Go wrapper/driver code) — confirmed by fetching the `v1.54.0` tag's
  `LICENSE` file. Copyright: The Sqlite Authors.
- The module also bundles the SQLite C amalgamation it transpiles, unchanged, alongside its own
  `SQLITE-LICENSE` file: SQLite's authors have dedicated the engine itself to the public domain,
  so it carries no separate license obligation beyond what's already recorded here.
- Transitive dependencies pulled in by `go mod tidy` (`modernc.org/libc`, `modernc.org/mathutil`,
  `modernc.org/memory`, `github.com/google/uuid`, `github.com/remyoudompheng/bigfft`,
  `github.com/ncruces/go-strftime`, `github.com/dustin/go-humanize`) were each confirmed
  individually from their own tagged `LICENSE` files: all BSD-3-Clause or MIT, no copyleft.
- Obligation: retain copyright/permission notice in any distributed binary. Satisfied by the new
  `LICENSE-3rdparty.csv` rows and `THIRD-PARTY-LICENSES.md` sections for `go/cost`'s dependency
  set.

## Consuming this decision

- `go/cost/go.mod` requires `modernc.org/sqlite v1.54.0`, plus the trimmed transitive set above
  (`go mod tidy` resolved this without pulling in `modernc.org/sqlite`'s own test-only/build-time
  dependencies like `modernc.org/cc/v4`/`modernc.org/ccgo/v4`, which a consumer never imports).
- `LICENSE-3rdparty.csv` gets one row per new component; `THIRD-PARTY-LICENSES.md` gets a
  `go/cost` line in the Scope section plus BSD-3-Clause/MIT license-text sections for the new
  components, grouped by identical license text per the file's existing convention.
- No other capability spike (ORM, migration tooling, connection-pool tuning) is decided by this
  doc — `go/cost` uses `database/sql` directly with no additional layer.
