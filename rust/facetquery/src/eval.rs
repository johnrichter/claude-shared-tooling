//! Evaluates a parsed [`crate::Query`] against a generic facet source.
//!
//! Frontmatter-agnostic by construction: this module knows nothing about any
//! one document schema, only the [`FacetSource`] trait a caller implements
//! for whatever it's evaluating against (frontmatter, a navigator symbol
//! index, anything with named facets and a text field). Zero dependency on
//! any other crate in this workspace.
//!
//! # Purity
//! [`evaluate`] is a pure, total function of `(query, source)`: no I/O,
//! clock, network, or global state, and no panics on any input the parser
//! could have produced. All non-determinism a caller might see (e.g. a
//! `HashMap`-backed `FacetSource` returning values in insertion order vs
//! not) lives entirely in the caller's own `FacetSource` impl — this module
//! never iterates a map itself and never reorders what `FacetSource` hands
//! back.
//!
//! # Determinism of the result
//! [`MatchResult::matched_facets`] and [`MatchResult::diagnostics`] are both
//! ordered deterministically from `(query, source)` alone: `matched_facets`
//! is deduplicated but keeps first-encounter order (left-to-right over the
//! AST); `diagnostics` is not deduplicated (each offending predicate
//! contributes its own diagnostic) but is likewise emitted in left-to-right
//! traversal order. Two `evaluate` calls on equal inputs are `PartialEq`.

use std::cmp::Ordering;

use crate::ast::{Bound, CmpOp, Expr, Matcher, Predicate, Query, Seg, SetJoin, Term};

/// What a [`FacetSource`] knows about one facet name.
///
/// `#[non_exhaustive]` — a future facet source capability (e.g. a facet that
/// exists but is undergoing indexing) may need a third variant without that
/// being a breaking change for existing callers matching on this today.
#[non_exhaustive]
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FacetLookup {
    /// The facet source has never heard of this facet name.
    Unknown,
    /// The facet is known, with its declared type and every value the
    /// current document/entity has for it. `values` may be empty — a
    /// schema-known facet the current document simply doesn't set is still
    /// `Present` (never `Unknown`, which is reserved for a facet name the
    /// source's schema has no entry for at all); see [`Matcher::Exists`]
    /// for why "present" and "exists" are deliberately NOT the same
    /// question.
    Present {
        /// The facet's declared type — governs range/comparison eligibility.
        ty: FacetType,
        /// Every value this facet holds, in the source's own order.
        values: Vec<String>,
    },
}

/// A facet's type, as declared by its [`FacetSource`].
///
/// `Numeric` and `Date` are "ordered" — eligible for `Range`/`Cmp` matchers.
/// `String` (the language's default) is not: a range or comparison against
/// a `String`-typed facet is [`EvalDiagnostic::RangeOnNonOrdered`].
///
/// `#[non_exhaustive]` — a future ordered or unordered type may be added
/// without breaking existing exhaustive-looking match arms (always include
/// a wildcard arm).
#[non_exhaustive]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FacetType {
    /// Unordered text equality/wildcard matching only (the default type).
    String,
    /// Ordered — parsed as a number for range/comparison.
    Numeric,
    /// Ordered — `YYYY-MM-DD`, compared lexically on that fixed-width form
    /// (which is order-correct for that shape, so no calendar parsing is
    /// needed).
    Date,
}

/// A generic source of facet data — the boundary between this crate and
/// whatever a caller is actually querying (frontmatter, a symbol index,
/// anything with named facets and searchable text).
///
/// Implementations must be pure and side-effect-free from [`evaluate`]'s
/// point of view: the same `(key, term)` inputs should answer identically
/// across repeated calls within one `evaluate` run, since evaluation may
/// call either method more than once for the same facet.
pub trait FacetSource {
    /// Looks up a facet by its exact name as written in the query
    /// (`facet:value`'s `facet`), case-sensitively — matching is this
    /// trait's job, not the evaluator's.
    fn facet(&self, key: &str) -> FacetLookup;

    /// Full-text match for a bareword/phrase term with no `facet:` prefix,
    /// against the source's own text field(s). How wildcards or phrase
    /// boundaries apply to full text is entirely the source's decision;
    /// the evaluator only delegates.
    fn text_matches(&self, term: &Term) -> bool;
}

