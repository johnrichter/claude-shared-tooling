//! Evaluator conformance suite — the counterpart to `tests/conformance.rs`'s
//! parser coverage, but for [`facetquery::evaluate`].
//!
//! Everything here runs against `FixtureSource`, an in-test [`FacetSource`]
//! that knows nothing about frontmatter or any other document schema — the
//! evaluator crate is meant to compile and test standalone, and this file
//! proves that by never importing anything outside `facetquery` itself.

use std::collections::HashMap;

use facetquery::{
    evaluate, parse, Bound, CmpOp, EvalDiagnostic, Expr, FacetLookup, FacetSource, FacetType,
    MatchResult, Matcher, Predicate, Query, Seg, SetJoin, Term,
};

// ---------------------------------------------------------------------------
// Fixture FacetSource
// ---------------------------------------------------------------------------

/// A fixed, in-memory facet source: a map of facet name to (type, values),
/// plus one text field for bareword full-text matching.
struct FixtureSource {
    facets: HashMap<&'static str, (FacetType, Vec<&'static str>)>,
    text: &'static str,
}

impl FixtureSource {
    fn new(text: &'static str) -> Self {
        Self {
            facets: HashMap::new(),
            text,
        }
    }

    fn with(mut self, name: &'static str, ty: FacetType, values: &[&'static str]) -> Self {
        self.facets.insert(name, (ty, values.to_vec()));
        self
    }
}

impl FacetSource for FixtureSource {
    fn facet(&self, key: &str) -> FacetLookup {
        match self.facets.get(key) {
            None => FacetLookup::Unknown,
            Some((ty, values)) => FacetLookup::Present {
                ty: *ty,
                values: values.iter().map(ToString::to_string).collect(),
            },
        }
    }

    // Case-sensitive substring match, treating `*`/`?` as ordinary wildcard
    // markers the same way facet-value matching does — good enough to prove
    // the evaluator delegates full text to the source rather than handling
    // it itself.
    fn text_matches(&self, term: &Term) -> bool {
        if term.segments.iter().all(|s| matches!(s, Seg::Literal(_))) {
            self.text.contains(term.raw.as_str())
        } else {
            // Any wildcard bareword in these tests is a lone `*` (match
            // everything) — sufficient for what this suite exercises.
            term.segments == vec![Seg::StarWild]
        }
    }
}

// ---------------------------------------------------------------------------
// AST-construction helpers (mirrors tests/conformance.rs's; this file only
// sees facetquery's public API).
// ---------------------------------------------------------------------------

fn lit(raw: &str) -> Term {
    Term {
        raw: raw.to_string(),
        segments: vec![Seg::Literal(raw.to_string())],
    }
}

fn wild(raw: &str, segments: Vec<Seg>) -> Term {
    Term {
        raw: raw.to_string(),
        segments,
    }
}

fn pred(facet: &str, matcher: Matcher) -> Expr {
    Expr::Pred(Predicate {
        facet: Some(facet.to_string()),
        matcher,
    })
}

fn term_pred(facet: &str, raw: &str) -> Expr {
    pred(facet, Matcher::Term(lit(raw)))
}

fn bareword(raw: &str) -> Expr {
    Expr::Pred(Predicate {
        facet: None,
        matcher: Matcher::Term(lit(raw)),
    })
}

fn query(expr: Expr) -> Query {
    Query { expr }
}

fn run(expr: Expr, source: &FixtureSource) -> MatchResult {
    evaluate(&query(expr), source)
}

// ===========================================================================
// Term matcher: equality + wildcard
// ===========================================================================

#[test]
fn term_equality_matches_exact_value() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let result = run(term_pred("status", "active"), &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["status"]);
}

#[test]
fn term_equality_rejects_partial_value() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let result = run(term_pred("status", "activ"), &source);
    assert!(!result.matched);
    assert!(result.matched_facets.is_empty());
}

