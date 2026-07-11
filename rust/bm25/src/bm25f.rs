//! BM25F: the fielded, per-field-weighted scorer.
//!
//! Unlike [`crate::okapi::OkapiIndex`] (one bag of tokens per document),
//! BM25F treats a document as a fixed set of NAMED fields (e.g. `name`,
//! `description`, `body`), each with its own weight and length-normalization
//! strength `b`. This is NEW work relative to ka's seed — ka's
//! `retrieve::bm25` is flat single-field and has no fielded concept at all.
//!
//! # Formula
//! For query term `t` and document `d`, per configured field `f`:
//! - `tf_norm(t,f) = tf(t,f) / (1 - b_f + b_f * (len_f(d) / avg_len_f))`
//!   — length normalization happens INSIDE the per-field term, before the
//!   fields are combined.
//! - `tf_hat(t,d) = Σ_f  w_f * tf_norm(t,f)` — the weighted pseudo-frequency,
//!   summed across every configured field.
//! - `term_score(t,d) = idf(t) * tf_hat(t,d) / (K1 + tf_hat(t,d))` —
//!   saturation is applied OUTSIDE the field sum, once, to the combined
//!   pseudo-frequency. This is the defining structural difference from Okapi
//!   BM25, which saturates a single flat `tf` directly; naively saturating
//!   each field independently and then summing is a different (and wrong)
//!   formula, so `tf_hat` must be fully combined across fields before `K1`
//!   ever enters the expression.
//! - `doc_score(d) = Σ over deduped query terms  term_score(t,d)`.
//!
//! `idf` is the same `+1`-smoothed form Okapi uses:
//! `ln(1 + (N - df + 0.5) / (df + 0.5))`, `df` = number of documents
//! containing `t` in ANY configured field. Always `>= 0.0`.
//!
//! # Determinism (the named risk for this task)
//! Two sums are float-associativity-sensitive, and both are pinned to a
//! FIXED, non-`HashMap` order:
//! - **Query terms**, deduped into a `Vec` in left-to-right tokenize order
//!   (never a `HashSet`'s iteration order) — mirrors `OkapiIndex::search`.
//! - **Fields**, iterated in [`BM25FConfig`]'s REGISTRATION order (the order
//!   [`BM25FConfig::with_field`] calls were made) — never a `HashMap`'s
//!   iteration order. Every document's per-field term frequency is stored as
//!   a dense `Vec<u32>` indexed by field position, not a field-name-keyed
//!   map, so "iterate fields" is a plain `0..num_fields` loop with no map
//!   involved at scoring time. This is the mechanism, not just the policy:
//!   there is no `HashMap<field name, _>` in the hot scoring path for a
//!   reader to accidentally introduce.
//!
//! `HashMap` is still used for accumulation (postings: term -> row -> per-
//! field tf vector; and the per-query scores: row -> running sum) exactly as
//! in [`crate::okapi`] — never for output order, and never for an order-
//! sensitive sum's iteration order. Ranked output is always re-sorted into
//! the same total order `okapi` uses: score descending
//! (`f64::total_cmp`), then `id` ascending as the tie-break.

use std::collections::{HashMap, HashSet};

use crate::okapi::ScoredDocument;
use crate::tokenize::tokenize_whole_identifier;

/// Term-frequency saturation constant, applied once to the combined
/// cross-field pseudo-frequency (`tf_hat`). Same value as
/// [`crate::okapi`]'s `K1` — BM25F's saturation curve is the same shape as
/// Okapi's, just applied to a different quantity.
const K1: f64 = 1.5;

/// One field's weight and length-normalization strength within a
/// [`BM25FConfig`].
///
/// `weight` scales that field's contribution to a document's combined
/// pseudo-frequency before saturation — a higher `weight` makes a match in
/// that field count for more. `b` is the per-field analogue of Okapi's `B`:
/// `0.0` disables length normalization for this field, `1.0` fully
/// normalizes by this field's length relative to the corpus average for
/// this field.
///
/// # Domain
/// `weight` must be finite and `>= 0.0`; `b` must be finite and in
/// `[0.0, 1.0]`. These are load-bearing invariants, not style preferences:
/// a negative `weight` can drive a document's combined pseudo-frequency
/// `tf_hat` negative, which (a) makes a genuine match score `<= 0.0` so the
/// score-positive output filter silently drops it, (b) hits a
/// division-by-zero (`inf`/`NaN`, violating [`ScoredDocument`]'s finite-score
/// guarantee) exactly at `tf_hat == -K1`, and (c) yields spuriously HIGH
/// positive scores once `tf_hat < -K1` — ranking a non-match at the top. A
/// `b` outside `[0.0, 1.0]` breaks the same way through the length-norm
/// denominator `1 - b + b*ratio`. [`BM25FConfig::with_field`] enforces both
/// domains at construction, so an index never scores against an out-of-domain
/// [`FieldWeight`].
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FieldWeight {
    /// This field's weight `w_f` in the cross-field sum. Finite and
    /// `>= 0.0`. Not required to sum to `1.0` across fields — BM25F weights
    /// are relative scaling factors, not a probability distribution.
    pub weight: f64,
    /// This field's length-normalization strength `b_f`, finite and in
    /// `[0.0, 1.0]`.
    pub b: f64,
}

