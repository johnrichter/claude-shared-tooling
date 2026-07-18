//! `FacetType::DateInterval` conformance suite — the match-table cases from
//! the language spec's "Date intervals" section, plus the malformed-input
//! and no-panic guarantees every facet type shares.
//!
//! Runs against a fixture [`FacetSource`] with no frontmatter/navigator
//! dependency, mirroring `tests/eval.rs`'s own fixture.

use std::collections::HashMap;

use facetquery::{
    evaluate, Bound, CmpOp, Expr, FacetLookup, FacetSource, FacetType, MatchResult, Matcher,
    Predicate, Query, Seg, SetJoin, Term,
};

// ---------------------------------------------------------------------------
// Fixture FacetSource
// ---------------------------------------------------------------------------

struct FixtureSource {
    facets: HashMap<&'static str, (FacetType, Vec<&'static str>)>,
}

impl FixtureSource {
    fn new() -> Self {
        Self {
            facets: HashMap::new(),
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

    fn text_matches(&self, _term: &Term) -> bool {
        false
    }
}

// ---------------------------------------------------------------------------
// AST-construction helpers
// ---------------------------------------------------------------------------

fn lit(raw: &str) -> Term {
    Term {
        raw: raw.to_string(),
        segments: vec![Seg::Literal(raw.to_string())],
    }
}

fn pred(facet: &str, matcher: Matcher) -> Expr {
    Expr::Pred(Predicate {
        facet: Some(facet.to_string()),
        matcher,
    })
}

fn run(expr: Expr, source: &impl FacetSource) -> MatchResult {
    evaluate(&Query { expr }, source)
}

fn period_source(values: &[&'static str]) -> FixtureSource {
    FixtureSource::new().with("period", FacetType::DateInterval, values)
}

// ===========================================================================
// Term matcher: single date and A/B interval operands
// ===========================================================================

#[test]
fn term_single_date_matches_when_within_stored_interval() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    assert!(run(pred("period", Matcher::Term(lit("2026-05-15"))), &source).matched);
}

#[test]
fn term_single_date_misses_outside_stored_interval() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    assert!(!run(pred("period", Matcher::Term(lit("2026-07-01"))), &source).matched);
}

#[test]
fn term_interval_operand_matches_on_overlap() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let result = run(
        pred("period", Matcher::Term(lit("2026-06-01/2026-08-01"))),
        &source,
    );
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["period"]);
}

#[test]
fn term_interval_operand_misses_disjoint_interval() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    assert!(
        !run(
            pred("period", Matcher::Term(lit("2026-07-01/2026-08-01"))),
            &source
        )
        .matched
    );
}

// ===========================================================================
// Boundary inclusivity: touching endpoints match, adjacent-but-not-touching
// endpoints miss
// ===========================================================================

#[test]
fn touching_endpoint_matches_both_directions() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    // Query interval starts exactly on the stored interval's end.
    assert!(
        run(
            pred("period", Matcher::Term(lit("2026-06-30/2026-07-15"))),
            &source
        )
        .matched
    );
    // Query interval ends exactly on the stored interval's start.
    assert!(
        run(
            pred("period", Matcher::Term(lit("2026-02-01/2026-04-01"))),
            &source
        )
        .matched
    );
}

#[test]
fn disjoint_by_one_day_misses() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    assert!(
        !run(
            pred("period", Matcher::Term(lit("2026-07-01/2026-08-01"))),
            &source
        )
        .matched
    );
}

// ===========================================================================
// Range matcher: overlap, unbounded sides, [* TO *]
// ===========================================================================

#[test]
fn range_matches_on_overlap() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-06-01")),
        hi: Bound::Value(lit("2026-12-31")),
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn range_misses_when_disjoint() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2027-01-01")),
        hi: Bound::Value(lit("2027-12-31")),
    };
    assert!(!run(pred("period", matcher), &source).matched);
}