#[test]
fn term_star_wildcard_matches_any_run_including_empty() {
    let source = FixtureSource::new("").with("service", FacetType::String, &["checkout-api"]);
    let t = wild(
        "check*",
        vec![Seg::Literal("check".to_string()), Seg::StarWild],
    );
    let result = run(pred("service", Matcher::Term(t)), &source);
    assert!(result.matched);

    // `*` also matches zero characters — an exact-prefix value still hits.
    let source2 = FixtureSource::new("").with("service", FacetType::String, &["check"]);
    let t2 = wild(
        "check*",
        vec![Seg::Literal("check".to_string()), Seg::StarWild],
    );
    assert!(run(pred("service", Matcher::Term(t2)), &source2).matched);
}

#[test]
fn term_question_wildcard_matches_exactly_one_char() {
    let source = FixtureSource::new("").with("code", FacetType::String, &["a1c"]);
    let t = wild(
        "a?c",
        vec![
            Seg::Literal("a".to_string()),
            Seg::QuestionWild,
            Seg::Literal("c".to_string()),
        ],
    );
    assert!(run(pred("code", Matcher::Term(t)), &source).matched);

    // `?` matches exactly one char, never zero or two.
    let source_empty = FixtureSource::new("").with("code", FacetType::String, &["ac"]);
    let t2 = wild(
        "a?c",
        vec![
            Seg::Literal("a".to_string()),
            Seg::QuestionWild,
            Seg::Literal("c".to_string()),
        ],
    );
    assert!(!run(pred("code", Matcher::Term(t2)), &source_empty).matched);
}

#[test]
fn term_matches_if_any_value_matches() {
    let source = FixtureSource::new("").with("tag", FacetType::String, &["red", "blue", "green"]);
    let result = run(term_pred("tag", "blue"), &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["tag"]);
}

// ===========================================================================
// Set matcher: OR / AND / single-member
// ===========================================================================

#[test]
fn set_or_matches_if_any_term_hits_any_value() {
    let source = FixtureSource::new("").with("tag", FacetType::String, &["red"]);
    let matcher = Matcher::Set {
        terms: vec![lit("red"), lit("blue")],
        join: SetJoin::Or,
    };
    assert!(run(pred("tag", matcher), &source).matched);
}

#[test]
fn set_and_requires_every_term_satisfied_by_some_value() {
    let source = FixtureSource::new("").with("tag", FacetType::String, &["red", "blue"]);
    let matcher = Matcher::Set {
        terms: vec![lit("red"), lit("blue")],
        join: SetJoin::And,
    };
    assert!(run(pred("tag", matcher), &source).matched);

    let matcher_miss = Matcher::Set {
        terms: vec![lit("red"), lit("green")],
        join: SetJoin::And,
    };
    assert!(!run(pred("tag", matcher_miss), &source).matched);
}

#[test]
fn single_member_set_behaves_as_the_member_itself() {
    let source = FixtureSource::new("").with("tag", FacetType::String, &["red"]);
    let as_or = Matcher::Set {
        terms: vec![lit("red")],
        join: SetJoin::Or,
    };
    let as_and = Matcher::Set {
        terms: vec![lit("red")],
        join: SetJoin::And,
    };
    assert!(run(pred("tag", as_or), &source).matched);
    assert!(run(pred("tag", as_and), &source).matched);
}

// ===========================================================================
// Exists
// ===========================================================================

#[test]
fn exists_matches_when_facet_present_with_any_value() {
    let source = FixtureSource::new("").with("owner", FacetType::String, &["alice"]);
    assert!(run(pred("owner", Matcher::Exists), &source).matched);
}

#[test]
fn not_exists_matches_when_facet_absent() {
    let source = FixtureSource::new("");
    let result = run(Expr::Not(Box::new(pred("owner", Matcher::Exists))), &source);
    // The facet is unknown, so the inner `Exists` predicate is a no-match
    // (with an UnknownFacet diagnostic) and NOT of that is a match.
    assert!(result.matched);
    assert_eq!(
        result.diagnostics,
        vec![EvalDiagnostic::UnknownFacet("owner".to_string())]
    );
}