/// A warning surfaced while evaluating — the query is syntactically valid
/// but a predicate's premise (a facet's existence, a facet's type) doesn't
/// hold against this particular [`FacetSource`]. The offending predicate is
/// a no-match; the rest of the query still evaluates.
///
/// `#[non_exhaustive]` — the language spec's eval-time diagnostic set may
/// grow in a future minor version.
#[non_exhaustive]
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum EvalDiagnostic {
    /// A predicate named a facet the source doesn't know at all.
    UnknownFacet(String),
    /// A predicate used `Range`/`Cmp` against a facet whose type isn't
    /// ordered (i.e. `String`).
    RangeOnNonOrdered {
        /// The facet name the range/comparison targeted.
        facet: String,
        /// The facet's actual (non-ordered) type.
        ty: FacetType,
    },
}

/// The outcome of [`evaluate`]-ing a [`Query`] against a [`FacetSource`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MatchResult {
    /// Whether the query as a whole matched.
    pub matched: bool,
    /// Every facet name whose predicate individually matched somewhere in
    /// the query tree, deduplicated, in first-encounter (left-to-right)
    /// order. Informational — populated independently of `Not`'s negation
    /// and independent of whether an enclosing `AND`/`OR` made the overall
    /// query match, so a caller can highlight "what contributed" even on a
    /// query that matched via other terms. Never includes a facet reached
    /// only through a `Not` (see [`Expr::Not`]'s doc note).
    pub matched_facets: Vec<String>,
    /// Every eval-time diagnostic raised, in left-to-right traversal order.
    pub diagnostics: Vec<EvalDiagnostic>,
}

/// Evaluates `query` against `source`, per the `facetquery@1` language
/// spec's eval-time semantics (unknown-facet and type-gating diagnostics,
/// match-all on an empty conjunction, exists/not-exists).
///
/// Pure and deterministic — see the module docs. Never panics: a value that
/// doesn't parse for an ordered facet type is simply a no-match, not an
/// error.
#[must_use]
pub fn evaluate(query: &Query, source: &impl FacetSource) -> MatchResult {
    let outcome = eval_expr(&query.expr, source);
    MatchResult {
        matched: outcome.matched,
        matched_facets: dedup_stable(outcome.matched_facets),
        diagnostics: outcome.diagnostics,
    }
}

/// One node's evaluation, before the top-level dedup pass in [`evaluate`].
struct Outcome {
    matched: bool,
    matched_facets: Vec<String>,
    diagnostics: Vec<EvalDiagnostic>,
}

fn eval_expr(expr: &Expr, source: &impl FacetSource) -> Outcome {
    match expr {
        // A zero-conjunct AND is the parser's match-all representation
        // (see crate::ast::Query's doc) — `Iterator::all` on an empty
        // sequence is already `true`, so this needs no special case.
        Expr::And(exprs) => {
            let results: Vec<Outcome> = exprs.iter().map(|e| eval_expr(e, source)).collect();
            let matched = results.iter().all(|r| r.matched);
            merge(matched, results)
        }
        Expr::Or(exprs) => {
            let results: Vec<Outcome> = exprs.iter().map(|e| eval_expr(e, source)).collect();
            let matched = results.iter().any(|r| r.matched);
            merge(matched, results)
        }
        Expr::Not(inner) => {
            let result = eval_expr(inner, source);
            // A `Not` never contributes matched_facets — the facet it
            // negated didn't itself satisfy the query, whichever way its
            // own sub-evaluation came out. Diagnostics still propagate:
            // an unknown-facet reference under `NOT` is still worth
            // surfacing, since the query still ran.
            Outcome {
                matched: !result.matched,
                matched_facets: Vec::new(),
                diagnostics: result.diagnostics,
            }
        }
        // Grouping is transparent to evaluation — it only overrode
        // precedence during parsing.
        Expr::Group(inner) => eval_expr(inner, source),
        Expr::Pred(pred) => eval_predicate(pred, source),
    }
}

/// Folds a boolean combinator's already-evaluated children into one
/// `Outcome`, concatenating their `matched_facets`/`diagnostics` in order.
fn merge(matched: bool, results: Vec<Outcome>) -> Outcome {
    let mut matched_facets = Vec::new();
    let mut diagnostics = Vec::new();
    for r in results {
        matched_facets.extend(r.matched_facets);
        diagnostics.extend(r.diagnostics);
    }
    Outcome {
        matched,
        matched_facets,
        diagnostics,
    }
}

