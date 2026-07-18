//! Okapi BM25: the classic flat, single-bag-of-tokens-per-document scorer.
//!
//! Standard Okapi BM25 — same constants (`K1 = 1.5`, `B = 0.75`), same
//! inverted-index shape (term → doc → term frequency), same scoring formula
//! as the textbook algorithm. Two design commitments matter here:
//!
//! 1. **Determinism.** A naive accumulator scores per-document totals into a
//!    `HashMap` and returns them via that map's iteration order, which
//!    Rust's `HashMap` never guarantees is stable — the same corpus/query
//!    could rank identically-scored documents in a different order run to
//!    run. This module never lets a `HashMap` iteration order reach the
//!    caller: ranked output is always sorted into a total order (score
//!    descending, then document id ascending as the tie-break), so identical
//!    inputs produce byte-identical output on every run, platform, and
//!    process.
//! 2. **Library shape.** This is a public, freestanding library type keyed
//!    by a caller-supplied `String` id, not an index bound to any one
//!    caller's internal row/chunk storage — so it carries no assumption
//!    about how the caller stores documents.
//!
//! # Sibling to BM25F
//! The fielded BM25F variant (per-field weights, multi-field documents)
//! is a sibling type in this crate — it does not touch `OkapiIndex`'s flat
//! single-field model. The two variants share nothing but the tokenizer
//! seam (`crate::tokenize`) and the ranked-output shape (score-desc, id-asc
//! total order) — both concerns are factored out here rather than inlined,
//! precisely so BM25F can reuse them.
//!
//! [`OkapiIndex::build`] takes a [`Tokenizer`] and stores it — the index
//! OWNS its tokenizer, so [`OkapiIndex::search`] always reuses the exact
//! same one `build` used; there is no API to call `search` with a different
//! tokenizer than the index was built with (build/search agreement is
//! structural, not a caller discipline). `ScoredDocument` lives in
//! [`crate::result`], a neutral module both variants depend on.

use std::collections::HashMap;

use crate::result::ScoredDocument;
use crate::tokenize::Tokenizer;

/// Term-frequency saturation constant. Higher `K1` lets repeated terms keep
/// contributing score for longer before saturating. Okapi BM25 default.
const K1: f64 = 1.5;

/// Document-length normalization strength, in `[0.0, 1.0]`. `0.0` disables
/// length normalization; `1.0` fully normalizes by document length relative
/// to the corpus average. Okapi BM25 default.
const B: f64 = 0.75;

/// One document's contribution to an [`OkapiIndex`]: a caller-supplied
/// identifier (e.g. a file path or chunk key) and its text.
///
/// # Invariants
/// `id` should be unique within a single [`OkapiIndex::build`] call. A
/// duplicate `id` is not rejected (this type does no validation) — it is
/// indexed as a distinct row and its score never merges with the earlier
/// row's, so callers relying on `id` as a lookup key after search should
/// de-duplicate before calling `build`.
pub struct Document<'a> {
    /// Caller-supplied identifier returned in [`ScoredDocument::id`].
    pub id: &'a str,
    /// The document's full text; tokenized internally with the
    /// [`Tokenizer`] passed to [`OkapiIndex::build`].
    pub text: &'a str,
}

/// An Okapi BM25 inverted index over a fixed corpus of documents.
///
/// Build once via [`OkapiIndex::build`], then query any number of times via
/// [`OkapiIndex::search`]. Immutable after construction — there is no
/// incremental-update API; re-run `build` over the full corpus to reindex.
pub struct OkapiIndex {
    /// term -> (doc row index -> term frequency in that doc).
    postings: HashMap<String, HashMap<usize, u32>>,
    /// doc row index -> caller-supplied id, in build order.
    doc_ids: Vec<String>,
    /// doc row index -> token count.
    doc_len: Vec<u32>,
    /// Corpus average document length, in tokens. `0.0` iff the corpus has
    /// zero documents or every document tokenizes to zero tokens.
    avgdl: f64,
    /// The tokenizer this index was built with; [`search`](Self::search)
    /// always reuses it, so a query and its documents can never tokenize
    /// under two different modes.
    tokenizer: Tokenizer,
}