#[test]
fn range_one_sided_unbounded_lo() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Unbounded,
        hi: Bound::Value(lit("2026-05-01")),
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn range_one_sided_unbounded_hi() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-06-01")),
        hi: Bound::Unbounded,
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn range_both_unbounded_matches_every_well_formed_stored_value() {
    let source = period_source(&["2026-04-01/2026-06-30", "1999-01-01/1999-01-02"]);
    let matcher = Matcher::Range {
        lo: Bound::Unbounded,
        hi: Bound::Unbounded,
    };
    let result = run(pred("period", matcher), &source);
    assert!(result.matched);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn range_is_ordered_and_never_raises_range_on_non_ordered() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-01-01")),
        hi: Bound::Value(lit("2026-12-31")),
    };
    let result = run(pred("period", matcher), &source);
    assert!(result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Cmp matcher — the worked case plus each operator
// ===========================================================================

#[test]
fn cmp_gt_worked_case_matches_via_far_endpoint() {
    // spec worked case: `>2026-01-01` matches a stored interval whose end
    // (2026-02-01) is past the operand, even though the interval started
    // before it.
    let source = period_source(&["2025-12-01/2026-02-01"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Gt,
        term: lit("2026-01-01"),
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn cmp_ge_matches_when_end_equals_operand() {
    let source = period_source(&["2025-12-01/2026-01-01"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Ge,
        term: lit("2026-01-01"),
    };
    assert!(run(pred("period", matcher), &source).matched);
    let matcher_gt = Matcher::Cmp {
        op: CmpOp::Gt,
        term: lit("2026-01-01"),
    };
    assert!(!run(pred("period", matcher_gt), &source).matched);
}

#[test]
fn cmp_lt_matches_when_start_is_before_operand() {
    let source = period_source(&["2026-01-01/2026-03-01"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Lt,
        term: lit("2026-02-01"),
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn cmp_le_matches_when_start_equals_operand() {
    let source = period_source(&["2026-01-01/2026-03-01"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Le,
        term: lit("2026-01-01"),
    };
    assert!(run(pred("period", matcher), &source).matched);
    let matcher_lt = Matcher::Cmp {
        op: CmpOp::Lt,
        term: lit("2026-01-01"),
    };
    assert!(!run(pred("period", matcher_lt), &source).matched);
}

// ===========================================================================
// Set matcher: OR / AND over date-interval members
// ===========================================================================

#[test]
fn set_or_matches_if_any_member_window_overlaps() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Set {
        terms: vec![lit("2020-01-01"), lit("2026-05-01")],
        join: SetJoin::Or,
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn set_and_requires_every_member_satisfied_by_some_value() {
    let source = period_source(&["2026-04-01/2026-06-30", "2030-01-01/2030-02-01"]);
    let matcher = Matcher::Set {
        terms: vec![lit("2026-05-01"), lit("2030-01-15")],
        join: SetJoin::And,
    };
    assert!(run(pred("period", matcher), &source).matched);

    let matcher_miss = Matcher::Set {
        terms: vec![lit("2026-05-01"), lit("1999-01-01")],
        join: SetJoin::And,
    };
    assert!(!run(pred("period", matcher_miss), &source).matched);
}

#[test]
fn set_or_with_interval_members_matches_if_any_member_window_overlaps() {
    // Members are themselves A/B intervals (not bare dates) — each collapses
    // to its own window before the OR-join checks any-member overlap.
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Set {
        terms: vec![
            lit("1999-01-01/1999-12-31"), // disjoint interval member
            lit("2026-05-01/2026-05-15"), // overlapping interval member
        ],
        join: SetJoin::Or,
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn set_or_with_interval_members_misses_when_every_member_window_disjoint() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Set {
        terms: vec![lit("1999-01-01/1999-12-31"), lit("2030-01-01/2030-02-01")],
        join: SetJoin::Or,
    };
    assert!(!run(pred("period", matcher), &source).matched);
}

#[test]
fn set_and_with_interval_members_across_multiple_stored_values() {
    // AND requires every member to be satisfied by SOME stored value, not
    // necessarily the same one -- each member here is an A/B interval, and
    // each is satisfied by a different stored interval.
    let source = period_source(&["2026-04-01/2026-06-30", "2030-01-01/2030-02-01"]);
    let matcher = Matcher::Set {
        terms: vec![lit("2026-05-01/2026-05-15"), lit("2030-01-10/2030-01-20")],
        join: SetJoin::And,
    };
    assert!(run(pred("period", matcher), &source).matched);

    // Same members, but one member's window is disjoint from every stored
    // value -- AND must miss even though the other member alone would hit.
    let matcher_miss = Matcher::Set {
        terms: vec![lit("2026-05-01/2026-05-15"), lit("1999-01-01/1999-02-01")],
        join: SetJoin::And,
    };
    assert!(!run(pred("period", matcher_miss), &source).matched);
}

#[test]
fn set_mixing_bare_date_and_interval_members_in_one_set() {
    // A single Set may mix a bare-date member with an A/B-interval member --
    // each collapses to its own window independently under the same join.
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Set {
        terms: vec![lit("2026-05-01"), lit("2030-01-01/2030-02-01")],
        join: SetJoin::Or,
    };
    assert!(run(pred("period", matcher), &source).matched);
}

// ===========================================================================
// Range: both bounds malformed vs. only one -- each is a no-match, not a
// panic and not a match by virtue of the other (well-formed) bound.
// ===========================================================================

#[test]
fn range_both_bounds_malformed_is_no_match() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("not-a-date")),
        hi: Bound::Value(lit("also-not-a-date")),
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn range_only_hi_bound_malformed_is_no_match_even_when_lo_alone_would_overlap() {
    // lo=2026-01-01 alone (paired with an open hi) would overlap the stored
    // interval -- confirms the malformed hi bound isn't silently dropped in
    // favor of treating the range as lo-only-bounded.
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-01-01")),
        hi: Bound::Value(lit("not-a-date")),
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn range_only_lo_bound_malformed_is_no_match_even_when_hi_alone_would_overlap() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("not-a-date")),
        hi: Bound::Value(lit("2026-12-31")),
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Compound query: interval predicate mixed with a string-facet predicate --
// matched_facets/diagnostics ordering and correctness under AND.
// ===========================================================================

#[test]
fn compound_and_interval_hit_plus_string_hit_both_contribute_matched_facets_in_order() {
    let mut source = period_source(&["2026-04-01/2026-06-30"]);
    source = source.with("type", FacetType::String, &["report"]);
    let expr = Expr::And(vec![
        pred("type", Matcher::Term(lit("report"))),
        pred(
            "period",
            Matcher::Range {
                lo: Bound::Value(lit("2026-01-01")),
                hi: Bound::Value(lit("2026-12-31")),
            },
        ),
    ]);
    let result = run(expr, &source);
    assert!(result.matched);
    // Left-to-right encounter order: "type" before "period".
    assert_eq!(result.matched_facets, vec!["type", "period"]);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn compound_and_interval_miss_contributes_no_matched_facet_and_no_diagnostic() {
    // The interval predicate misses (disjoint range) while the string
    // predicate hits -- overall AND still fails, "period" must NOT appear in
    // matched_facets (it didn't itself match), and the interval miss must
    // not raise any diagnostic (it's a well-typed, well-formed miss, not an
    // eval-time error condition).
    let mut source = period_source(&["2026-04-01/2026-06-30"]);
    source = source.with("type", FacetType::String, &["report"]);
    let expr = Expr::And(vec![
        pred("type", Matcher::Term(lit("report"))),
        pred(
            "period",
            Matcher::Range {
                lo: Bound::Value(lit("2027-01-01")),
                hi: Bound::Value(lit("2027-12-31")),
            },
        ),
    ]);
    let result = run(expr, &source);
    assert!(!result.matched);
    assert_eq!(result.matched_facets, vec!["type"]);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn compound_and_interval_predicate_first_then_string_preserves_encounter_order() {
    // Same as above but with the interval predicate written first in the
    // query -- matched_facets order must track AST left-to-right traversal,
    // not any fixed type-based ordering.
    let mut source = period_source(&["2026-04-01/2026-06-30"]);
    source = source.with("type", FacetType::String, &["report"]);
    let expr = Expr::And(vec![
        pred(
            "period",
            Matcher::Range {
                lo: Bound::Value(lit("2026-01-01")),
                hi: Bound::Value(lit("2026-12-31")),
            },
        ),
        pred("type", Matcher::Term(lit("report"))),
    ]);
    let result = run(expr, &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["period", "type"]);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn compound_or_string_unknown_facet_diagnostic_survives_alongside_interval_hit() {
    // An OR where one branch references an unknown string facet (raising
    // UnknownFacet) and the other is an interval hit -- the interval branch's
    // match and lack-of-diagnostic must coexist correctly with the other
    // branch's diagnostic in the merged Outcome; distinguishes the "no
    // diagnostic on a well-formed interval miss/hit" path from the
    // "diagnostic on a genuinely unknown facet" path in the same evaluation.
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let expr = Expr::Or(vec![
        pred("ghost", Matcher::Term(lit("x"))),
        pred("period", Matcher::Term(lit("2026-05-15"))),
    ]);
    let result = run(expr, &source);
    assert!(result.matched);
    assert_eq!(result.matched_facets, vec!["period"]);
    assert_eq!(
        result.diagnostics,
        vec![facetquery::EvalDiagnostic::UnknownFacet(
            "ghost".to_string()
        )]
    );
}

// ===========================================================================
// Boundary inclusivity, other direction: a Cmp/Range bound exactly equal to
// a stored interval's endpoint, and a stored interval whose endpoint sits
// exactly on a Range bound -- both already exercised for Term above; these
// pin the same inclusivity for Range/Cmp specifically.
// ===========================================================================

#[test]
fn range_lo_bound_exactly_on_stored_end_is_inclusive_overlap() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("2026-06-30")),
        hi: Bound::Unbounded,
    };
    assert!(run(pred("period", matcher), &source).matched);
}

#[test]
fn range_hi_bound_exactly_on_stored_start_is_inclusive_overlap() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Unbounded,
        hi: Bound::Value(lit("2026-04-01")),
    };
    assert!(run(pred("period", matcher), &source).matched);
}

// ===========================================================================
// Malformed-vs-diagnosed contrast: the malformed-stored-value no-match path
// (this section) emits ZERO diagnostics, in explicit contrast to the
// UnknownFacet/RangeOnNonOrdered paths (asserted non-empty in the same test)
// -- proves the empty-diagnostics assertion elsewhere isn't just "diagnostics
// happen to be unimplemented" but a deliberate distinction from the paths
// that DO raise one.
// ===========================================================================

#[test]
fn malformed_stored_value_diagnostics_are_empty_in_contrast_to_unknown_facet_and_non_ordered() {
    // Malformed stored value on a known DateInterval facet -- no diagnostic.
    let malformed_source = period_source(&["not-an-interval"]);
    let malformed_result = run(pred("period", Matcher::Exists), &malformed_source);
    assert!(malformed_result.matched); // Exists doesn't care about shape.
    let malformed_range = run(
        pred(
            "period",
            Matcher::Range {
                lo: Bound::Unbounded,
                hi: Bound::Unbounded,
            },
        ),
        &malformed_source,
    );
    assert!(!malformed_range.matched);
    assert!(malformed_range.diagnostics.is_empty());

    // Contrast 1: a genuinely unknown facet name DOES raise a diagnostic.
    let unknown_result = run(
        pred("nonexistent_facet", Matcher::Term(lit("x"))),
        &malformed_source,
    );
    assert!(!unknown_result.diagnostics.is_empty());

    // Contrast 2: Range/Cmp against a String-typed facet DOES raise
    // RangeOnNonOrdered, even though the value shape itself is irrelevant.
    let string_source = FixtureSource::new().with("title", FacetType::String, &["hello"]);
    let non_ordered_result = run(
        pred(
            "title",
            Matcher::Range {
                lo: Bound::Unbounded,
                hi: Bound::Unbounded,
            },
        ),
        &string_source,
    );
    assert!(!non_ordered_result.diagnostics.is_empty());
}

// ===========================================================================
// Exists — unchanged behavior, sanity-checked against DateInterval too
// ===========================================================================

#[test]
fn exists_matches_when_facet_has_any_interval_value() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    assert!(run(pred("period", Matcher::Exists), &source).matched);
}

// ===========================================================================
// Malformed stored value: silent no-match, no diagnostic
// ===========================================================================

#[test]
fn malformed_stored_value_is_no_match_without_diagnostic() {
    for stored in [
        "notadate",
        "2026-04-01",              // only one endpoint, no `/`
        "2026-04-01/2026-06-30/x", // too many parts
        "2026-04-01/notadate",     // non-date-shaped endpoint
    ] {
        let source = period_source(&[stored]);
        let result = run(pred("period", Matcher::Exists), &source);
        // Exists only needs a value to be present, regardless of shape.
        assert!(result.matched);

        let range_result = run(
            pred(
                "period",
                Matcher::Range {
                    lo: Bound::Unbounded,
                    hi: Bound::Unbounded,
                },
            ),
            &source,
        );
        assert!(!range_result.matched, "stored={stored:?}");
        assert!(range_result.diagnostics.is_empty(), "stored={stored:?}");
    }
}

#[test]
fn start_after_end_stored_value_is_no_match() {
    let source = period_source(&["2026-06-30/2026-04-01"]);
    let matcher = Matcher::Range {
        lo: Bound::Unbounded,
        hi: Bound::Unbounded,
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Malformed query operand: no-match, no ParseError-shaped diagnostic
// ===========================================================================

#[test]
fn malformed_cmp_operand_is_no_match() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Cmp {
        op: CmpOp::Gt,
        term: lit("not-a-date"),
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

#[test]
fn malformed_range_bound_is_no_match() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let matcher = Matcher::Range {
        lo: Bound::Value(lit("not-a-date")),
        hi: Bound::Unbounded,
    };
    let result = run(pred("period", matcher), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Wildcard term: not date/interval-shaped -> no-match (the deliberate
// divergence from point-Date's glob matching)
// ===========================================================================

#[test]
fn wildcard_term_is_a_no_match_not_a_glob_match() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    let wildcard = Term {
        raw: "2026-*".to_string(),
        segments: vec![Seg::Literal("2026-".to_string()), Seg::StarWild],
    };
    let result = run(pred("period", Matcher::Term(wildcard)), &source);
    assert!(!result.matched);
    assert!(result.diagnostics.is_empty());
}

// ===========================================================================
// Calendar validation — a shape-valid but impossible date (endpoint or
// operand) parses as a real `time::Date`, so it's rejected the same way any
// malformed value is: silent no-match, no diagnostic, no panic.
// ===========================================================================

const CALENDAR_INVALID_DATES: &[&str] = &[
    "2026-13-01", // month 13
    "2026-00-01", // month 0
    "2026-01-32", // day 32
    "2026-01-00", // day 0
    "2026-02-30", // no such day in February
    "2025-02-29", // 2025 is not a leap year
];

/// `2026-01-01/<invalid>` for each [`CALENDAR_INVALID_DATES`] entry, in the
/// same order -- a stored interval whose end endpoint is calendar-invalid.
const CALENDAR_INVALID_STORED_ENDS: &[&str] = &[
    "2026-01-01/2026-13-01",
    "2026-01-01/2026-00-01",
    "2026-01-01/2026-01-32",
    "2026-01-01/2026-01-00",
    "2026-01-01/2026-02-30",
    "2025-01-01/2025-02-29",
];

#[test]
fn calendar_invalid_stored_endpoint_is_no_match() {
    for (invalid, stored) in CALENDAR_INVALID_DATES
        .iter()
        .zip(CALENDAR_INVALID_STORED_ENDS)
    {
        let source = period_source(&[stored]);
        let matcher = Matcher::Range {
            lo: Bound::Unbounded,
            hi: Bound::Unbounded,
        };
        let result = run(pred("period", matcher), &source);
        assert!(!result.matched, "invalid={invalid:?}");
        assert!(result.diagnostics.is_empty(), "invalid={invalid:?}");
    }
}

#[test]
fn calendar_invalid_query_operand_is_no_match_for_term_and_cmp() {
    let source = period_source(&["2026-04-01/2026-06-30"]);
    for invalid in CALENDAR_INVALID_DATES {
        let term_result = run(pred("period", Matcher::Term(lit(invalid))), &source);
        assert!(!term_result.matched, "invalid={invalid:?}");
        assert!(term_result.diagnostics.is_empty(), "invalid={invalid:?}");

        let cmp_result = run(
            pred(
                "period",
                Matcher::Cmp {
                    op: CmpOp::Gt,
                    term: lit(invalid),
                },
            ),
            &source,
        );
        assert!(!cmp_result.matched, "invalid={invalid:?}");
        assert!(cmp_result.diagnostics.is_empty(), "invalid={invalid:?}");
    }
}

#[test]
fn leap_day_on_a_leap_year_is_a_valid_interval_endpoint_and_matches() {
    let source = period_source(&["2024-01-01/2024-02-29"]);
    assert!(run(pred("period", Matcher::Term(lit("2024-02-29"))), &source).matched);
}

// ===========================================================================
// No panic on arbitrary stored + query strings
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

const FUZZ_ALPHABET: &[char] = &['2', '0', '6', '-', '/', 'x', '*', '?', ' ', '🦀'];

fn random_fuzz_string(rng: &mut Xorshift64, len: usize) -> String {
    (0..len)
        .map(|_| FUZZ_ALPHABET[rng.next_range(FUZZ_ALPHABET.len())])
        .collect()
}

/// A `FacetSource` over one owned interval value — `FixtureSource` only
/// stores `&'static str`, which can't hold fuzz-generated strings.
struct OwnedSource {
    value: String,
}

impl FacetSource for OwnedSource {
    fn facet(&self, key: &str) -> FacetLookup {
        if key == "period" {
            FacetLookup::Present {
                ty: FacetType::DateInterval,
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

#[test]
fn fuzz_arbitrary_stored_and_query_strings_never_panic() {
    let mut rng = Xorshift64::new(0xDA7E_1DEA_0000_0001);
    for trial in 0..5_000 {
        let stored_len = rng.next_range(24);
        let stored = random_fuzz_string(&mut rng, stored_len);
        let operand_len = rng.next_range(24);
        let operand = random_fuzz_string(&mut rng, operand_len);
        let source = OwnedSource {
            value: stored.clone(),
        };

        let matchers = [
            Matcher::Term(lit(&operand)),
            Matcher::Range {
                lo: Bound::Value(lit(&operand)),
                hi: Bound::Unbounded,
            },
            Matcher::Cmp {
                op: CmpOp::Gt,
                term: lit(&operand),
            },
        ];
        for matcher in matchers {
            let result = std::panic::catch_unwind(|| run(pred("period", matcher), &source));
            assert!(
                result.is_ok(),
                "trial {trial}: panicked stored={stored:?} operand={operand:?}"
            );
        }
    }
}
