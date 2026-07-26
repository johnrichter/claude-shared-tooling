# Claude Shared Tooling

Small, focused command-line tools for working with git and web content, plus a home for reusable schema and format definitions.
Each tool is stdlib-only today and doubles as an importable module for programmatic use.

## What's here

- **CLI tools** — three small utilities, each also usable as a library (see [Tools](#tools)).
- **`schemas/`** — generalized, non-proprietary schema and format definitions others can adopt (see `schemas/README.md`).
- **`rust/`** — a Cargo workspace of deterministic, no-ML Rust crates (see [Rust workspace](#rust-workspace)).
- **`conformance/`** — cross-language byte-identity gates for the contracts in `schemas/` that more than one language implements: shared inputs, recorded goldens, and a runner that fails on any difference between implementations (`conformance/logkit/README.md`, `conformance/clikit/README.md`).

## Tools

| Command | Purpose |
| --- | --- |
| `resign-commits` | Conflict-proof re-signing of unsigned commits on a git ref via `git commit-tree` (reuses each exact tree; preserves merge topology; dry-run by default). |
| `sitemap-parser` | Fetch, parse, window-filter, and prefix-filter any XML sitemap (`urlset` or one-level `sitemapindex`); fail-open to `[]`. |
| `article-meta` | Deterministically extract `{title, published, excerpt}` from a page's structured metadata (no LLM; verbatim-or-null). |

## Rust workspace

`rust/` is a separate Cargo workspace, isolated from the Python package so it never entangles the
Python build/test/lint lifecycle. Every member is pure Rust (no C dependency, `unsafe_code`
forbidden workspace-wide) so it stays buildable as a static `musl` binary. None of the crates are
published (`publish = false`); each is a library consumed by an external binary that vendors this
repo — for example `frontmatter` and `facetquery` are designed to be embedded in the **navigator**
binary at build time. Detail: `rust/README.md`.

| Crate | Purpose |
| --- | --- |
| `bm25` | General-purpose, call-time-customizable BM25 ranking library — flat (`OkapiIndex`) and fielded (`BM25FIndex`) scoring, pluggable tokenizer, deterministic (bit-for-bit identical output for identical input). |
| `frontmatter` | The one YAML-frontmatter-plus-Markdown-body parser for navigator, plus a `validate` module that interprets the declarative schema in `schemas/frontmatter/` (core profile + extension pack) against a parsed file. |
| `facetquery` | Frontmatter-agnostic boolean facet-query language (`facetquery@1`) — parses a query string to an AST and evaluates it against any generic facet source; spec lives in `schemas/facetquery/`. |

`frontmatter` depends on `facetquery` (a query, once parsed, matches against many files' frontmatter) — the only inter-crate dependency in the workspace today. A tree-sitter-based symbol-extraction crate is expected to join as a fourth member in a later navigator milestone.

## Install (dev)

```
uv venv && uv pip install -e .
python -m unittest discover -s tests -p "test_*.py"
```

Requires Python >= 3.10. Stdlib-only in fact today — no third-party packages to install.

### Rust

```
cd rust
cargo fmt --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test
```

Requires a current stable Rust toolchain. CI runs the same commands per member crate across the
`{linux, macos} x {x86_64, aarch64}` matrix — see the `rust-*` jobs in `.github/workflows/ci.yml`.

## Dependency policy

Stdlib stays correct for what it covers well — a one-line string op, a built-in already fit for purpose. For a **non-trivial common capability** (CLI/argument parsing, YAML, JSON, regex, globbing, math, HTTP, date/time, and the like), **prefer a well-maintained, robust, trusted library over hand-rolling it** — never reinvent the wheel when a trusted, actively-maintained library exists. Fewer dependencies is not a goal in itself; robustness and correctness are.

Every material dependency choice runs a **robustness-first vetting** (maintenance cadence, adoption, security-advisory history, license fit) before adoption, clears an **OSS-license** check, is **pinned** to an exact version, and is **recorded in `LICENSE-3rdparty.csv`** in the same change. Among viable candidates, prefer **official / first-party / actively-maintained** libraries; flag supply-chain risk and get sign-off before introducing untrusted code.

Hand-rolling a common capability is the narrow exception — only when no trusted library fits (vetting bar, license, or a hard build-fit constraint) — and requires flagging the rejected candidates, stating the rationale, and getting sign-off.

The top-level `claude-tooling` package stays stdlib-only; `tooling/codegov-lint` is the one
exception today — it pins `ruff` (see its own `requirements.txt` and `LICENSE-3rdparty.csv`)
as the trusted implementation of its Python docstring check, installed into an isolated venv
rather than any shared interpreter.

## Guardrails

A pre-commit hook plus required CI checks gate every merge:

- **Local pre-commit hook** — `git config core.hooksPath .githooks` (convenience; bypassable).
- **CI required check (secrets)** — `.github/workflows/ci.yml` runs the guardrail script + the test suite (the authority).
- **CI required check (SC-CODEGOV)** — `.github/workflows/codegov.yml` runs `tooling/codegov-lint` against every task's diff; see `tooling/codegov-lint/README.md`.

## Versioning

Semver git tags (`vX.Y.Z`); consumers pin a tag. Bump on every release so updates propagate.

### Go workspace (`go/`)

Versioned independently of the top-level `vX.Y.Z` line, using Go's subdirectory-module tag
convention: a tag for a module rooted below the repo root is prefixed with that path, so a
`go/` release is tagged `go/vX.Y.Z` (e.g. `go/v0.1.0`) — never a bare `vX.Y.Z`, which is reserved
for the top-level Python package. Bump on every release that changes `go/` behavior (source or a
recompiled `.bin/` binary); tag from a commit where `go/` is in its released state.

### Changelog

- **v0.2.0** — Dependency policy reframed from "dependency-free / Tier 0" to **stdlib-preferred** (justified vendored deps permitted, subject to OSS-license clearance). No code change: `dependencies` stays empty, every module remains stdlib-only in fact. Docs-only contract clarification.
- **v0.1.0** — Initial release: `resign_commits`, `sitemap_parser`, `article_meta` tools + schema home + secret-scanning guardrail.

## License

MIT — see [`LICENSE`](LICENSE).
Dependency attributions live in [`THIRD-PARTY-LICENSES.md`](THIRD-PARTY-LICENSES.md).

## Claude Code setup

This repo's `.claude/settings.json` enables plugins from the `jr-claude-plugins` marketplace. Register it once at the Claude user level — repo settings carry no machine-specific paths:

```sh
claude plugin marketplace add git@github.com:johnrichter/claude-marketplace.git
# or, with the psa-platform repos checked out as siblings:
claude plugin marketplace add ../marketplace-public
```

Knowledge bases are configured at the Claude user level, not per repo.
