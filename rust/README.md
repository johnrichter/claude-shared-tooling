# rust — deterministic shared machinery (Cargo workspace)

Sensitivity: **public** · Owner: **public** · Kind: **shared Rust workspace**

The platform's first Rust workspace in this repo. Isolated under `rust/` so it never entangles
the Go module (`go/`) or the Python package (`ai_shared_lib_public/`) — separate `Cargo.toml`,
separate build/test/lint lifecycle. Holds only deterministic, no-ML machinery.

## What lives here

- **`bm25/`** — general-purpose, call-time-customizable BM25 ranking library. Scaffold only as of
  M1.P1.T1 (compiles, lints clean, one placeholder test); scoring + tokenization land in
  M1.P1.T2 / M1.P2.* (see `bm25/src/lib.rs` for exact markers).
- (forward-looking) a tree-sitter-based symbol-extraction crate joins as a second workspace
  member in a later navigator milestone.

## Dependency policy

Pure Rust only — no CGO, no C dependency, no non-first-party heavy dependency — so every member
crate stays buildable as a static `musl` binary later (a binary-target concern; kept true of the
library now so it is never retrofitted). A justified third-party dependency is acceptable subject
to OSS-license clearance per the org policy, same as the Go module and Python package.

## Lints

Workspace-wide lints live once in `rust/Cargo.toml` (`[workspace.lints]`) and every member opts in
via `[lints] workspace = true` — never repeated per-crate. `unsafe_code` is forbidden workspace-
wide; `clippy::all` + `clippy::pedantic` are warned locally (fast local dev loop) and enforced as
hard failures in CI via `-D warnings` (see `.gitlab-ci.yml`), not as a crate-level `#![deny(...)]`
that would fight local iteration.

## Development

```sh
cd rust
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test
```

## CI

`.gitlab-ci.yml` runs the above three commands per member crate across the
`{linux, macos} x {x86_64, aarch64}` arch matrix (cross-compiled where a runner can't execute the
target natively) — see the `rust-*` jobs. Independent of the existing Go/Python/guardrail jobs.
