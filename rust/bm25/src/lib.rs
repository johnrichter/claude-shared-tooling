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
//! - [`tokenize_whole_identifier`] — the whole-identifier tokenizer
//!   (`[a-z0-9_]+`, lowercased), ported from ka. This IS this crate's
//!   tokenizer, not a caller/adapter concern: `bm25` owns tokenization so a
//!   query and an indexed document always tokenize identically.
//! - [`OkapiIndex`] — the classic flat, single-bag-of-tokens-per-document
//!   Okapi scorer (`K1 = 1.5`, `B = 0.75`), also ported from ka.
//!
//! Both are faithful ports of ka's `retrieve::bm25` — same constants, same
//! formula, same tokenizer regex — generalized only enough to be a
//! freestanding library (public types, caller-supplied string ids,
//! guaranteed-deterministic ranked output) rather than ka's internal,
//! `usize`-row-keyed, `HashMap`-order-dependent original.
//!
//! # M1.P2.T1 (landed)
//! [`BM25FIndex`] — the fielded, per-field-weighted scorer (multi-field
//! documents, configurable field weight + length-normalization `b` via
//! [`BM25FConfig`]). New work, not a rewrite: the flat [`OkapiIndex`] is
//! untouched. Shares the tokenizer seam (`crate::tokenize`) and the
//! ranked-output shape ([`ScoredDocument`]) with `okapi`, per the plan.
//!
//! # M1.P2.T2 (landed)
//! [`tokenize_all_case_split`] — the all-case-splitting tokenizer mode
//! (`camelCase`/`snake_case`/`TitleCase`/`PascalCase`/`SCREAMING_SNAKE`/
//! kebab -> sub-tokens, e.g. `ddTrace`/`dd_trace`/`DD_TRACE`/`DdTrace` all ->
//! `["dd", "trace"]`). A sibling free function to [`tokenize_whole_identifier`],
//! same `fn(&str) -> Vec<String>` signature, on purpose — this is what let
//! `M1.P2.T3` hold either behind one closed enum.
//!
//! # M1.P2.T3 (landed) — call-time variant x tokenizer selection
//! A caller picks the scoring variant AND the tokenizer independently, at
//! the call site that builds an index:
//! - **Variant** — [`OkapiIndex`] (flat, one text per document) or
//!   [`BM25FIndex`] (fielded, per-field-weighted; needs a [`BM25FConfig`]).
//!   The two variants keep their own `Document` shape ([`OkapiDocument`] vs.
//!   [`BM25FDocument`]) — deliberately NOT unified into one lossy type,
//!   because Okapi's "one text" and BM25F's "named fields" aren't the same
//!   shape and forcing them together would either lose BM25F's per-field
//!   structure or force Okapi callers to wrap a single text in a fake field.
//! - **Tokenizer** — [`Tokenizer::CaseSplit`] (navigator's default) or
//!   [`Tokenizer::WholeIdentifier`] (exact-symbol opt-in), passed to either
//!   variant's `build`.
//!
//! Both axes are independent and orthogonal: any tokenizer works with either
//! variant. The index OWNS the [`Tokenizer`] it was built with and reuses it
//! for every `search` call — there is no API to search with a different
//! tokenizer than the index was built with, so build/search tokenizer
//! agreement (an M1.P2.T1 QR finding: a mismatch silently breaks retrieval)
//! is a structural guarantee, not caller discipline.
//!
//! ```
//! use bm25::{BM25FConfig, BM25FDocument, BM25FIndex, OkapiDocument, OkapiIndex, Tokenizer};
//!
//! // Variant: Okapi (flat). Tokenizer: case-splitting (navigator's default).
//! let okapi_index = OkapiIndex::build(
//!     Tokenizer::CaseSplit,
//!     [OkapiDocument { id: "doc-1", text: "ddTrace span init" }],
//! );
//! assert_eq!(okapi_index.search("trace", 10).len(), 1);
//!
//! // Variant: BM25F (fielded). Tokenizer: whole-identifier (exact-symbol).
//! let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
//! let bm25f_index = BM25FIndex::build(
//!     &config,
//!     Tokenizer::WholeIdentifier,
//!     [BM25FDocument { id: "doc-1", fields: vec![("body", "dd_trace span")] }],
//! );
//! assert_eq!(bm25f_index.search("dd_trace", 10).len(), 1);
//! ```
//!
//! `ScoredDocument`, this crate's one ranked-result type, moved from
//! `crate::okapi` to the neutral [`crate::result`] module in this task —
//! both variants return it, so neither should have "owned" it.