/// Fielded scoring configuration: which fields exist, in what order, and
/// each one's [`FieldWeight`].
///
/// # Invariants
/// Field REGISTRATION order (the order [`BM25FConfig::with_field`] calls
/// happen in) is the FIXED order [`BM25FIndex::search`] iterates fields in
/// when summing a term's cross-field pseudo-frequency — see the module-level
/// determinism note. Two configs built with the same fields in a different
/// order are logically equivalent (the sum is commutative in exact
/// arithmetic) but are NOT guaranteed to produce byte-identical `f64` scores
/// against each other, because float addition is not associative; a single
/// config used consistently across build and search always is.
#[derive(Debug, Clone, Default)]
pub struct BM25FConfig {
    fields: Vec<(String, FieldWeight)>,
}

impl BM25FConfig {
    /// An empty configuration (no fields registered). Add fields with
    /// [`with_field`](Self::with_field) before calling
    /// [`BM25FIndex::build`] — a config with zero fields builds an index
    /// where every document scores `0.0` against every query (no field
    /// ever contributes).
    #[must_use]
    pub fn new() -> Self {
        Self { fields: Vec::new() }
    }

    /// Registers a field named `name` with the given `weight` and `b`,
    /// appending it after any previously registered fields. Returns `self`
    /// for chaining.
    ///
    /// Registering the same `name` twice adds a second, independent field
    /// slot — [`Document`] field lookups match by name against every
    /// registered slot in registration order, so a duplicate name is
    /// treated as two distinct fields that happen to share a label, not
    /// merged. Callers should register each field name once.
    ///
    /// # Panics
    /// Panics if `weight` is not finite or is negative, or if `b` is not
    /// finite or lies outside `[0.0, 1.0]` — see [`FieldWeight`]'s domain
    /// note for why an out-of-domain value is a correctness hazard, not a
    /// tuning choice. This is a fail-fast contract on a construction-time
    /// programming constant (field weights are code, not runtime data), so
    /// an invalid weight surfaces at the `with_field` call site rather than
    /// silently corrupting scores; the check is an always-on `assert!` (not
    /// a `debug_assert!`) precisely so it cannot compile out in release and
    /// reintroduce the silent-failure path.
    #[must_use]
    pub fn with_field(mut self, name: &str, weight: f64, b: f64) -> Self {
        assert!(
            weight.is_finite() && weight >= 0.0,
            "field {name:?} weight must be finite and >= 0.0, got {weight}"
        );
        assert!(
            b.is_finite() && (0.0..=1.0).contains(&b),
            "field {name:?} b must be finite and in [0.0, 1.0], got {b}"
        );
        self.fields
            .push((name.to_string(), FieldWeight { weight, b }));
        self
    }

    /// A ready-made configuration matching navigator's frontmatter
    /// weighting: `name`/`id`/`tags` weighted highest, `description` next,
    /// `body` lowest (design doc: "Shared Rust library" §(a)). The exact
    /// multipliers (`3.0`/`2.0`/`1.0`) are this library's default choice —
    /// the ORDINAL ranking (highest > next > lowest) is the pinned
    /// requirement; callers who want different multipliers build their own
    /// [`BM25FConfig`] via [`with_field`](Self::with_field) instead.
    #[must_use]
    pub fn frontmatter_default() -> Self {
        Self::new()
            .with_field("name", 3.0, 0.75)
            .with_field("id", 3.0, 0.75)
            .with_field("tags", 3.0, 0.75)
            .with_field("description", 2.0, 0.75)
            .with_field("body", 1.0, 0.75)
    }

    /// Number of registered fields.
    fn len(&self) -> usize {
        self.fields.len()
    }

    /// Maps a field name to its registration-order index, for the first
    /// registered slot with that name.
    fn index_of(&self, name: &str) -> Option<usize> {
        self.fields.iter().position(|(n, _)| n == name)
    }
}

/// One document's contribution to a [`BM25FIndex`]: a caller-supplied
/// identifier and an ordered list of `(field name, field text)` pairs.
///
/// # Invariants
/// `id` should be unique within a single [`BM25FIndex::build`] call, same
/// caveat as [`crate::okapi::Document`]. `fields` may list field names in
/// any order, omit a field the [`BM25FConfig`] registers (treated as that
/// field being empty for this document — length `0`, no term contributes
/// from it), or include a field name the config never registered (ignored:
/// an unconfigured field has no weight to score with, so its text never
/// reaches the index). Listing the same field name twice accumulates both
/// texts' tokens into that one field slot (as if concatenated).
pub struct Document<'a> {
    /// Caller-supplied identifier returned in [`ScoredDocument::id`].
    pub id: &'a str,
    /// `(field name, field text)` pairs. Field text is tokenized internally
    /// with [`tokenize_whole_identifier`].
    pub fields: Vec<(&'a str, &'a str)>,
}