fn eval_predicate(pred: &Predicate, source: &impl FacetSource) -> Outcome {
    let Some(facet) = &pred.facet else {
        // Bareword — the grammar only ever pairs `facet: None` with a
        // `Term` matcher; any other shape here is unreachable under the
        // current grammar, and defensively a no-match (not a panic) should
        // a future non-exhaustive `Matcher` variant reach this arm.
        let matched = match &pred.matcher {
            Matcher::Term(term) => source.text_matches(term),
            _ => false,
        };
        return Outcome {
            matched,
            matched_facets: Vec::new(),
            diagnostics: Vec::new(),
        };
    };

    match source.facet(facet) {
        FacetLookup::Unknown => Outcome {
            matched: false,
            matched_facets: Vec::new(),
            diagnostics: vec![EvalDiagnostic::UnknownFacet(facet.clone())],
        },
        FacetLookup::Present { ty, values } => eval_present(facet, ty, &values, &pred.matcher),
    }
}

/// Evaluates a predicate's matcher once its facet is known to be present.
fn eval_present(facet: &str, ty: FacetType, values: &[String], matcher: &Matcher) -> Outcome {
    let no_match = || Outcome {
        matched: false,
        matched_facets: Vec::new(),
        diagnostics: Vec::new(),
    };
    let hit = || Outcome {
        matched: true,
        matched_facets: vec![facet.to_string()],
        diagnostics: Vec::new(),
    };

    match matcher {
        Matcher::Term(term) => {
            if values.iter().any(|v| term_matches_value(term, v)) {
                hit()
            } else {
                no_match()
            }
        }
        Matcher::Set { terms, join } => {
            // A single-member set is the member itself — the join is a
            // placeholder for exactly one member and carries no meaning to
            // pick between here; the per-term/per-value check below is
            // identical to `Term` in that case regardless of `join`.
            let term_hits = |term: &Term| values.iter().any(|v| term_matches_value(term, v));
            let set_hit = match join {
                SetJoin::Or => terms.iter().any(term_hits),
                SetJoin::And => !terms.is_empty() && terms.iter().all(term_hits),
            };
            if set_hit {
                hit()
            } else {
                no_match()
            }
        }
        // "Exists" means at least one value, not merely a schema-known
        // facet name: a source may return `Present` with an empty `values`
        // for a facet its schema knows but this document doesn't set (so a
        // lookup by that name is never spuriously `UnknownFacet`), and
        // `Exists` must not treat that as a hit -- otherwise every
        // schema-known-but-absent facet would satisfy `facet:*`.
        Matcher::Exists => {
            if values.is_empty() {
                no_match()
            } else {
                hit()
            }
        }
        Matcher::Range { lo, hi } => {
            if ty == FacetType::String {
                return non_ordered(facet, ty);
            }
            let in_range = values.iter().any(|v| {
                let Some(v) = OrdKey::parse(ty, v) else {
                    return false;
                };
                bound_satisfied(lo, ty, |lok| v >= lok) && bound_satisfied(hi, ty, |hik| v <= hik)
            });
            if in_range {
                hit()
            } else {
                no_match()
            }
        }
        Matcher::Cmp { op, term } => {
            if ty == FacetType::String {
                return non_ordered(facet, ty);
            }
            let Some(operand) = OrdKey::parse(ty, &term.raw) else {
                // An unparseable comparison operand can never be
                // satisfied — no-match, not a panic.
                return no_match();
            };
            let satisfies = values
                .iter()
                .any(|v| OrdKey::parse(ty, v).is_some_and(|vk| cmp_matches(*op, &vk, &operand)));
            if satisfies {
                hit()
            } else {
                no_match()
            }
        }
    }
}

fn non_ordered(facet: &str, ty: FacetType) -> Outcome {
    Outcome {
        matched: false,
        matched_facets: Vec::new(),
        diagnostics: vec![EvalDiagnostic::RangeOnNonOrdered {
            facet: facet.to_string(),
            ty,
        }],
    }
}

/// A bound is satisfied if it's [`Bound::Unbounded`] (that side is always
/// open) or its value parses for `ty` and `check` holds against it. A bound
/// value that doesn't parse for the type can never be satisfied — no-match,
/// not a panic.
fn bound_satisfied(bound: &Bound, ty: FacetType, check: impl FnOnce(OrdKey) -> bool) -> bool {
    match bound {
        Bound::Unbounded => true,
        Bound::Value(term) => OrdKey::parse(ty, &term.raw).is_some_and(check),
    }
}

fn cmp_matches(op: CmpOp, value: &OrdKey, operand: &OrdKey) -> bool {
    let Some(ordering) = value.partial_cmp(operand) else {
        return false;
    };
    match op {
        CmpOp::Gt => ordering == Ordering::Greater,
        CmpOp::Ge => ordering != Ordering::Less,
        CmpOp::Lt => ordering == Ordering::Less,
        CmpOp::Le => ordering != Ordering::Greater,
    }
}