#![deny(unsafe_code)]

pub mod bm25f;
pub mod okapi;
pub mod result;
pub mod tokenize;

// Flat re-exports: a caller writes `use bm25::{OkapiIndex, BM25FIndex, ...}`
// without reaching into `bm25::okapi`/`bm25::bm25f`/`bm25::result`/
// `bm25::tokenize` directly. `Document` is renamed per-variant on the way
// out (`OkapiDocument`/`BM25FDocument`) since the two variants each have
// their own `Document` type with a different field shape — re-exporting both
// under the bare name `Document` would collide.
pub use bm25f::{BM25FConfig, BM25FIndex, Document as BM25FDocument, FieldWeight};
pub use okapi::{Document as OkapiDocument, OkapiIndex};
pub use result::ScoredDocument;
pub use tokenize::{tokenize_all_case_split, tokenize_whole_identifier, Tokenizer};

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

/// M1.P2.T3 acceptance: the public, call-time-selectable API — both
/// variants x both tokenizers, exercised ONLY through `bm25::` (no reach
/// into `bm25::okapi`/`bm25::bm25f`/`bm25::tokenize` internals), proving the
/// crate is linkable and usable standalone (no navigator dependency).
#[cfg(test)]
mod public_api_tests {
    use super::{BM25FConfig, BM25FDocument, BM25FIndex, OkapiDocument, OkapiIndex, Tokenizer};