#[test]
fn exists_does_not_match_a_schema_known_facet_with_zero_values() {
    // Present-but-valueless is a real source shape (a schema-known facet
    // the current document just doesn't set) — pins that Exists requires
    // an actual value, not merely a `Present` lookup result, and that this
    // case raises no `UnknownFacet` diagnostic (the source did know the
    // name).
    let source = FixtureSource::new("").with("owner", FacetType::String, &[]);
    let result = run(pred("owner", Matcher::Exists), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Range / comparison — type-gated
// ===========================================================================

#[test]
fn range_on_numeric_facet_is_eligible() {
    let source = FixtureSource::new("").with("count", FacetType::Numeric, &["42"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("10")),
        hi: Bound::Value(lit("100")),
    };
    let result = run(pred("count", matcher), &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["count"]);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn range_on_date_facet_compares_as_real_dates() {
    let source = FixtureSource::new("").with("created", FacetType::Date, &["2026-06-15"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-01-01")),
        hi: Bound::Value(lit("2026-12-31")),
    };
    assert!(run(pred("created", matcher), &source).matched);

    let out_of_range = Matcher::Range {
        lo: Bound::Value(lit("2027-01-01")),
        hi: Bound::Unbounded,
    };
    assert!(!run(pred("created", out_of_range), &source).matched);
}

#[test]
fn range_unbounded_side_is_open() {
    let source = FixtureSource::new("").with("count", FacetType::Numeric, &["5"]);
    let matcher = Matcher::Range {
        lo: Bound::Unbounded,
        hi: Bound::Value(lit("10")),
    };
    assert!(run(pred("count", matcher), &source).matched);
}

#[test]
fn cmp_operators_on_ordered_type() {
    let source = FixtureSource::new("").with("count", FacetType::Numeric, &["50"]);
    assert!(
        run(
            pred(
                "count",
                Matcher::Cmp {
                    op: CmpOp::Gt,
                    term: lit("10")
                }
            ),
            &source
        )
        .matched
    );
    assert!(
        !run(
            pred(
                "count",
                Matcher::Cmp {
                    op: CmpOp::Lt,
                    term: lit("10")
                }
            ),
            &source
        )
        .matched
    );
    assert!(
        run(
            pred(
                "count",
                Matcher::Cmp {
                    op: CmpOp::Ge,
                    term: lit("50")
                }
            ),
            &source
        )
        .matched
    );
    assert!(
        run(
            pred(
                "count",
                Matcher::Cmp {
                    op: CmpOp::Le,
                    term: lit("50")
                }
            ),
            &source
        )
        .matched
    );
}

#[test]
fn range_on_string_facet_is_rangeonnonordered_diagnostic_and_no_match() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("a")),
        hi: Bound::Value(lit("z")),
    };
    let result = run(pred("status", matcher), &source);
    assert!(!result.matched);
    assert_eq!(
        result.diagnostics,
        vec![EvalDiagnostic::RangeOnNonOrdered {
            facet: "status".to_string(),
            ty: FacetType::String,
        }]
    );
}

#[test]
fn cmp_on_string_facet_is_rangeonnonordered_diagnostic_and_no_match() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Gt,
        term: lit("a"),
    };
    let result = run(pred("status", matcher), &source);
    assert!(!result.matched);
    assert_eq!(
        result.diagnostics,
        vec![EvalDiagnostic::RangeOnNonOrdered {
            facet: "status".to_string(),
            ty: FacetType::String,
        }]
    );
}