/// A BM25F inverted index over a fixed corpus of fielded documents, scored
/// against a fixed [`BM25FConfig`].
///
/// Build once via [`BM25FIndex::build`], then query any number of times via
/// [`BM25FIndex::search`]. Immutable after construction, same shape as
/// [`crate::okapi::OkapiIndex`].
pub struct BM25FIndex {
    /// The configuration this index was built with — field names, weights,
    /// `b`, and (critically for determinism) registration order.
    config: BM25FConfig,
    /// term -> (doc row index -> per-field term frequency, dense `Vec`
    /// indexed by field registration position, length == `config.len()`).
    postings: HashMap<String, HashMap<usize, Vec<u32>>>,
    /// doc row index -> caller-supplied id, in build order.
    doc_ids: Vec<String>,
    /// doc row index -> per-field token count, dense `Vec` indexed by field
    /// registration position, length == `config.len()`.
    doc_field_len: Vec<Vec<u32>>,
    /// Corpus average field length, in tokens, indexed by field
    /// registration position. `0.0` at index `i` iff every document's
    /// field `i` is empty or absent (or the corpus has zero documents).
    avg_field_len: Vec<f64>,
}

impl BM25FIndex {
    /// Builds an index over `docs` against `config`. Each field's text is
    /// tokenized with [`tokenize_whole_identifier`]; an empty, absent, or
    /// all-punctuation field yields a zero-length field for that document,
    /// not an error.
    ///
    /// Deterministic: two calls with the same `config`/`docs` in the same
    /// document order produce indexes that score and rank identically;
    /// document build order affects nothing observable in
    /// [`search`](Self::search)'s output (a total order over `(score,
    /// id)`), matching [`crate::okapi::OkapiIndex::build`].
    #[must_use]
    pub fn build<'a, I>(config: &BM25FConfig, docs: I) -> Self
    where
        I: IntoIterator<Item = Document<'a>>,
    {
        let num_fields = config.len();
        let mut postings: HashMap<String, HashMap<usize, Vec<u32>>> = HashMap::new();
        let mut doc_ids = Vec::new();
        let mut doc_field_len: Vec<Vec<u32>> = Vec::new();
        let mut total_field_tokens: Vec<u64> = vec![0; num_fields];

        for doc in docs {
            let row = doc_ids.len();
            let mut field_len = vec![0u32; num_fields];

            for (field_name, text) in &doc.fields {
                let Some(field_idx) = config.index_of(field_name) else {
                    // Unconfigured field: no weight to score with, so its
                    // text never enters postings or length accounting.
                    continue;
                };
                let tokens = tokenize_whole_identifier(text);
                #[allow(
                    clippy::cast_possible_truncation,
                    reason = "a single document field's token count fits u32 for any realistic \
                              corpus; truncation would require a >4B-token field"
                )]
                let token_count = tokens.len() as u32;
                field_len[field_idx] += token_count;
                for term in tokens {
                    let row_map = postings.entry(term).or_default();
                    let tf_vec = row_map.entry(row).or_insert_with(|| vec![0u32; num_fields]);
                    tf_vec[field_idx] += 1;
                }
            }

