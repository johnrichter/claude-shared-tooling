//! The ranked-result type shared by every BM25 variant this crate exposes.
//!
//! `ScoredDocument` used to live in [`crate::okapi`] and be re-borrowed by
//! [`crate::bm25f`] (`bm25f -> okapi` for a type that is neither variant's
//! private concern). Promoted here at `M1.P2.T3` — the task that defines
//! this crate's public API surface — so both variants (and any future one,
//! e.g. the M4 "search JSON hit" pin) depend on a neutral, variant-agnostic
//! module instead of on each other.

/// A ranked search result: one document's id and its BM25 score against the
/// query that produced it.
///
/// Returned by [`crate::okapi::OkapiIndex::search`] and
/// [`crate::bm25f::BM25FIndex::search`] — both variants share this exact
/// type, so a caller that switches variants (or ranks results from both
/// side by side) never has to convert between two lookalike structs.
#[derive(Debug, Clone, PartialEq)]
pub struct ScoredDocument {
    /// The document's caller-supplied identifier (`Document::id` on
    /// whichever variant produced this result).
    pub id: String,
    /// The BM25 score. Always finite for a well-formed index/query (no
    /// `NaN`/`inf`) — both variants guarantee this at their zero-length-
    /// corpus and zero-score-filter edge cases.
    pub score: f64,
}