#[test]
fn value_that_does_not_parse_for_ordered_type_is_no_match_not_panic() {
    let source = FixtureSource::new("").with("count", FacetType::Numeric, &["not-a-number"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("0")),
        hi: Bound::Value(lit("100")),
    };
    let result = run(pred("count", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn comparison_operand_that_does_not_parse_is_no_match_not_panic() {
    let source = FixtureSource::new("").with("count", FacetType::Numeric, &["50"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Gt,
        term: lit("not-a-number"),
    };
    assert!(!run(pred("count", matcher), &source).matched);
}

// ===========================================================================
// Unknown facet
// ===========================================================================

#[test]
fn unknown_facet_is_diagnostic_and_no_match_but_query_still_evaluates() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let result = run(
        Expr::And(vec![term_pred("status", "active"), term_pred("ghost", "x")]),
        &source,
    );
    assert!(!result.matched);
    assert_eq!(result.matched_facets, vec!["status"]);
    assert_eq!(
        result.diagnostics,
        vec![EvalDiagnostic::UnknownFacet("ghost".to_string())]
    );
}

// ===========================================================================
// Bareword / full text
// ===========================================================================

#[test]
fn bareword_delegates_to_text_matches() {
    let source = FixtureSource::new("the quick brown fox");
    assert!(run(bareword("quick"), &source).matched);
    assert!(!run(bareword("slow"), &source).matched);
}

#[test]
fn bareword_never_contributes_matched_facets() {
    let source = FixtureSource::new("the quick brown fox");
    let result = run(bareword("quick"), &source);
    assert!(result.matched);
    assert!(result.matched_facets.is_empty());
}

// ===========================================================================
// Boolean combinators
// ===========================================================================

#[test]
fn and_of_empty_vec_is_match_all() {
    let source = FixtureSource::new("");
    let result = run(Expr::And(vec![]), &source);
    assert!(result.matched);
    assert!(result.matched_facets.is_empty());
    assert!(result.diagnostics.is_empty());
}

#[test]
fn or_matches_if_any_branch_matches() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let result = run(
        Expr::Or(vec![
            term_pred("status", "inactive"),
            term_pred("status", "active"),
        ]),
        &source,
    );
    assert!(result.matched);
}

#[test]
fn not_negates_and_never_contributes_matched_facets() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let matched_case = run(
        Expr::Not(Box::new(term_pred("status", "inactive"))),
        &source,
    );
    assert!(matched_case.matched);
    assert!(matched_case.matched_facets.is_empty());

    let unmatched_case = run(Expr::Not(Box::new(term_pred("status", "active"))), &source);
    assert!(!unmatched_case.matched);
    assert!(unmatched_case.matched_facets.is_empty());
}

#[test]
fn group_is_transparent_to_evaluation() {
    let source = FixtureSource::new("").with("status", FacetType::String, &["active"]);
    let grouped = run(
        Expr::Group(Box::new(term_pred("status", "active"))),
        &source,
    );
    let ungrouped = run(term_pred("status", "active"), &source);
    assert_eq!(grouped, ungrouped);
}

// ===========================================================================
// End-to-end: parse then evaluate
// ===========================================================================

#[test]
fn compound_query_end_to_end() {
    let source = FixtureSource::new("checkout flow")
        .with("status", FacetType::String, &["active"])
        .with("priority", FacetType::Numeric, &["3"]);

    let parsed = parse(r"status:active AND priority:>2 AND checkout").expect("valid query");
    let result = evaluate(&parsed, &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["status", "priority"]);
    assert!(result.diagnostics.is_empty());

    let parsed_not = parse("status:active AND NOT priority:>10").expect("valid query");
    let result_not = evaluate(&parsed_not, &source);
    assert!(result_not.matched);
    // The NOT branch never contributes to matched_facets even though its
    // inner predicate was itself a well-formed, present-facet no-match.
    assert_eq!(result_not.matched_facets, vec!["status"]);
}

// ===========================================================================
// matched_facets: order + dedup
// ===========================================================================

#[test]
fn matched_facets_are_deduplicated_in_first_encounter_order() {
    let source = FixtureSource::new("")
        .with("a", FacetType::String, &["1"])
        .with("b", FacetType::String, &["1"]);
    let result = run(
        Expr::And(vec![
            term_pred("a", "1"),
            term_pred("b", "1"),
            term_pred("a", "1"),
        ]),
        &source,
    );
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["a", "b"]);
}

// ===========================================================================
// diagnostics: order
// ===========================================================================

#[test]
fn diagnostics_are_emitted_in_left_to_right_order() {
    let source = FixtureSource::new("");
    let result = run(
        Expr::And(vec![term_pred("first", "x"), term_pred("second", "y")]),
        &source,
    );
    assert_eq!(
        result.diagnostics,
        vec![
            EvalDiagnostic::UnknownFacet("first".to_string()),
            EvalDiagnostic::UnknownFacet("second".to_string()),
        ]
    );
}

// ===========================================================================
// Determinism
// ===========================================================================

