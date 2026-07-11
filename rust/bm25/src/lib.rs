//! `bm25` — general-purpose, call-time-customizable BM25 ranking library.
//!
//! Scope: pure ranking math over caller-supplied term statistics. No ML, no
//! embeddings, no network I/O, no filesystem access — every call is a
//! deterministic function of its inputs, so identical inputs always produce
//! identical outputs (bit-for-bit) regardless of platform, thread count, or
//! call order.
//!
//! Inputs (land in M1.P1.T2 / M1.P2.*): per-document term frequencies, a
//! corpus-wide document-frequency table, document lengths, and caller-tunable
//! `k1`/`b` parameters — never a hardcoded corpus or a global mutable index.
//! Outputs: a BM25 score per (query, document) pair, or a ranked document
//! list. Invariants the real implementation must uphold: pure functions (no
//! hidden state across calls), no panics on malformed-but-well-typed input
//! (empty query, empty corpus, zero-length document all return a defined
//! result, not a panic), and no floating-point behavior that varies by
//! target arch (musl vs glibc, `x86_64` vs aarch64) — this is why the crate is
//! pure Rust with no C dependency: a C dependency's libm can vary output by
//! platform, which would break the determinism contract.
//!
//! Tokenization is a caller/adapter concern, not this crate's: `bm25` never
//! decides what a "term" is. Callers pre-tokenize and hand this crate term
//! statistics.
//!
//! This file is the M1.P1.T1 scaffold only — no scoring or tokenization
//! logic yet. It exists to prove the crate compiles, lints clean, and tests
//! green inside the workspace, ahead of the real implementation.

#![deny(unsafe_code)]

/// Returns this crate's semantic version, read from `Cargo.toml` at compile
/// time via `env!("CARGO_PKG_VERSION")`. Placeholder public surface for the
/// M1.P1.T1 scaffold — exercised by the crate's one scaffold-era test.
///
/// # M1.P1.T2
/// Real BM25 scoring entry points (e.g. `score`, `Bm25Params`, a document/
/// corpus statistics type) land here, replacing/joining this placeholder.
///
/// # M1.P2.*
/// Tokenization-adapter traits and any batch/streaming scoring API land
/// alongside the M1.P1.T2 scoring core.
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
