//! SDET regression probe (not part of the reviewed change set): the
//! workspace date format is strictly `YYYY-MM-DD` (4-digit zero-padded
//! year, unsigned, 2-digit zero-padded month/day, no surrounding
//! whitespace). `parse_date`'s `format_description!("[year]-[month]-[day]")`
//! uses `time`'s DEFAULT component widths, which are lenient on year sign
//! (`sign:automatic` by default) -- this file pins that against the real
//! `evaluate()` API, not just the raw `time::Date::parse` call.
//!
//! EXPECTED TO FAIL today on `+YYYY`/`-YYYY` -- see the SDET report. Do not
//! delete/weaken/skip these assertions to make the suite green; they are the
//! reproduction for a real defect, not test-authoring noise.

use std::collections::HashMap;

use facetquery::{
    evaluate, Bound, Expr, FacetLookup, FacetSource, FacetType, Matcher, Predicate, Query, Seg,
    Term,
};

struct FixtureSource {
    facets: HashMap<&'static str, (FacetType, Vec<String>)>,
}

impl FixtureSource {
    fn date(values: &[&str]) -> Self {
        let mut facets = HashMap::new();
        facets.insert(
            "created",
            (
                FacetType::Date,
                values.iter().map(ToString::to_string).collect(),
            ),
        );
        Self { facets }
    }
}

impl FacetSource for FixtureSource {
    fn facet(&self, key: &str) -> FacetLookup {
        match self.facets.get(key) {
            None => FacetLookup::Unknown,
            Some((ty, values)) => FacetLookup::Present {
                ty: *ty,
                values: values.clone(),
            },
        }
    }
    fn text_matches(&self, _term: &Term) -> bool {
        false
    }
}

fn lit(raw: &str) -> Term {
    Term {
        raw: raw.to_string(),
        segments: vec![Seg::Literal(raw.to_string())],
    }
}

fn unbounded_range_matched(source: &FixtureSource) -> bool {
    let expr = Expr::Pred(Predicate {
        facet: Some("created".to_string()),
        matcher: Matcher::Range {
            lo: Bound::Unbounded,
            hi: Bound::Unbounded,
        },
    });
    evaluate(&Query { expr }, source).matched
}

/// Non-padded, wrong-width, and whitespace-padded years/months/days are
/// correctly rejected today -- pinned so a future `format_description`
/// change doesn't regress these.
#[test]
fn non_strict_widths_and_whitespace_are_rejected() {
    for bad in [
        "202-01-01",   // 3-digit year
        "20260-01-01", // 5-digit year
        "2026-1-1",    // non-padded month/day
        "2026-1-01",
        "2026-01-1",
        " 2026-01-01", // leading whitespace
        "2026-01-01 ", // trailing whitespace
    ] {
        let source = FixtureSource::date(&[bad]);
        assert!(
            !unbounded_range_matched(&source),
            "expected {bad:?} to be rejected as a stored Date value, but it matched"
        );

        let source2 = FixtureSource::date(&["2026-01-01"]);
        let expr = Expr::Pred(Predicate {
            facet: Some("created".to_string()),
            matcher: Matcher::Term(lit(bad)),
        });
        assert!(
            !evaluate(&Query { expr }, &source2).matched,
            "expected {bad:?} to be rejected as a Term query operand, but it matched"
        );
    }
}

/// DEFECT: the workspace date format has no sign. `time`'s default `[year]`
/// component is lenient (`sign:automatic`), so `+YYYY-MM-DD`/`-YYYY-MM-DD`
/// parse as valid `time::Date`s instead of being rejected -- both as a
/// stored facet value and as a query operand, through the real `evaluate()`
/// path (not just the raw `time::Date::parse` call).
#[test]
fn signed_years_are_rejected() {
    for bad in ["+2026-01-01", "-2026-01-01"] {
        let source = FixtureSource::date(&[bad]);
        assert!(
            !unbounded_range_matched(&source),
            "DEFECT: expected signed year {bad:?} to be rejected as a stored Date value, but it matched (time's default [year] component is sign-lenient)"
        );

        let source2 = FixtureSource::date(&["2026-01-01"]);
        let expr = Expr::Pred(Predicate {
            facet: Some("created".to_string()),
            matcher: Matcher::Term(lit(bad)),
        });
        assert!(
            !evaluate(&Query { expr }, &source2).matched,
            "DEFECT: expected signed year {bad:?} to be rejected as a Term query operand, but it matched"
        );
    }
}