#[test]
fn repeated_evaluation_is_equal() {
    let source = FixtureSource::new("checkout flow")
        .with("status", FacetType::String, &["active"])
        .with("priority", FacetType::Numeric, &["3"]);
    let parsed = parse("status:active AND priority:>2 AND checkout").expect("valid query");
    let first = evaluate(&parsed, &source);
    let second = evaluate(&parsed, &source);
    assert_eq!(first, second);
}

#[test]
fn repeated_evaluation_is_equal_across_diagnostics_and_unicode() {
    // A second determinism probe with a shape that touches both diagnostic
    // kinds and a multi-byte value, so determinism isn't only pinned on the
    // "everything matches cleanly" happy path above.
    let source = FixtureSource::new("")
        .with("status", FacetType::String, &["café"])
        .with("count", FacetType::Numeric, &["3"]);
    let expr = Expr::And(vec![
        term_pred("status", "café"),
        pred(
            "status",
            Matcher::Cmp {
                op: CmpOp::Gt,
                term: lit("a"),
            },
        ),
        term_pred("ghost", "x"),
    ]);
    let q = query(expr);
    let first = evaluate(&q, &source);
    let second = evaluate(&q, &source);
    assert_eq!(first, second);
    assert_eq!(first.matched_facets, vec!["status"]);
    assert_eq!(
        first.diagnostics,
        vec![
            EvalDiagnostic::RangeOnNonOrdered {
                facet: "status".to_string(),
                ty: FacetType::String,
            },
            EvalDiagnostic::UnknownFacet("ghost".to_string()),
        ]
    );
}

// ===========================================================================
// Unicode / multi-byte values (glob walks chars, not bytes)
// ===========================================================================

#[test]
fn question_wildcard_counts_chars_not_bytes_on_multibyte_value() {
    // "café" has 4 chars but 5 bytes (é is 2 bytes in UTF-8) -- if the glob
    // walk counted bytes, "caf?" (4 tokens) would fail to consume the whole
    // value; if it counts chars, it matches exactly.
    let source = FixtureSource::new("").with("name", FacetType::String, &["café"]);
    let t = wild(
        "caf?",
        vec![Seg::Literal("caf".to_string()), Seg::QuestionWild],
    );
    assert!(run(pred("name", Matcher::Term(t)), &source).matched);

    // "café" is exactly 4 chars; a 5-token pattern ("caf" + 2 wildcards)
    // demands 5 chars and must NOT match -- if `?` counted bytes instead
    // ("café" is 5 bytes, since é is 2 bytes in UTF-8), this would wrongly
    // match.
    let t2 = wild(
        "caf??",
        vec![
            Seg::Literal("caf".to_string()),
            Seg::QuestionWild,
            Seg::QuestionWild,
        ],
    );
    let source2 = FixtureSource::new("").with("name", FacetType::String, &["café"]);
    assert!(!run(pred("name", Matcher::Term(t2)), &source2).matched);
}

#[test]
fn star_wildcard_matches_multibyte_value_as_single_run() {
    let source = FixtureSource::new("").with("emoji", FacetType::String, &["🦀🦀🦀"]);
    let t = wild("*", vec![Seg::StarWild]);
    // A lone `*` on a facet value is the Exists shape at the parser level,
    // but constructed directly here it stays Matcher::Term -- still must
    // match any multi-byte value without panicking on a char boundary.
    assert!(run(pred("emoji", Matcher::Term(t)), &source).matched);
}

// ===========================================================================
// glob_match adversarial cases (the nontrivial algorithm)
// ===========================================================================

fn wild_pattern(tokens: &[char]) -> Term {
    // Builds a Term whose segments alternate: any run of literal chars
    // becomes one Seg::Literal, `*`/`?` each become their own wildcard Seg.
    let mut segments = Vec::new();
    let mut lit_buf = String::new();
    let mut raw = String::new();
    for &c in tokens {
        raw.push(c);
        match c {
            '*' => {
                if !lit_buf.is_empty() {
                    segments.push(Seg::Literal(std::mem::take(&mut lit_buf)));
                }
                segments.push(Seg::StarWild);
            }
            '?' => {
                if !lit_buf.is_empty() {
                    segments.push(Seg::Literal(std::mem::take(&mut lit_buf)));
                }
                segments.push(Seg::QuestionWild);
            }
            other => lit_buf.push(other),
        }
    }
    if !lit_buf.is_empty() {
        segments.push(Seg::Literal(lit_buf));
    }
    Term { raw, segments }
}