/// A facet value or bound, parsed into its ordered-type representation for
/// comparison. Only ever constructed for `Numeric`/`Date` — `String` is
/// gated out before any `OrdKey::parse` call.
#[derive(Debug, Clone, PartialEq)]
enum OrdKey {
    /// A `Numeric` value, parsed as `f64`.
    Num(f64),
    /// A `Date` value, kept as its raw `YYYY-MM-DD` text — lexical order on
    /// that fixed-width form is calendar-correct, so no date parsing beyond
    /// a shape check is needed.
    Date(String),
}

impl OrdKey {
    /// Parses `raw` for `ty`, or `None` if it doesn't parse as that type —
    /// never panics.
    fn parse(ty: FacetType, raw: &str) -> Option<Self> {
        match ty {
            FacetType::Numeric => raw.parse::<f64>().ok().map(Self::Num),
            FacetType::Date => is_date_shape(raw).then(|| Self::Date(raw.to_string())),
            FacetType::String => None,
        }
    }
}

impl PartialOrd for OrdKey {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        match (self, other) {
            (Self::Num(a), Self::Num(b)) => a.partial_cmp(b),
            (Self::Date(a), Self::Date(b)) => Some(a.cmp(b)),
            // Both sides always parse from the same `ty`, so this never
            // happens in practice; no ordering is still the safe answer.
            (Self::Num(_), Self::Date(_)) | (Self::Date(_), Self::Num(_)) => None,
        }
    }
}

/// `YYYY-MM-DD` shape check (fixed digit/dash positions) — not a calendar
/// validation (e.g. `2026-02-30` passes this check); the language spec only
/// requires order-correct lexical comparison, not calendar correctness.
fn is_date_shape(s: &str) -> bool {
    let bytes = s.as_bytes();
    bytes.len() == 10
        && bytes[..4].iter().all(u8::is_ascii_digit)
        && bytes[4] == b'-'
        && bytes[5..7].iter().all(u8::is_ascii_digit)
        && bytes[7] == b'-'
        && bytes[8..10].iter().all(u8::is_ascii_digit)
}

// ---------------------------------------------------------------------------
// Term matching — equality and wildcard share one algorithm: a literal-only
// term degenerates to exact equality under the same glob walk, so there is
// no separate equality code path to keep in sync.
// ---------------------------------------------------------------------------

/// One token of a [`Term`]'s pattern, flattened from its `segments` for the
/// glob walk in [`glob_match`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Tok {
    /// One literal character to match exactly.
    Char(char),
    /// `*` — zero or more of any character.
    Star,
    /// `?` — exactly one of any character.
    Question,
}

fn flatten(segments: &[Seg]) -> Vec<Tok> {
    let mut out = Vec::new();
    for seg in segments {
        match seg {
            Seg::Literal(s) => out.extend(s.chars().map(Tok::Char)),
            Seg::StarWild => out.push(Tok::Star),
            Seg::QuestionWild => out.push(Tok::Question),
        }
    }
    out
}

/// Classic wildcard-glob matching (linear-time two-pointer with backtrack
/// to the most recent `*`), generalized from bytes to `char`s so multi-byte
/// text matches correctly.
fn glob_match(pattern: &[Tok], text: &[char]) -> bool {
    let (mut ti, mut pi) = (0usize, 0usize);
    let mut star: Option<usize> = None;
    let mut star_ti = 0usize;

    while ti < text.len() {
        match pattern.get(pi) {
            Some(Tok::Char(c)) if *c == text[ti] => {
                pi += 1;
                ti += 1;
            }
            Some(Tok::Question) => {
                pi += 1;
                ti += 1;
            }
            Some(Tok::Star) => {
                star = Some(pi);
                star_ti = ti;
                pi += 1;
            }
            _ => match star {
                Some(s) => {
                    pi = s + 1;
                    star_ti += 1;
                    ti = star_ti;
                }
                None => return false,
            },
        }
    }
    while pattern.get(pi) == Some(&Tok::Star) {
        pi += 1;
    }
    pi == pattern.len()
}

fn term_matches_value(term: &Term, value: &str) -> bool {
    let pattern = flatten(&term.segments);
    let text: Vec<char> = value.chars().collect();
    glob_match(&pattern, &text)
}

/// Deduplicates `items` while keeping first-encounter order.
fn dedup_stable(items: Vec<String>) -> Vec<String> {
    let mut seen = Vec::new();
    for item in items {
        if !seen.contains(&item) {
            seen.push(item);
        }
    }
    seen
}
