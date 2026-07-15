# rust — deterministic shared machinery (Cargo workspace)

Sensitivity: **public** · Owner: **public** · Kind: **shared Rust workspace**

The platform's first Rust workspace in this repo. Isolated under `rust/` so it never entangles
the Python package (`ai_shared_lib_public/`) — separate `Cargo.toml`, separate build/test/lint
lifecycle. Holds only deterministic, no-ML machinery.

## What lives here

- **`bm25/`** — general-purpose, call-time-customizable BM25 ranking library. Complete as of the
  M1 capstone (M1.P2.T3): both scoring variants (`OkapiIndex` flat, `BM25FIndex` fielded) and both
  tokenizers (`Tokenizer::CaseSplit` default, `Tokenizer::WholeIdentifier`) are selectable at call
  time; the tokenizer is injected at `build` and reused at `search` so build/search agreement is
  structural. Deterministic, no ML. See `bm25/src/lib.rs` for the public-API surface and example.
- **`frontmatter/`** — the one YAML-frontmatter-plus-Markdown-body parser for navigator, plus a
  `validate` module that interprets the declarative schema in `schemas/frontmatter/` (core
  profile + extension pack) against a parsed file. Every navigator subcommand consumes this
  crate rather than each owning its own parse implementation. See `frontmatter/src/lib.rs`.
- **`facetquery/`** — a pure, frontmatter-agnostic boolean facet-query language (`facetquery@1`):
  parses a query string to an AST and evaluates it against any generic facet source. Normative
  spec in `schemas/facetquery/`; `frontmatter/` depends on this crate so a parsed query matches
  against many files. See `facetquery/src/lib.rs`.
- (forward-looking) a tree-sitter-based symbol-extraction crate joins as a fourth workspace
  member in a later navigator milestone.

## Dependency policy

For a non-trivial common capability (parsing, serialization, regex, and the like), **prefer a
well-maintained, robust, trusted crate over hand-rolling it** — same posture as the Python
package: robustness and correctness over minimizing dependency count. Vet every material
crate choice robustness-first (maintenance cadence, adoption, security-advisory history), clear
its license per the org policy, and pin the exact version.

That preference is layered under the build constraint, not replaced by it: every member crate must
stay **pure Rust** — no CGO, no C dependency — so it stays buildable as a static `musl` binary
later (a binary-target concern; kept true of the library now so it is never retrofitted). A
candidate crate that pulls in a C dependency fails the build-fit check regardless of how well it
vets otherwise. Hand-roll only when no crate clears vetting, license, and the pure-Rust constraint
together — flag it, state why, and get sign-off.

## Lints

Workspace-wide lints live once in `rust/Cargo.toml` (`[workspace.lints]`) and every member opts in
via `[lints] workspace = true` — never repeated per-crate. `unsafe_code` is forbidden workspace-
wide; `clippy::all` + `clippy::pedantic` are warned locally (fast local dev loop) and enforced as
hard failures in CI via `-D warnings` (see `.github/workflows/ci.yml`), not as a crate-level `#![deny(...)]`
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

`.github/workflows/ci.yml` runs the above commands per member crate across the
`{linux, macos} x {x86_64, aarch64}` arch matrix (cross-compiled where a runner can't execute the
target natively) — see the `rust-*` jobs. Independent of the existing Python/guardrail jobs.