/// A `FacetSource` over one owned value -- `FixtureSource` only stores
/// `&'static str`, which can't hold the fuzz-generated/composed strings this
/// section needs.
struct OwnedSource {
    value: String,
}

impl FacetSource for OwnedSource {
    fn facet(&self, key: &str) -> FacetLookup {
        if key == "f" {
            FacetLookup::Present {
                ty: FacetType::String,
                values: vec![self.value.clone()],
            }
        } else {
            FacetLookup::Unknown
        }
    }
    fn text_matches(&self, _term: &Term) -> bool {
        false
    }
}

fn matches(pattern: &str, value: &str) -> bool {
    let pattern_tokens: Vec<char> = pattern.chars().collect();
    let term = wild_pattern(&pattern_tokens);
    let source = OwnedSource {
        value: value.to_string(),
    };
    let result = evaluate(&query(pred("f", Matcher::Term(term))), &source);
    result.matched
}

#[test]
fn glob_leading_trailing_mid_star() {
    assert!(matches("*bc", "abc"));
    assert!(matches("ab*", "abc"));
    assert!(matches("a*c", "abc"));
    assert!(matches("a*c", "ac")); // * matches empty in the middle too
    assert!(!matches("a*c", "abd"));
}

#[test]
fn glob_many_stars_collapse_correctly() {
    // Runs of `*` are semantically one `*` -- must not change the result
    // vs a single star, and must not blow up walking a long pattern.
    assert!(matches("****", "anything at all"));
    assert!(matches("****", ""));
    assert!(matches("*a*a*a*", "aaa"));
    assert!(matches("*a*a*a*", "xaxaxax"));
    assert!(!matches("*a*a*a*", "aa")); // needs 3 'a's, only has 2
}

#[test]
fn glob_question_at_boundaries() {
    assert!(matches("?bc", "abc"));
    assert!(matches("ab?", "abc"));
    assert!(!matches("?bc", "bc")); // ? requires exactly one char, not zero
    assert!(!matches("?", "")); // ? never matches empty
}

#[test]
fn glob_empty_pattern_vs_empty_value() {
    assert!(matches("", "")); // empty pattern matches only empty value
    assert!(!matches("", "x"));
    assert!(matches("*", "")); // lone * matches empty value too
}

#[test]
fn glob_no_catastrophic_backtracking_on_long_pathological_input() {
    // Classic ReDoS-style shape for naive backtracking glob implementations:
    // many stars each separated by a repeated character, against a long
    // value with no full match -- a quadratic/exponential implementation
    // would visibly hang here; the two-pointer algorithm in eval.rs is
    // linear and should return well within the test harness's own timeout.
    let pattern = "a*".repeat(200) + "b";
    let value = "a".repeat(10_000);
    let start = std::time::Instant::now();
    let hit = matches(&pattern, &value);
    let elapsed = start.elapsed();
    assert!(!hit); // value has no trailing 'b', can never match
    assert!(
        elapsed < std::time::Duration::from_secs(1),
        "glob_match took {elapsed:?}, suspected non-linear blowup"
    );
}

// ===========================================================================
// glob_match fuzz -- no panic, always terminates
// ===========================================================================

struct Xorshift64(u64);

impl Xorshift64 {
    fn new(seed: u64) -> Self {
        Self(seed | 1)
    }
    fn next_u64(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x << 13;
        x ^= x >> 7;
        x ^= x << 17;
        self.0 = x;
        x
    }
    fn next_range(&mut self, n: usize) -> usize {
        let capped = self.next_u64() % (n.max(1) as u64);
        usize::try_from(capped).unwrap_or(0)
    }
}

const GLOB_FUZZ_ALPHABET: &[char] = &['a', 'b', '*', '?', '🦀', 'é'];

fn random_glob_string(rng: &mut Xorshift64, len: usize) -> String {
    (0..len)
        .map(|_| GLOB_FUZZ_ALPHABET[rng.next_range(GLOB_FUZZ_ALPHABET.len())])
        .collect()
}

