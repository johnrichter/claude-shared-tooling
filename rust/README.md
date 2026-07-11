# rust — deterministic shared machinery (Cargo workspace)

Sensitivity: **public** · Owner: **public** · Kind: **shared Rust workspace**

The platform's first Rust workspace in this repo. Isolated under `rust/` so it never entangles
the Go module (`go/`) or the Python package (`ai_shared_lib_public/`) — separate `Cargo.toml`,
separate build/test/lint lifecycle. Holds only deterministic, no-ML machinery.

## What lives here

- **`bm25/`** — general-purpose, call-time-customizable BM25 ranking library. Complete as of the
  M1 capstone (M1.P2.T3): both scoring variants (`OkapiIndex` flat, `BM25FIndex` fielded) and both
  tokenizers (`Tokenizer::CaseSplit` default, `Tokenizer::WholeIdentifier`) are selectable at call
  time; the tokenizer is injected at `build` and reused at `search` so build/search agreement is
  structural. Deterministic, no ML. See `bm25/src/lib.rs` for the public-API surface and example.
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
RUSTDOCFLAGS="-D warnings" cargo doc --no-deps
cargo test
```

## CI

`.gitlab-ci.yml` runs the above commands per member crate across the
`{linux, macos} x {x86_64, aarch64}` arch matrix (cross-compiled where a runner can't execute the
target natively) — see the `rust-*` jobs. Independent of the existing Go/Python/guardrail jobs.
