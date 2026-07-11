//! `bm25` — general-purpose, call-time-customizable BM25 ranking library.
//!
//! Scope: pure ranking math over document/query text. No ML, no embeddings,
//! no network I/O, no filesystem access — every call is a deterministic
//! function of its inputs, so identical inputs always produce identical
//! outputs (bit-for-bit) regardless of platform, thread count, or call
//! order. No floating-point behavior varies by target arch (musl vs glibc,
//! `x86_64` vs aarch64) — this is why the crate is pure Rust with no C
//! dependency: a C dependency's libm can vary output by platform, which
//! would break the determinism contract.
//!
//! # What's here (`M1.P1.T2` — the seed)
//! - [`tokenize::tokenize_whole_identifier`] — the whole-identifier
//!   tokenizer (`[a-z0-9_]+`, lowercased), ported from ka. This IS this
//!   crate's tokenizer, not a caller/adapter concern: `bm25` owns
//!   tokenization so a query and an indexed document always tokenize
//!   identically.
//! - [`okapi::OkapiIndex`] — the classic flat, single-bag-of-tokens-per-
//!   document Okapi scorer (`K1 = 1.5`, `B = 0.75`), also ported from ka.
//!
//! Both are faithful ports of ka's `retrieve::bm25` — same constants, same
//! formula, same tokenizer regex — generalized only enough to be a
//! freestanding library (public types, caller-supplied string ids,
//! guaranteed-deterministic ranked output) rather than ka's internal,
//! `usize`-row-keyed, `HashMap`-order-dependent original.
//!
//! # M1.P2.T1 (landed)
//! [`bm25f::BM25FIndex`] — the fielded, per-field-weighted scorer (multi-
//! field documents, configurable field weight + length-normalization `b`
//! via [`bm25f::BM25FConfig`]). New work, not a rewrite: the flat
//! [`okapi::OkapiIndex`] is untouched. Shares the tokenizer seam
//! (`crate::tokenize`) and the ranked-output shape
//! ([`okapi::ScoredDocument`]) with `okapi`, per the plan.
//!
//! # M1.P2.T2 (landed)
//! [`tokenize::tokenize_all_case_split`] — the all-case-splitting tokenizer
//! mode (`camelCase`/`snake_case`/`TitleCase`/`PascalCase`/`SCREAMING_SNAKE`/
//! kebab -> sub-tokens, e.g. `ddTrace`/`dd_trace`/`DD_TRACE`/`DdTrace` all ->
//! `["dd", "trace"]`). A sibling free function to
//! [`tokenize::tokenize_whole_identifier`], same `fn(&str) -> Vec<String>`
//! signature, on purpose — see `M1.P2.T3` below.
//!
//! # M1.P2.T3
//! A call-time selection API (variant `{Okapi | BM25F}` x tokenizer
//! `{whole-identifier | case-splitting}`) lands here, composing the two
//! variants and two tokenizer modes without either implementation knowing
//! about selection. Both tokenizers already share one `fn(&str) ->
//! Vec<String>` signature (per `M1.P2.T2`) specifically so this task can
//! hold either behind one function-pointer/closure type and inject it into
//! `okapi`/`bm25f` uniformly — `okapi`/`bm25f` currently hardcode
//! [`tokenize::tokenize_whole_identifier`] (flagged by the M1.P2.T1 QR);
//! this task replaces that hardcoding with the injected tokenizer.

#![deny(unsafe_code)]

pub mod bm25f;
pub mod okapi;
pub mod tokenize;

/// Returns this crate's semantic version, read from `Cargo.toml` at compile
/// time via `env!("CARGO_PKG_VERSION")`.
#[must_use]
pub fn version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Hermetic, deterministic: reads only `CARGO_PKG_VERSION`, set at
    /// compile time from this crate's own `Cargo.toml` — no environment,
    /// filesystem, network, or clock dependency, so the assertion holds on
    /// every platform in the CI arch matrix.
    #[test]
    fn version_matches_cargo_toml() {
        assert_eq!(version(), "0.1.0");
    }
}