#[test]
fn glob_fuzz_random_pattern_and_value_never_panics_and_terminates() {
    let mut rng = Xorshift64::new(0xFACE_7A57_0000_0001);
    for trial in 0..5_000 {
        let pattern_len = rng.next_range(30);
        let pattern = random_glob_string(&mut rng, pattern_len);
        let value_len = rng.next_range(30);
        let value = random_glob_string(&mut rng, value_len);
        let start = std::time::Instant::now();
        let result = std::panic::catch_unwind(|| matches(&pattern, &value));
        let elapsed = start.elapsed();
        assert!(
            result.is_ok(),
            "trial {trial}: glob panicked pattern={pattern:?} value={value:?}"
        );
        assert!(
            elapsed < std::time::Duration::from_millis(200),
            "trial {trial}: glob took {elapsed:?} pattern={pattern:?} value={value:?}"
        );
    }
}

// ===========================================================================
// matched_facets: SE deviation -- recorded per-matcher-kind, independent of
// the enclosing combinator's final boolean (an AND that overall fails still
// lists the sub-predicates that individually matched).
// ===========================================================================

#[test]
fn matched_facets_recorded_even_when_enclosing_and_overall_fails() {
    let source = FixtureSource::new("")
        .with("status", FacetType::String, &["active"])
        .with("priority", FacetType::Numeric, &["3"]);
    let result = run(
        Expr::And(vec![
            term_pred("status", "active"),     // matches
            term_pred("status", "inactive"),   // does not match
            pred("priority", Matcher::Exists), // matches
        ]),
        &source,
    );
    assert!(!result.matched); // overall AND fails (second conjunct fails)
                              // Yet both individually-satisfied predicates still show up.
    assert_eq!(result.matched_facets, vec!["status", "priority"]);
}

#[test]
fn matched_facets_recorded_for_every_matcher_kind_not_just_term() {
    let source = FixtureSource::new("")
        .with("tag", FacetType::String, &["red"])
        .with("count", FacetType::Numeric, &["50"])
        .with("owner", FacetType::String, &["alice"]);
    let result = run(
        Expr::And(vec![
            pred(
                "tag",
                Matcher::Set {
                    terms: vec![lit("red")],
                    join: SetJoin::Or,
                },
            ),
            pred("owner", Matcher::Exists),
            pred(
                "count",
                Matcher::Cmp {
                    op: CmpOp::Gt,
                    term: lit("10"),
                },
            ),
        ]),
        &source,
    );
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["tag", "owner", "count"]);
}

// ===========================================================================
// Set AND: each term satisfied by SOME value, not necessarily the SAME value
// ===========================================================================

#[test]
fn set_and_allows_different_terms_satisfied_by_different_values() {
    let source = FixtureSource::new("").with("tag", FacetType::String, &["red", "blue"]);
    // "red" is satisfied only by the first value, "blue" only by the second
    // -- AND must still hold since each term individually has a satisfying
    // value, even though no single value satisfies both terms at once.
    let matcher = Matcher::Set {
        terms: vec![lit("red"), lit("blue")],
        join: SetJoin::And,
    };
    assert!(run(pred("tag", matcher), &source).matched);
}

// ===========================================================================
// Range/Cmp on Numeric: negative and fractional values
// ===========================================================================

#[test]
fn range_and_cmp_on_numeric_handle_negative_and_fractional_values() {
    let source = FixtureSource::new("").with("delta", FacetType::Numeric, &["-3.5"]);
    let in_range = Matcher::Range {
        lo: Bound::Value(lit("-10")),
        hi: Bound::Value(lit("0")),
    };
    assert!(run(pred("delta", in_range), &source).matched);

    let out_of_range = Matcher::Range {
        lo: Bound::Value(lit("0")),
        hi: Bound::Unbounded,
    };
    assert!(!run(pred("delta", out_of_range), &source).matched);

    assert!(
        run(
            pred(
                "delta",
                Matcher::Cmp {
                    op: CmpOp::Lt,
                    term: lit("0")
                }
            ),
            &source
        )
        .matched
    );
}

// ===========================================================================
// Date facet: unparseable value is a no-match, not a panic
// ===========================================================================