impl OkapiIndex {
    /// Builds an index over `docs` using `tokenizer`. Each document is
    /// tokenized with `tokenizer`, which the index stores and reuses for
    /// every subsequent [`search`](Self::search) call — this is the
    /// build/search agreement guarantee: there is no way to query this
    /// index with a different tokenizer than it was built with. An empty or
    /// all-punctuation `text` yields a zero-length document, not an error.
    ///
    /// Deterministic: two calls with the same `tokenizer`/`docs` in the
    /// same order produce indexes that score and rank identically (row
    /// order affects nothing observable — [`search`](Self::search) output
    /// is a total order over `(score, id)`, independent of build/insertion
    /// order).
    #[must_use]
    pub fn build<'a, I>(tokenizer: Tokenizer, docs: I) -> Self
    where
        I: IntoIterator<Item = Document<'a>>,
    {
        let mut postings: HashMap<String, HashMap<usize, u32>> = HashMap::new();
        let mut doc_ids = Vec::new();
        let mut doc_len = Vec::new();
        let mut total_tokens: u64 = 0;

        for doc in docs {
            let row = doc_ids.len();
            let tokens = tokenizer.tokenize(doc.text);
            #[allow(
                clippy::cast_possible_truncation,
                reason = "a single document's token count fits u32 for any realistic corpus; \
                          truncation would require a >4B-token document"
            )]
            let len = tokens.len() as u32;
            total_tokens += u64::from(len);
            for term in tokens {
                *postings.entry(term).or_default().entry(row).or_insert(0) += 1;
            }
            doc_ids.push(doc.id.to_string());
            doc_len.push(len);
        }

        let n = doc_ids.len();
        let avgdl = if n == 0 {
            0.0
        } else {
            #[allow(
                clippy::cast_precision_loss,
                reason = "corpus token/doc counts are far below f64's 2^53 exact-integer range \
                          in any realistic corpus; BM25 is a statistical score, not an exact count"
            )]
            {
                total_tokens as f64 / n as f64
            }
        };

        Self {
            postings,
            doc_ids,
            doc_len,
            avgdl,
            tokenizer,
        }
    }

    /// Number of documents in the index.
    #[must_use]
    pub fn len(&self) -> usize {
        self.doc_ids.len()
    }

    /// `true` iff the index holds no documents.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.doc_ids.is_empty()
    }

    /// Inverse document frequency for `term`, Okapi's `+1`-smoothed form
    /// (kept positive so common terms never flip a score negative):
    /// `ln(1 + (N - df + 0.5) / (df + 0.5))`. Returns `0.0` for a term with
    /// zero document frequency (absent from the corpus) rather than `ln(1)`
    /// of an undefined ratio — the standard early-exit for a term with no
    /// documents to compare against.
    fn idf(&self, term: &str) -> f64 {
        let df = self.postings.get(term).map_or(0, HashMap::len);
        if df == 0 {
            return 0.0;
        }
        #[allow(
            clippy::cast_precision_loss,
            reason = "document/term counts are far below f64's 2^53 exact-integer range in any \
                      realistic corpus"
        )]
        let (n, df) = (self.doc_ids.len() as f64, df as f64);
        (1.0 + (n - df + 0.5) / (df + 0.5)).ln()
    }

    /// Scores every document against `query` and returns them ranked.
    ///
    /// Query tokens are deduplicated (a repeated query term contributes its
    /// idf-weighted score once) and tokenized with this
    /// index's own [`Tokenizer`] (the one passed to [`build`](Self::build))
    /// — a query and a document always tokenize identically, so e.g.
    /// exact-identifier queries (`foo_bar`) match documents containing
    /// that exact identifier under [`Tokenizer::WholeIdentifier`].
    ///
    /// Only documents sharing at least one query term score above `0.0` and
    /// appear in the output; documents with no term overlap are omitted
    /// entirely (BM25 has no defined "distance" for zero-overlap pairs).
    ///
    /// `top_n` caps the number of results returned; pass `usize::MAX` for
    /// "all matching documents."
    ///
    /// # Determinism
    /// Output is sorted by score descending, then by `id` ascending as a
    /// total-order tie-break, so ties (including a zero-`avgdl` corpus,
    /// where every score is `0.0`) never depend on internal `HashMap`
    /// iteration order. Identical `(index, query, top_n)` inputs always
    /// produce byte-identical output.
    #[must_use]
    pub fn search(&self, query: &str, top_n: usize) -> Vec<ScoredDocument> {
        let mut seen_terms = std::collections::HashSet::new();
        let mut scores: HashMap<usize, f64> = HashMap::new();

        for term in self.tokenizer.tokenize(query) {
            if !seen_terms.insert(term.clone()) {
                continue;
            }
            let idf = self.idf(&term);
            if idf == 0.0 {
                continue;
            }
            let Some(postings) = self.postings.get(&term) else {
                continue;
            };
            for (&row, &tf) in postings {
                // `f64::from` is a lossless, infallible u32 -> f64 widening
                // (u32::MAX < 2^53), so no cast-precision lint applies here.
                let (tf_f, doc_len_f) = (f64::from(tf), f64::from(self.doc_len[row]));
                // avgdl is 0.0 only when every document is zero-length, in
                // which case doc_len_f is also 0.0 for every row — treat the
                // length-normalization ratio as 0.0 rather than 0.0/0.0
                // (NaN), so search never returns a NaN score.
                let length_ratio = if self.avgdl == 0.0 {
                    0.0
                } else {
                    doc_len_f / self.avgdl
                };
                let denom = tf_f + K1 * (1.0 - B + B * length_ratio);
                let contribution = idf * (tf_f * (K1 + 1.0)) / denom;
                *scores.entry(row).or_insert(0.0) += contribution;
            }
        }

        let mut ranked: Vec<ScoredDocument> = scores
            .into_iter()
            .map(|(row, score)| ScoredDocument {
                id: self.doc_ids[row].clone(),
                score,
            })
            .collect();
        ranked.sort_by(|a, b| b.score.total_cmp(&a.score).then_with(|| a.id.cmp(&b.id)));
        ranked.truncate(top_n);
        ranked
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Golden derivation below is a worked numeric example (hand-computed
    // then cross-checked in Python) — kept as a plain `//` block comment,
    // not `///` doc comment, so `clippy::doc_markdown` doesn't require
    // backticking every code-like term in dense prose math.
    //
    // Golden ranking parity vs the exact Okapi formula (K1=1.5, B=0.75).
    //
    // Corpus (3 docs, hand-tokenized with the whole-identifier tokenizer):
    //   doc "a": "foo_bar span init"        -> 3 tokens
    //   doc "b": "foo_bar foo_bar context" -> 3 tokens (foo_bar tf=2)
    //   doc "c": "span context handler"      -> 3 tokens, no foo_bar
    // avgdl = (3+3+3)/3 = 3.0; every doc_len/avgdl ratio = 1.0, so the
    // length-normalization term is identical for all three docs — isolating
    // the tf/idf effect being tested.
    //
    // Query: "foo_bar".
    //   df("foo_bar") = 2 (docs a, b), N = 3.
    //   idf = ln(1 + (3 - 2 + 0.5)/(2 + 0.5)) = ln(1 + 1.5/2.5) = ln(1.6)
    //       = 0.4700036292457356 (computed independently in Python via
    //         math.log(1.6) to cross-check the Rust ln() call).
    //   denom (both docs, doc_len/avgdl=1.0) = tf + 1.5*(1 - 0.75 + 0.75*1.0)
    //       = tf + 1.5*1.0 = tf + 1.5.
    //   doc a: tf=1 -> score = idf * (1*2.5)/(1+1.5) = idf * 2.5/2.5 = idf.
    //   doc b: tf=2 -> score = idf * (2*2.5)/(2+1.5) = idf * 5/3.5
    //       = idf * 1.4285714285714286.
    //   doc c: tf=0 -> no "foo_bar" posting -> absent from results.
    //   Expected ranking: b (higher tf) > a; c excluded (zero overlap).
    #[test]
    fn golden_ranking_matches_exact_okapi_formula() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "a",
                    text: "foo_bar span init",
                },
                Document {
                    id: "b",
                    text: "foo_bar foo_bar context",
                },
                Document {
                    id: "c",
                    text: "span context handler",
                },
            ],
        );

        let results = index.search("foo_bar", 10);

        assert_eq!(results.len(), 2, "doc c has zero overlap and is excluded");
        assert_eq!(results[0].id, "b");
        assert_eq!(results[1].id, "a");
        assert!(results[0].score > results[1].score);

        let idf = (1.0_f64 + (3.0 - 2.0 + 0.5) / (2.0 + 0.5)).ln();
        let expected_a = idf * (1.0 * (K1 + 1.0)) / (1.0 + K1 * (1.0 - B + B));
        let expected_b = idf * (2.0 * (K1 + 1.0)) / (2.0 + K1 * (1.0 - B + B));
        assert!((results[1].score - expected_a).abs() < 1e-12);
        assert!((results[0].score - expected_b).abs() < 1e-12);
    }

    /// Repeated query terms are deduplicated — a query with a term twice
    /// scores identically to a query with it once.
    #[test]
    fn duplicate_query_terms_are_deduplicated() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "a",
                    text: "foo_bar span",
                },
                Document {
                    id: "b",
                    text: "span handler",
                },
            ],
        );
        let once = index.search("foo_bar", 10);
        let twice = index.search("foo_bar foo_bar", 10);
        assert_eq!(once, twice);
    }

    /// Determinism: identical inputs produce byte-identical (here,
    /// value-identical, which for `f64` scores is the strongest available
    /// check) output across repeated runs, and ties break by id, never by
    /// build/insertion or `HashMap` order.
    #[test]
    fn ranked_output_is_deterministic_with_id_tiebreak() {
        // Two docs with identical text score identically -- the tie must
        // resolve by id ascending ("a" before "z"), regardless of build
        // order or HashMap iteration order.
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "z",
                    text: "foo_bar span",
                },
                Document {
                    id: "a",
                    text: "foo_bar span",
                },
            ],
        );

        let first_run = index.search("foo_bar", 10);
        let second_run = index.search("foo_bar", 10);
        assert_eq!(first_run, second_run, "repeated runs must be identical");
        assert_eq!(
            first_run.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(),
            vec!["a", "z"],
            "equal scores must tie-break by id ascending"
        );
    }

    /// `top_n` truncates the ranked list without changing the order of the
    /// documents that remain.
    #[test]
    fn top_n_truncates_ranking() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "a",
                    text: "foo_bar foo_bar foo_bar",
                },
                Document {
                    id: "b",
                    text: "foo_bar foo_bar",
                },
                Document {
                    id: "c",
                    text: "foo_bar",
                },
            ],
        );
        let top1 = index.search("foo_bar", 1);
        assert_eq!(top1.len(), 1);
        assert_eq!(top1[0].id, "a");
    }

    /// Zero-length documents (empty text) and an empty corpus never panic
    /// or produce a `NaN` score — the `avgdl == 0.0` guard in `search`.
    #[test]
    fn empty_corpus_and_empty_documents_do_not_panic_or_produce_nan() {
        let empty_index = OkapiIndex::build(Tokenizer::WholeIdentifier, std::iter::empty());
        assert!(empty_index.is_empty());
        assert_eq!(empty_index.search("foo_bar", 10), Vec::new());

        let zero_len_index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document { id: "a", text: "" },
                Document { id: "b", text: "" },
            ],
        );
        assert_eq!(zero_len_index.search("foo_bar", 10), Vec::new());
    }

    /// An empty query yields no results (no terms to score against).
    #[test]
    fn empty_query_yields_no_results() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [Document {
                id: "a",
                text: "foo_bar span",
            }],
        );
        assert_eq!(index.search("", 10), Vec::new());
    }

    /// A single-document corpus: `avgdl` collapses to exactly that one
    /// document's length, so `length_ratio` is always `1.0` and the score
    /// reduces to the plain tf-saturation curve. No panic, no division by a
    /// stale/zero `avgdl`.
    #[test]
    fn single_doc_corpus_avgdl_equals_doc_len() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [Document {
                id: "only",
                text: "foo_bar foo_bar span",
            }],
        );
        let results = index.search("foo_bar", 10);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].id, "only");

        // N=1, df=1 -> idf = ln(1 + (1-1+0.5)/(1+0.5)) = ln(1 + 1/3).
        // doc_len = avgdl = 3.0 -> length_ratio = 1.0 -> denom = tf + 1.5.
        // tf=2 -> score = idf * (2*2.5)/(2+1.5) = idf * 5/3.5.
        let idf = (1.0_f64 + (1.0 - 1.0 + 0.5) / (1.0 + 0.5)).ln();
        let expected = idf * (2.0 * (K1 + 1.0)) / (2.0 + K1 * (1.0 - B + B));
        assert!((results[0].score - expected).abs() < 1e-12);
    }

    /// A query term entirely absent from the corpus contributes nothing and
    /// never surfaces a phantom result — `idf == 0.0` short-circuits before
    /// any postings lookup.
    #[test]
    fn query_term_absent_from_corpus_yields_no_result_for_it() {
        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [Document {
                id: "a",
                text: "foo_bar span",
            }],
        );
        assert_eq!(index.search("nonexistent_term", 10), Vec::new());
        // Mixed query: one present term, one absent -> only the present
        // term's contribution counts (absent term adds 0.0, not NaN/error).
        let mixed = index.search("foo_bar nonexistent_term", 10);
        let present_only = index.search("foo_bar", 10);
        assert_eq!(mixed, present_only);
    }

    /// Determinism against BUILD/insertion order (the headline risk this
    /// crate exists to fix): the same set of documents, inserted in two
    /// different orders, with several exact score ties, must rank
    /// byte-identically. If `search` ever leaked `HashMap` iteration order
    /// (postings map, per-term row map, or the query-scoped `scores` map),
    /// this test would flake across runs/orders; it does not, because
    /// output is always re-sorted into the total order before returning.
    #[test]
    fn ranking_is_identical_regardless_of_build_insertion_order() {
        let forward = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "doc_a",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_b",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_c",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_d",
                    text: "foo_bar context",
                },
            ],
        );
        let reversed = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "doc_d",
                    text: "foo_bar context",
                },
                Document {
                    id: "doc_c",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_b",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_a",
                    text: "foo_bar span",
                },
            ],
        );
        let shuffled = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "doc_b",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_d",
                    text: "foo_bar context",
                },
                Document {
                    id: "doc_a",
                    text: "foo_bar span",
                },
                Document {
                    id: "doc_c",
                    text: "foo_bar span",
                },
            ],
        );

        let forward_results = forward.search("foo_bar span", 10);
        let reversed_results = reversed.search("foo_bar span", 10);
        let shuffled_results = shuffled.search("foo_bar span", 10);

        assert_eq!(forward_results, reversed_results);
        assert_eq!(forward_results, shuffled_results);
        // doc_a/doc_b/doc_c are exact ties (identical text) -> id-ascending
        // tie-break; doc_d scores lower (no "span" overlap) and sorts last.
        assert_eq!(
            forward_results
                .iter()
                .map(|d| d.id.as_str())
                .collect::<Vec<_>>(),
            vec!["doc_a", "doc_b", "doc_c", "doc_d"]
        );
    }

    /// Independent golden: expected scores below were computed OUTSIDE this
    /// crate and outside its formula call path — a standalone Python script
    /// (not sharing a line of code with `okapi.rs` or this test file)
    /// re-implementing the textbook Okapi BM25 formula from scratch and
    /// printing `repr()` of the results, which are hardcoded here as float
    /// literals. This is deliberately NOT the same shape as
    /// `golden_ranking_matches_exact_okapi_formula` above, whose expected values
    /// are computed inline in Rust using the same closed-form expression the
    /// implementation uses — a regression that changed the formula in both
    /// `okapi.rs` and that inline expression in lock-step would still pass
    /// that test. This test cannot be fooled that way: the literals are
    /// frozen numbers with no formula in this file to co-mutate.
    ///
    /// Reproduction (ad hoc, not part of the build): corpus/tokens/formula
    /// below, run with `python3`.
    /// ```text
    /// import math
    /// K1, B = 1.5, 0.75
    /// docs = {
    ///     "alpha": "foo_bar span init handler",
    ///     "beta":  "foo_bar context handler retry retry",
    ///     "gamma": "span context handler",
    ///     "delta": "foo_bar foo_bar foo_bar context",
    /// }
    /// import re
    /// toks = {k: re.findall(r"[a-z0-9_]+", v.lower()) for k, v in docs.items()}
    /// doc_len = {k: len(v) for k, v in toks.items()}
    /// N = len(docs)
    /// avgdl = sum(doc_len.values()) / N
    /// def tf(t, d): return toks[d].count(t)
    /// def df(t): return sum(1 for d in toks.values() if t in d)
    /// def idf(t):
    ///     d = df(t)
    ///     return 0.0 if d == 0 else math.log(1 + (N - d + 0.5) / (d + 0.5))
    /// def score(t, d):
    ///     f = tf(t, d)
    ///     if f == 0: return 0.0
    ///     denom = f + K1 * (1 - B + B * doc_len[d] / avgdl)
    ///     return idf(t) * (f * (K1 + 1)) / denom
    /// for d in docs:
    ///     print(d, sum(score(t, d) for t in ["foo_bar", "handler"]))
    /// ```
    /// Output (frozen, hardcoded below):
    ///   alpha 0.7133498878774648
    ///   beta  0.6412133823617662
    ///   gamma 0.40188726077603654
    ///   delta 0.5944582398978873
    #[test]
    fn independent_hardcoded_golden_matches_frozen_python_reference() {
        const TOLERANCE: f64 = 1e-9;

        let index = OkapiIndex::build(
            Tokenizer::WholeIdentifier,
            [
                Document {
                    id: "alpha",
                    text: "foo_bar span init handler",
                },
                Document {
                    id: "beta",
                    text: "foo_bar context handler retry retry",
                },
                Document {
                    id: "gamma",
                    text: "span context handler",
                },
                Document {
                    id: "delta",
                    text: "foo_bar foo_bar foo_bar context",
                },
            ],
        );

        let results = index.search("foo_bar handler", 10);

        let expected: &[(&str, f64)] = &[
            ("alpha", 0.713_349_887_877_464_8),
            ("beta", 0.641_213_382_361_766_2),
            ("delta", 0.594_458_239_897_887_3),
            ("gamma", 0.401_887_260_776_036_54),
        ];

        assert_eq!(
            results.len(),
            expected.len(),
            "result count must match the frozen reference"
        );
        for (got, (expected_id, expected_score)) in results.iter().zip(expected.iter()) {
            assert_eq!(
                &got.id, expected_id,
                "ranking order must match the frozen reference"
            );
            assert!(
                (got.score - expected_score).abs() < TOLERANCE,
                "score for {} was {} but frozen reference expects {}",
                got.id,
                got.score,
                expected_score
            );
        }
    }
}