    /// All FOUR {variant} x {tokenizer} combinations, each proving a
    /// sensible ranked result comes back through the public API alone.
    #[test]
    fn okapi_whole_identifier_combination_ranks_a_match() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                OkapiDocument {
                    id: "match",
                    text: "dd_trace span init",
                },
                OkapiDocument {
                    id: "no_match",
                    text: "context handler retry",
                },
            ],
        );
        let results = index.search("dd_trace", 10);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].id, "match");
        assert!(results[0].score > 0.0);
    }

    #[test]
    fn okapi_case_split_combination_ranks_a_match() {
        let index = OkapiIndex::build(
            Tokenizer::CaseSplit,
            [
                OkapiDocument {
                    id: "match",
                    text: "ddTrace span init",
                },
                OkapiDocument {
                    id: "no_match",
                    text: "context handler retry",
                },
            ],
        );
        let results = index.search("trace", 10);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].id, "match");
        assert!(results[0].score > 0.0);
    }

    #[test]
    fn bm25f_whole_identifier_combination_ranks_a_match() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            Tokenizer::WholeIdentifier,
            [
                BM25FDocument {
                    id: "match",
                    fields: vec![("body", "dd_trace span init")],
                },
                BM25FDocument {
                    id: "no_match",
                    fields: vec![("body", "context handler retry")],
                },
            ],
        );
        let results = index.search("dd_trace", 10);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].id, "match");
        assert!(results[0].score > 0.0);
    }

    #[test]
    fn bm25f_case_split_combination_ranks_a_match() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            Tokenizer::CaseSplit,
            [
                BM25FDocument {
                    id: "match",
                    fields: vec![("body", "ddTrace span init")],
                },
                BM25FDocument {
                    id: "no_match",
                    fields: vec![("body", "context handler retry")],
                },
            ],
        );
        let results = index.search("trace", 10);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].id, "match");
        assert!(results[0].score > 0.0);
    }

    /// Tokenizer-choice effect: `CaseSplit` retrieves a doc that
    /// `WholeIdentifier` does NOT, for the SAME index-construction inputs —
    /// proves tokenizer selection flows through to retrieval, not just
    /// compiles. Query `trace` matches `ddTrace` only when the tokenizer
    /// splits camelCase into sub-tokens.
    #[test]
    fn case_split_retrieves_a_doc_whole_identifier_does_not() {
        let build = |tokenizer: Tokenizer| {
            OkapiIndex::build(
                tokenizer,
                [OkapiDocument {
                    id: "camel_doc",
                    text: "ddTrace span init",
                }],
            )
        };

        let case_split_results = build(Tokenizer::CaseSplit).search("trace", 10);
        assert_eq!(
            case_split_results.len(),
            1,
            "CaseSplit must split ddTrace into [\"dd\", \"trace\"], matching query \"trace\""
        );

        let whole_identifier_results = build(Tokenizer::WholeIdentifier).search("trace", 10);
        assert_eq!(
            whole_identifier_results.len(),
            0,
            "WholeIdentifier keeps ddTrace as one token (\"ddtrace\"), which never equals \
             \"trace\" -- must NOT match"
        );
    }

    /// Same effect reproduced on the fielded variant, so the tokenizer
    /// injection point is proven on both `build` call sites, not just
    /// `OkapiIndex`'s.
    #[test]
    fn case_split_retrieves_a_doc_whole_identifier_does_not_on_bm25f() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let build = |tokenizer: Tokenizer| {
            BM25FIndex::build(
                &config,
                tokenizer,
                [BM25FDocument {
                    id: "camel_doc",
                    fields: vec![("body", "ddTrace span init")],
                }],
            )
        };

        assert_eq!(build(Tokenizer::CaseSplit).search("trace", 10).len(), 1);
        assert_eq!(
            build(Tokenizer::WholeIdentifier).search("trace", 10).len(),
            0
        );
    }

    /// Build/search agreement, by construction: there is no `search`
    /// overload or parameter anywhere in the public API that accepts a
    /// `Tokenizer` — an index's tokenizer is fixed for its lifetime at
    /// `build` and `search` always reuses it. This test's assertion is
    /// really about the API shape (documented here as an executable
    /// example of the guarantee): the same index, queried twice, tokenizes
    /// both the build corpus and every query identically every time.
    #[test]
    fn search_always_reuses_the_tokenizer_the_index_was_built_with() {
        // Built with CaseSplit -- if `search` used a different tokenizer
        // internally (the mismatch this guard exists to prevent), a query
        // tokenized as one whole identifier ("ddtrace") would never match
        // postings keyed by CaseSplit's sub-tokens ("dd", "trace").
        let index = OkapiIndex::build(
            Tokenizer::CaseSplit,
            [OkapiDocument {
                id: "doc",
                text: "ddTrace",
            }],
        );
        // Repeated `search` calls against the same index are unaffected by
        // anything except the query text -- there is no way to pass a
        // different tokenizer in, so both calls tokenize "trace" as
        // CaseSplit did at build time and both find the match.
        assert_eq!(index.search("trace", 10).len(), 1);
        assert_eq!(index.search("trace", 10).len(), 1);
    }

    /// Non-trivial-corpus strengthening of the four combination tests
    /// above: those assert only "exactly one match, score > 0" against a
    /// two-doc corpus (one match, one non-match), which does not exercise
    /// ranking among multiple matching documents. These four assert the
    /// actual ranked `id` order across a THREE-doc corpus per combination
    /// (high term-frequency match, low term-frequency match, no match),
    /// same doc length across the two matches so term frequency alone
    /// drives the order -- proving `search`'s sort (score desc, id asc
    /// tie-break) is real, not incidentally correct for a single result.
    #[test]
    fn okapi_whole_identifier_combination_ranks_multiple_matches_by_score() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                OkapiDocument {
                    id: "high_tf",
                    text: "dd_trace dd_trace dd_trace filler filler filler",
                },
                OkapiDocument {
                    id: "low_tf",
                    text: "dd_trace filler filler filler filler filler",
                },
                OkapiDocument {
                    id: "no_match",
                    text: "filler filler filler filler filler filler",
                },
            ],
        );
        let results = index.search("dd_trace", 10);
        let ids: Vec<&str> = results.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(
            ids,
            vec!["high_tf", "low_tf"],
            "higher term frequency must outrank lower; non-matching doc must be excluded"
        );
        assert!(results[0].score > results[1].score);
    }

    #[test]
    fn okapi_case_split_combination_ranks_multiple_matches_by_score() {
        let index = OkapiIndex::build(
            Tokenizer::CaseSplit,
            [
                OkapiDocument {
                    id: "high_tf",
                    text: "ddTrace ddTrace ddTrace filler filler filler",
                },
                OkapiDocument {
                    id: "low_tf",
                    text: "ddTrace filler filler filler filler filler",
                },
                OkapiDocument {
                    id: "no_match",
                    text: "filler filler filler filler filler filler",
                },
            ],
        );
        let results = index.search("trace", 10);
        let ids: Vec<&str> = results.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(ids, vec!["high_tf", "low_tf"]);
        assert!(results[0].score > results[1].score);
    }

    #[test]
    fn bm25f_whole_identifier_combination_ranks_multiple_matches_by_score() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            Tokenizer::WholeIdentifier,
            [
                BM25FDocument {
                    id: "high_tf",
                    fields: vec![("body", "dd_trace dd_trace dd_trace filler filler filler")],
                },
                BM25FDocument {
                    id: "low_tf",
                    fields: vec![("body", "dd_trace filler filler filler filler filler")],
                },
                BM25FDocument {
                    id: "no_match",
                    fields: vec![("body", "filler filler filler filler filler filler")],
                },
            ],
        );
        let results = index.search("dd_trace", 10);
        let ids: Vec<&str> = results.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(ids, vec!["high_tf", "low_tf"]);
        assert!(results[0].score > results[1].score);
    }

    #[test]
    fn bm25f_case_split_combination_ranks_multiple_matches_by_score() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            Tokenizer::CaseSplit,
            [
                BM25FDocument {
                    id: "high_tf",
                    fields: vec![("body", "ddTrace ddTrace ddTrace filler filler filler")],
                },
                BM25FDocument {
                    id: "low_tf",
                    fields: vec![("body", "ddTrace filler filler filler filler filler")],
                },
                BM25FDocument {
                    id: "no_match",
                    fields: vec![("body", "filler filler filler filler filler filler")],
                },
            ],
        );
        let results = index.search("trace", 10);
        let ids: Vec<&str> = results.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(ids, vec!["high_tf", "low_tf"]);
        assert!(results[0].score > results[1].score);
    }

    /// Inverse of `case_split_retrieves_a_doc_whole_identifier_does_not`:
    /// `WholeIdentifier` makes an exact-symbol match that `CaseSplit`
    /// over-splits into a FALSE positive. Corpus doc's text has "dd" and
    /// "trace" as unrelated separate words (never joined as `dd_trace`).
    /// Query `dd_trace` is one token under `WholeIdentifier` (exact-symbol)
    /// and correctly finds nothing; under `CaseSplit` it splits into
    /// `["dd", "trace"]`, both of which independently appear in the doc, so
    /// it wrongly matches -- proving `WholeIdentifier`'s exact-symbol
    /// contract is real, not just "matches more" like `CaseSplit`.
    #[test]
    fn whole_identifier_rejects_a_false_positive_case_split_over_splits_into() {
        let build = |tokenizer: Tokenizer| {
            OkapiIndex::build(
                tokenizer,
                [OkapiDocument {
                    id: "unrelated_words_doc",
                    text: "dd config, trace log",
                }],
            )
        };

        let whole_identifier_results = build(Tokenizer::WholeIdentifier).search("dd_trace", 10);
        assert_eq!(
            whole_identifier_results.len(),
            0,
            "WholeIdentifier keeps query \"dd_trace\" as one token, which never equals the \
             doc's separate \"dd\"/\"trace\" tokens -- must NOT match"
        );

        let case_split_results = build(Tokenizer::CaseSplit).search("dd_trace", 10);
        assert_eq!(
            case_split_results.len(),
            1,
            "CaseSplit splits query \"dd_trace\" into [\"dd\", \"trace\"], both of which \
             appear as unrelated separate words in the doc -- over-splitting produces a \
             false-positive match here, which is exactly why WholeIdentifier exists as the \
             exact-symbol opt-in"
        );
    }
}