#[test]
fn date_value_that_does_not_parse_is_no_match_not_panic() {
    let source = FixtureSource::new("").with("created", FacetType::Date, &["notadate"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2000-01-01")),
        hi: Bound::Value(lit("2100-01-01")),
    };
    let result = std::panic::catch_unwind(|| run(pred("created", matcher), &source));
    assert!(result.is_ok());
    assert!(!result.unwrap().matched);
}

// ===========================================================================
// Calendar validation — a shape-valid but impossible date parses as a real
// `time::Date`, so it must be rejected the same way a malformed value is:
// silent no-match, no diagnostic, no panic. Covers a point `Date` facet, a
// `DateInterval` endpoint, and a `DateInterval` query operand.
// ===========================================================================

const CALENDAR_INVALID_DATES: &[&str] = &[
    "2026-13-01", // month 13
    "2026-00-01", // month 0
    "2026-01-32", // day 32
    "2026-01-00", // day 0
    "2026-02-30", // no such day in February
    "2025-02-29", // 2025 is not a leap year
];

#[test]
fn calendar_invalid_point_date_value_is_no_match() {
    for invalid in CALENDAR_INVALID_DATES {
        let source = FixtureSource::new("").with("created", FacetType::Date, &[invalid]);
        let matcher = Matcher::Range {
            lo: Bound::Unbounded,
            hi: Bound::Unbounded,
        };
        let result = run(pred("created", matcher), &source);
        assert!(!result.matched, "invalid={invalid:?}");
        assert!(result.diagnostics.is_empty(), "invalid={invalid:?}");
    }
}

#[test]
fn calendar_invalid_point_date_query_operand_is_no_match() {
    let source = FixtureSource::new("").with("created", FacetType::Date, &["2026-06-15"]);
    for invalid in CALENDAR_INVALID_DATES {
        let matcher = Matcher::Cmp {
            op: CmpOp::Gt,
            term: lit(invalid),
        };
        let result = run(pred("created", matcher), &source);
        assert!(!result.matched, "invalid={invalid:?}");
        assert!(result.diagnostics.is_empty(), "invalid={invalid:?}");
    }
}

#[test]
fn leap_day_on_a_leap_year_is_a_valid_date_and_matches() {
    let source = FixtureSource::new("").with("created", FacetType::Date, &["2024-02-29"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2024-01-01")),
        hi: Bound::Value(lit("2024-12-31")),
    };
    assert!(run(pred("created", matcher), &source).matched);
}

#[test]
fn real_calendar_ordering_across_year_and_month_boundaries() {
    let source = FixtureSource::new("").with(
        "created",
        FacetType::Date,
        &["2026-01-01", "2026-02-01", "2026-12-31"],
    );
    assert!(
        run(
            pred(
                "created",
                Matcher::Cmp {
                    op: CmpOp::Lt,
                    term: lit("2026-02-01")
                }
            ),
            &source
        )
        .matched
    );
    assert!(
        run(
            pred(
                "created",
                Matcher::Cmp {
                    op: CmpOp::Gt,
                    term: lit("2026-02-01")
                }
            ),
            &source
        )
        .matched
    );
    // Every stored value satisfies this range -- proves the endpoints
    // compare as real dates spanning a year and a month boundary, not as
    // lexical strings that happen to agree with calendar order here.
    let all_in_range = Matcher::Range {
        lo: Bound::Value(lit("2026-01-01")),
        hi: Bound::Value(lit("2026-12-31")),
    };
    let result = run(pred("created", all_in_range), &source);
    assert!(result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Purity / no I/O, clock, or global state (grep-level confirmation lives in
// the review report; this test only pins the observable contract: same
// inputs called many times in a loop from different call sites never drift).
// ===========================================================================

#[test]
fn evaluate_is_referentially_transparent_across_many_repeated_calls() {
    let source = FixtureSource::new("checkout flow")
        .with("status", FacetType::String, &["active"])
        .with("priority", FacetType::Numeric, &["3"]);
    let parsed = parse("status:active AND priority:>2 AND checkout").expect("valid query");
    let baseline = evaluate(&parsed, &source);
    for _ in 0..1_000 {
        assert_eq!(evaluate(&parsed, &source), baseline);
    }
}