            for (idx, len) in field_len.iter().enumerate() {
                total_field_tokens[idx] += u64::from(*len);
            }
            doc_ids.push(doc.id.to_string());
            doc_field_len.push(field_len);
        }

        let n = doc_ids.len();
        let avg_field_len: Vec<f64> = total_field_tokens
            .iter()
            .map(|&total| {
                if n == 0 {
                    0.0
                } else {
                    #[allow(
                        clippy::cast_precision_loss,
                        reason = "corpus token/doc counts are far below f64's 2^53 \
                                  exact-integer range in any realistic corpus; BM25F is a \
                                  statistical score, not an exact count"
                    )]
                    {
                        total as f64 / n as f64
                    }
                }
            })
            .collect();

        Self {
            config: config.clone(),
            postings,
            doc_ids,
            doc_field_len,
            avg_field_len,
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

    /// Inverse document frequency for `term`: same `+1`-smoothed form as
    /// [`crate::okapi::OkapiIndex`] — `ln(1 + (N - df + 0.5) / (df +
    /// 0.5))`, always `>= 0.0`. `df` counts a document once if `term`
    /// appears in ANY of its configured fields (not once per field).
    /// Returns `0.0` for a term with zero document frequency.
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

    /// This row's cross-field pseudo-frequency `tf_hat(t,d)` for one query
    /// term, given `tf_vec` (that row's per-field term frequency for the
    /// term, dense, indexed by field registration position).
    ///
    /// Iterates fields `0..num_fields` in [`BM25FConfig`] REGISTRATION
    /// order — the fixed order the module-level determinism note commits
    /// to — never a `HashMap`'s iteration order (there is no field-keyed
    /// map here at all).
    fn tf_hat(&self, row: usize, tf_vec: &[u32]) -> f64 {
        let mut tf_hat = 0.0;
        for (field_idx, (_, field_weight)) in self.config.fields.iter().enumerate() {
            let tf = tf_vec[field_idx];
            if tf == 0 {
                continue;
            }
            // f64::from is a lossless, infallible u32 -> f64 widening
            // (u32::MAX < 2^53); no cast-precision-loss lint applies.
            let tf_f = f64::from(tf);
            let avg_len = self.avg_field_len[field_idx];
            let len_f = f64::from(self.doc_field_len[row][field_idx]);
            // avg_len is 0.0 only when every document's this field is
            // empty/absent, in which case len_f is also 0.0 for every row
            // — treat the length-normalization ratio as 0.0 rather than
            // 0.0/0.0 (NaN), matching OkapiIndex::search's guard.
            let length_ratio = if avg_len == 0.0 { 0.0 } else { len_f / avg_len };
            let tf_norm = tf_f / (1.0 - field_weight.b + field_weight.b * length_ratio);
            tf_hat += field_weight.weight * tf_norm;
        }
        tf_hat
    }

    /// Scores every document against `query` and returns them ranked.
    ///
    /// Query tokens are deduplicated (a repeated query term contributes its
    /// idf-weighted score once) and tokenized with the same
    /// [`tokenize_whole_identifier`] used at build time, matching
    /// [`crate::okapi::OkapiIndex::search`].
    ///
    /// Only documents sharing at least one query term (in any configured
    /// field) score above `0.0` and appear in the output.
    ///
    /// `top_n` caps the number of results returned; pass `usize::MAX` for
    /// "all matching documents."
    ///
    /// # Determinism
    /// Query terms are summed in deduped left-to-right tokenize order;
    /// within each term, fields are summed in `config` registration order
    /// (see [`tf_hat`](Self::tf_hat)) — both fixed, neither a `HashMap`
    /// iteration order. Output is sorted by score descending
    /// (`f64::total_cmp`), then by `id` ascending as a total-order
    /// tie-break, so identical `(index, query, top_n)` inputs always
    /// produce byte-identical output regardless of build/insertion order.
    #[must_use]
    pub fn search(&self, query: &str, top_n: usize) -> Vec<ScoredDocument> {
        let mut seen_terms = HashSet::new();
        let mut scores: HashMap<usize, f64> = HashMap::new();

        for term in tokenize_whole_identifier(query) {
            if !seen_terms.insert(term.clone()) {
                continue;
            }
            let idf = self.idf(&term);
            if idf == 0.0 {
                continue;
            }
            let Some(term_postings) = self.postings.get(&term) else {
                continue;
            };
            for (&row, tf_vec) in term_postings {
                let tf_hat = self.tf_hat(row, tf_vec);
                let contribution = idf * tf_hat / (K1 + tf_hat);
                *scores.entry(row).or_insert(0.0) += contribution;
            }
        }

        // A row can accumulate a total of exactly `0.0` even though it has a
        // `postings` entry for a matched term — e.g. every field the term
        // matched in has weight `0.0`, so `tf_hat`/`contribution` collapse to
        // `0.0`. That is not a real match; filter it out BEFORE sorting so it
        // never surfaces as a phantom `score: 0.0` hit, matching `okapi`'s
        // matching-docs-only semantics and this method's own doc comment.
        // `score > 0.0` (not `!= 0.0`) is exactly right because every score
        // is non-negative: `idf >= 0`, and `tf_hat >= 0` since every field
        // `weight >= 0.0` and `b in [0.0, 1.0]` are enforced at construction
        // (`with_field`) — so `tf_hat/(K1+tf_hat) in [0,1)` and a real match
        // is always strictly positive. Were negative weights admissible,
        // `tf_hat` could go negative and a genuine match could land `<= 0.0`
        // and be silently dropped here; the construction-time domain check is
        // what makes this filter safe.
        let mut ranked: Vec<ScoredDocument> = scores
            .into_iter()
            .filter(|&(_, score)| score > 0.0)
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

    /// Per-field weighting actually changes ranking, not just compiles: two
    /// docs match the SAME query term in DIFFERENT fields (`zzz_title` in
    /// its `title` field, `aaa_body` in its `body` field); both fields have
    /// equal token length (`1`) so `tf_norm` is identical for both docs at
    /// equal weight, isolating the weight effect.
    ///
    /// At equal field weights the two docs score identically -> the id-
    /// ascending tie-break puts `aaa_body` first. Raising ONLY the `title`
    /// field's weight makes `zzz_title` outscore `aaa_body` despite `z` >
    /// `a` — proving the reordering came from the weight, not the
    /// tie-break.
    #[test]
    fn raising_a_fields_weight_reorders_matches_in_that_field_above_others() {
        let docs = || {
            [
                Document {
                    id: "zzz_title",
                    fields: vec![("title", "target"), ("body", "other")],
                },
                Document {
                    id: "aaa_body",
                    fields: vec![("title", "other"), ("body", "target")],
                },
            ]
        };

        let equal_weights = BM25FConfig::new()
            .with_field("title", 1.0, 0.75)
            .with_field("body", 1.0, 0.75);
        let tied = BM25FIndex::build(&equal_weights, docs()).search("target", 10);
        assert_eq!(
            tied.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(),
            vec!["aaa_body", "zzz_title"],
            "equal weights -> equal scores -> id-ascending tie-break"
        );

        let title_weighted = BM25FConfig::new()
            .with_field("title", 5.0, 0.75)
            .with_field("body", 1.0, 0.75);
        let reordered = BM25FIndex::build(&title_weighted, docs()).search("target", 10);
        assert_eq!(
            reordered.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(),
            vec!["zzz_title", "aaa_body"],
            "raising the title field's weight must reorder the title match above the body match"
        );
        assert!(reordered[0].score > reordered[1].score);
    }

    /// Determinism against BUILD/insertion order AND field-registration
    /// order within the config: several exact-score ties, and the same
    /// logical config expressed with fields registered in two different
    /// orders (which is NOT guaranteed to reproduce identical `f64` sums
    /// per the module docs — this test's docs are constructed so both
    /// orders happen to land on the same value, exercising the "config used
    /// consistently" path, not the cross-config-order claim).
    #[test]
    fn ranking_is_identical_regardless_of_build_insertion_order() {
        let config = BM25FConfig::new()
            .with_field("title", 2.0, 0.75)
            .with_field("body", 1.0, 0.75);

        let make = |order: [&str; 4]| {
            let by_id = |id: &str| -> Document {
                match id {
                    "doc_a" => Document {
                        id: "doc_a",
                        fields: vec![("title", "dd_trace"), ("body", "span")],
                    },
                    "doc_b" => Document {
                        id: "doc_b",
                        fields: vec![("title", "dd_trace"), ("body", "span")],
                    },
                    "doc_c" => Document {
                        id: "doc_c",
                        fields: vec![("title", "dd_trace"), ("body", "span")],
                    },
                    "doc_d" => Document {
                        id: "doc_d",
                        fields: vec![("title", "dd_trace"), ("body", "context")],
                    },
                    _ => unreachable!(),
                }
            };
            BM25FIndex::build(&config, order.into_iter().map(by_id))
        };

        let forward = make(["doc_a", "doc_b", "doc_c", "doc_d"]);
        let reversed = make(["doc_d", "doc_c", "doc_b", "doc_a"]);
        let shuffled = make(["doc_b", "doc_d", "doc_a", "doc_c"]);

        let forward_results = forward.search("dd_trace span", 10);
        let reversed_results = reversed.search("dd_trace span", 10);
        let shuffled_results = shuffled.search("dd_trace span", 10);

        assert_eq!(forward_results, reversed_results);
        assert_eq!(forward_results, shuffled_results);
        assert_eq!(
            forward_results
                .iter()
                .map(|d| d.id.as_str())
                .collect::<Vec<_>>(),
            vec!["doc_a", "doc_b", "doc_c", "doc_d"]
        );
    }

    /// Repeated runs against the same index produce byte-identical (here,
    /// value-identical) output, and an exact tie breaks by id ascending —
    /// mirrors `okapi::tests::ranked_output_is_deterministic_with_id_tiebreak`.
    #[test]
    fn ranked_output_is_deterministic_with_id_tiebreak() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            [
                Document {
                    id: "z",
                    fields: vec![("body", "dd_trace span")],
                },
                Document {
                    id: "a",
                    fields: vec![("body", "dd_trace span")],
                },
            ],
        );

        let first_run = index.search("dd_trace", 10);
        let second_run = index.search("dd_trace", 10);
        assert_eq!(first_run, second_run);
        assert_eq!(
            first_run.iter().map(|d| d.id.as_str()).collect::<Vec<_>>(),
            vec!["a", "z"]
        );
    }

    /// Edge cases: empty field text, a field entirely absent from a
    /// document's `fields` list, a single-document corpus, an empty query,
    /// and a query term absent from the corpus — none panics or produces a
    /// `NaN`/`inf` score.
    #[test]
    fn edge_cases_do_not_panic_or_produce_nan() {
        let config = BM25FConfig::new()
            .with_field("title", 2.0, 0.75)
            .with_field("body", 1.0, 0.75);

        // Empty corpus.
        let empty_index = BM25FIndex::build(&config, std::iter::empty());
        assert!(empty_index.is_empty());
        assert_eq!(empty_index.search("dd_trace", 10), Vec::new());

        // Empty field text, and a field entirely absent from a document.
        let index = BM25FIndex::build(
            &config,
            [
                Document {
                    id: "empty_title",
                    fields: vec![("title", ""), ("body", "dd_trace")],
                },
                Document {
                    id: "missing_title",
                    fields: vec![("body", "dd_trace")],
                },
            ],
        );
        let results = index.search("dd_trace", 10);
        assert_eq!(results.len(), 2);
        for doc in &results {
            assert!(doc.score.is_finite());
            assert!(!doc.score.is_nan());
        }

        // Single-document corpus.
        let single = BM25FIndex::build(
            &config,
            [Document {
                id: "only",
                fields: vec![("title", "dd_trace"), ("body", "span")],
            }],
        );
        let single_results = single.search("dd_trace", 10);
        assert_eq!(single_results.len(), 1);
        assert!(single_results[0].score.is_finite());

        // Absent query term.
        assert_eq!(single.search("nonexistent_term", 10), Vec::new());

        // Empty query.
        assert_eq!(single.search("", 10), Vec::new());
    }

    /// A field weighted `0.0` contributes nothing, even when the query term
    /// appears there: two otherwise-identical docs differ only in which
    /// field holds the match (`title` vs. `body`); `title` weight is `0.0`.
    /// If `title`'s match still contributed, `title_match` would outscore
    /// `no_match_at_all` (which has zero occurrences anywhere) — instead
    /// both must score identically to `0.0`.
    #[test]
    fn zero_field_weight_contributes_nothing() {
        let config = BM25FConfig::new()
            .with_field("title", 0.0, 0.75)
            .with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            [
                Document {
                    id: "title_match",
                    fields: vec![("title", "target"), ("body", "other")],
                },
                Document {
                    id: "no_match_at_all",
                    fields: vec![("title", "other"), ("body", "other")],
                },
            ],
        );
        // "target" only ever appears in a zero-weighted field -> no
        // document scores above 0.0, so neither appears in ranked output.
        assert_eq!(index.search("target", 10), Vec::new());
    }

    /// `b_f` actually changes length normalization: same relative tf, but
    /// one doc's field is longer than the corpus average and the other's is
    /// shorter. At `b = 0.0` (length normalization disabled) both docs must
    /// score IDENTICALLY despite the length difference; at `b = 1.0` (full
    /// length normalization) the shorter-than-average doc must score
    /// STRICTLY HIGHER (less length penalty).
    #[test]
    fn b_f_controls_length_normalization_strength() {
        let build_with_b = |b: f64| {
            let config = BM25FConfig::new().with_field("body", 1.0, b);
            BM25FIndex::build(
                &config,
                [
                    Document {
                        id: "short",
                        fields: vec![("body", "target")],
                    },
                    Document {
                        id: "long",
                        fields: vec![("body", "target filler filler filler filler")],
                    },
                ],
            )
        };

        let b_zero = build_with_b(0.0);
        let results_b_zero = b_zero.search("target", 10);
        assert_eq!(results_b_zero.len(), 2);
        assert!(
            (results_b_zero[0].score - results_b_zero[1].score).abs() < 1e-12,
            "b=0.0 must disable length normalization: scores must be identical regardless of \
             field length, got {:?}",
            results_b_zero
                .iter()
                .map(|d| (d.id.as_str(), d.score))
                .collect::<Vec<_>>()
        );

        let b_one = build_with_b(1.0);
        let results_b_one = b_one.search("target", 10);
        assert_eq!(results_b_one.len(), 2);
        assert_eq!(
            results_b_one[0].id, "short",
            "b=1.0 must fully length-normalize: the shorter-than-average field must rank above \
             the longer-than-average field"
        );
        assert!(results_b_one[0].score > results_b_one[1].score);
    }

    /// Duplicate field registrations (same name, two `with_field` calls) are
    /// treated as two independent slots per the documented invariant — no
    /// panic, and a document's single field-name/text pair only ever
    /// populates the FIRST registered slot with that name
    /// ([`BM25FConfig::index_of`] returns the first match), leaving the
    /// second slot at length `0` for that document.
    #[test]
    fn duplicate_field_names_are_independent_slots_without_panic() {
        let config = BM25FConfig::new()
            .with_field("body", 1.0, 0.75)
            .with_field("body", 5.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            [Document {
                id: "only",
                fields: vec![("body", "target")],
            }],
        );
        let results = index.search("target", 10);
        assert_eq!(results.len(), 1);
        assert!(results[0].score.is_finite());
        assert!(!results[0].score.is_nan());
        // Deterministic across repeated runs against the same index.
        assert_eq!(results, index.search("target", 10));
    }

    /// A field name present on a [`Document`] but never registered in the
    /// [`BM25FConfig`] is silently ignored: its text never enters postings
    /// or length accounting, so a query term appearing ONLY in that
    /// unconfigured field never matches, and the document is otherwise
    /// scored as if that field/text were never supplied at all — no panic.
    #[test]
    fn unconfigured_field_name_on_a_document_is_silently_ignored() {
        let config = BM25FConfig::new().with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            [Document {
                id: "has_unconfigured_field",
                fields: vec![("body", "known"), ("unconfigured", "target")],
            }],
        );
        // "target" only appears in the unconfigured field -> no match.
        assert_eq!(index.search("target", 10), Vec::new());
        // "known" (in the configured field) still matches normally.
        let known_results = index.search("known", 10);
        assert_eq!(known_results.len(), 1);
        assert!(known_results[0].score.is_finite());
    }

    /// Independent golden: expected scores below were computed OUTSIDE this
    /// crate by a standalone Python re-implementation of the BM25F formula
    /// (fresh code, not sharing a line with `bm25f.rs`), whose `repr()`
    /// output is hardcoded here as float literals — same discipline as
    /// `okapi::tests::independent_hardcoded_golden_matches_frozen_python_reference`.
    ///
    /// Reproduction (ad hoc, not part of the build), run with `python3`:
    /// ```text
    /// import math, re
    /// K1 = 1.5
    /// def tokenize(s): return re.findall(r"[a-z0-9_]+", s.lower())
    /// config = [("title", 3.0, 0.75), ("tags", 2.0, 0.75), ("body", 1.0, 0.75)]
    /// docs = [
    ///     ("alpha", [("title","dd_trace parser"), ("tags","rust config"),
    ///                ("body","span init handler")]),
    ///     ("beta",  [("title","span handler"), ("tags","dd_trace"),
    ///                ("body","context retry retry dd_trace")]),
    ///     ("gamma", [("title","handler"), ("tags","context"),
    ///                ("body","span context handler")]),
    /// ]
    /// field_index = {name: i for i, (name, w, b) in enumerate(config)}
    /// n_fields = len(config)
    /// postings, doc_field_len, doc_ids = {}, [], []
    /// total = [0]*n_fields
    /// for doc_id, fields in docs:
    ///     row = len(doc_ids)
    ///     flen = [0]*n_fields
    ///     for fname, text in fields:
    ///         fi = field_index[fname]
    ///         toks = tokenize(text)
    ///         flen[fi] += len(toks)
    ///         for t in toks:
    ///             postings.setdefault(t, {}).setdefault(row, [0]*n_fields)
    ///             postings[t][row][fi] += 1
    ///     total = [total[i] + flen[i] for i in range(n_fields)]
    ///     doc_ids.append(doc_id); doc_field_len.append(flen)
    /// N = len(doc_ids)
    /// avg = [total[i]/N for i in range(n_fields)]
    /// def idf(t):
    ///     df = len(postings.get(t, {}))
    ///     return 0.0 if df == 0 else math.log(1 + (N - df + 0.5)/(df + 0.5))
    /// def score(row, terms):
    ///     s = 0.0
    ///     for t in terms:
    ///         i = idf(t)
    ///         if i == 0.0: continue
    ///         tp = postings.get(t)
    ///         if tp is None or row not in tp: continue
    ///         tf_hat = 0.0
    ///         for fi, (fname, w, b) in enumerate(config):
    ///             tf = tp[row][fi]
    ///             if tf == 0: continue
    ///             ratio = 0.0 if avg[fi] == 0.0 else doc_field_len[row][fi]/avg[fi]
    ///             tf_hat += w * (tf / (1 - b + b*ratio))
    ///         s += i * tf_hat/(K1 + tf_hat)
    ///     return s
    /// for row, doc_id in enumerate(doc_ids):
    ///     print(doc_id, repr(score(row, ["dd_trace", "handler"])))
    /// ```
    /// Output (frozen, hardcoded below):
    ///   alpha 0.35434438180545286
    ///   beta  0.4088549516640166
    ///   gamma 0.10436246035877784
    #[test]
    fn independent_hardcoded_golden_matches_frozen_python_reference() {
        const TOLERANCE: f64 = 1e-9;

        let config = BM25FConfig::new()
            .with_field("title", 3.0, 0.75)
            .with_field("tags", 2.0, 0.75)
            .with_field("body", 1.0, 0.75);
        let index = BM25FIndex::build(
            &config,
            [
                Document {
                    id: "alpha",
                    fields: vec![
                        ("title", "dd_trace parser"),
                        ("tags", "rust config"),
                        ("body", "span init handler"),
                    ],
                },
                Document {
                    id: "beta",
                    fields: vec![
                        ("title", "span handler"),
                        ("tags", "dd_trace"),
                        ("body", "context retry retry dd_trace"),
                    ],
                },
                Document {
                    id: "gamma",
                    fields: vec![
                        ("title", "handler"),
                        ("tags", "context"),
                        ("body", "span context handler"),
                    ],
                },
            ],
        );

        let results = index.search("dd_trace handler", 10);

        let expected: &[(&str, f64)] = &[
            ("beta", 0.408_854_951_664_016_6),
            ("alpha", 0.354_344_381_805_452_86),
            ("gamma", 0.104_362_460_358_777_84),
        ];

        assert_eq!(results.len(), expected.len());
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

    /// DISCRIMINATING probe for the one place a plausible-but-wrong formula
    /// could slip in: saturating `tf_hat` ONCE, outside the field sum
    /// (correct BM25F) vs. saturating EACH field independently and then
    /// summing (a different, wrong formula). Both `b_f = 0` so
    /// `tf_norm(t,f) = tf(t,f)` exactly (length ratio is multiplied by `b`,
    /// which is `0`) — isolating the saturation-order question from length
    /// normalization entirely.
    ///
    /// One doc (`"has_x"`) matches query term `"x"` with `tf = 3` in BOTH
    /// `title` and `body` (`weight = 1.0` each); a second doc (`"no_x"`)
    /// never matches `"x"`, only present so `idf("x") != 0.0` (`df = 1`,
    /// `N = 2`).
    ///
    /// `tf_hat = 1.0*3 + 1.0*3 = 6.0`.
    /// - Correct (combine-then-saturate): `idf * 6.0/(K1+6.0)`.
    /// - Wrong (per-field-saturate-then-sum): `idf * (3.0/(K1+3.0) +
    ///   3.0/(K1+3.0))`.
    ///
    /// These two formulas diverge by ~40% here (`0.554...` vs. `0.924...`)
    /// — computed independently in Python, hardcoded below. If `tf_hat`
    /// were saturated per-field instead of combined-then-saturated, this
    /// assertion fails.
    #[test]
    fn saturation_is_combine_then_saturate_not_per_field_saturate_then_sum() {
        const TOLERANCE: f64 = 1e-9;
        // idf = ln(1 + (2 - 1 + 0.5)/(1 + 0.5)) = ln(2.0) [N=2, df=1]
        // correct = idf * 6.0/(1.5+6.0)
        const CORRECT: f64 = 0.554_517_744_447_956_2;
        // wrong   = idf * (3.0/(1.5+3.0) + 3.0/(1.5+3.0))
        const WRONG_PER_FIELD_SATURATION: f64 = 0.924_196_240_746_593_7;
        assert!(
            (CORRECT - WRONG_PER_FIELD_SATURATION).abs() > 0.3,
            "sanity: the two candidate formulas must meaningfully diverge for this probe to mean anything"
        );

        let config = BM25FConfig::new()
            .with_field("title", 1.0, 0.0)
            .with_field("body", 1.0, 0.0);
        let index = BM25FIndex::build(
            &config,
            [
                Document {
                    id: "has_x",
                    fields: vec![("title", "x x x"), ("body", "x x x")],
                },
                Document {
                    id: "no_x",
                    fields: vec![("title", "y"), ("body", "y")],
                },
            ],
        );

        let results = index.search("x", 10);
        assert_eq!(results.len(), 1, "only has_x contains the query term");
        assert_eq!(results[0].id, "has_x");
        assert!(
            (results[0].score - CORRECT).abs() < TOLERANCE,
            "score was {}, expected combine-then-saturate value {} (NOT the wrong \
             per-field-saturate-then-sum value {})",
            results[0].score,
            CORRECT,
            WRONG_PER_FIELD_SATURATION
        );
        assert!(
            (results[0].score - WRONG_PER_FIELD_SATURATION).abs() > 0.1,
            "score {} must not match the wrong per-field-saturate-then-sum formula's value {}",
            results[0].score,
            WRONG_PER_FIELD_SATURATION
        );
    }

    /// A negative field weight is rejected at construction, not silently
    /// scored. This is the guard that keeps the `score > 0.0` output filter
    /// safe: without it, a negative weight could drive a genuine match's
    /// `tf_hat` negative and either silently drop the hit, divide by zero
    /// (`tf_hat == -K1`), or produce a spuriously high positive score
    /// (`tf_hat < -K1`) — all latent before this check existed.
    #[test]
    #[should_panic(expected = "weight must be finite and >= 0.0")]
    fn negative_field_weight_is_rejected_at_construction() {
        let _ = BM25FConfig::new().with_field("body", -1.0, 0.75);
    }

    /// A non-finite (`NaN`) weight is rejected: it would poison every score
    /// it touches into `NaN`, defeating the total order.
    #[test]
    #[should_panic(expected = "weight must be finite and >= 0.0")]
    fn nan_field_weight_is_rejected_at_construction() {
        let _ = BM25FConfig::new().with_field("body", f64::NAN, 0.75);
    }

    /// A `b` above `1.0` is rejected: the length-norm denominator
    /// `1 - b + b*ratio` can hit zero/negative outside `[0.0, 1.0]`, the
    /// same `NaN`/negative-score class as a bad weight.
    #[test]
    #[should_panic(expected = "b must be finite and in [0.0, 1.0]")]
    fn out_of_range_b_is_rejected_at_construction() {
        let _ = BM25FConfig::new().with_field("body", 1.0, 1.5);
    }

    /// The domain endpoints (`weight == 0.0`, `b == 0.0`, `b == 1.0`) are
    /// valid and must NOT panic — the check rejects out-of-domain values
    /// only, never the legal boundary the rest of the suite relies on.
    #[test]
    fn domain_boundary_values_are_accepted() {
        let _ = BM25FConfig::new()
            .with_field("zero_weight", 0.0, 0.5)
            .with_field("b_low", 1.0, 0.0)
            .with_field("b_high", 1.0, 1.0);
    }

    /// The `frontmatter_default` config preserves the design's ordinal
    /// ranking: `name`/`id`/`tags` > `description` > `body`.
    #[test]
    fn frontmatter_default_ranks_fields_name_id_tags_above_description_above_body() {
        let config = BM25FConfig::frontmatter_default();
        let weight_of = |field: &str| {
            config
                .fields
                .iter()
                .find(|(n, _)| n == field)
                .map(|(_, w)| w.weight)
                .expect("field is registered")
        };
        for top in ["name", "id", "tags"] {
            assert!(weight_of(top) > weight_of("description"));
        }
        assert!(weight_of("description") > weight_of("body"));
    }
}
