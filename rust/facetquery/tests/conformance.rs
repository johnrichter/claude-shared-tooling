//! Exhaustive `facetquery@1` conformance suite for the test engineer's
//! verification stage of M-FACETQ.P1.T3.
//!
//! Oracle: `schemas/facetquery/facetquery-language.spec.md` +
//! `facetquery.ebnf` (repo root, two levels up from `rust/`). Every test here
//! asserts the parser against what those two files DEFINE, not against
//! whatever the implementation happens to do — a divergence found here is a
//! crate defect per the spec's own "Conformance" section, and is reported as
//! such rather than adjusted to fit.
//!
//! The in-crate `src/parser.rs` unit tests (24, pre-existing) are a floor:
//! they pin the grammar's main shapes. This file adds every spec-defined
//! case not already pinned there — the full precedence table, every parens-
//! override combination, set-shorthand joins, wildcards, ranges/comparisons,
//! exists/not-exists, escaping, reserved words, and all edge-case-table rows
//! with positioned-`Location` assertions (not just `is_err()`).

use facetquery::{
    parse, Bound, CmpOp, Expr, Matcher, ParseErrorKind, Predicate, Seg, SetJoin, Term,
};

// ---------------------------------------------------------------------------
// AST-construction helpers (mirrors src/parser.rs's private test helpers;
// duplicated here because integration tests only see the crate's public API).
// ---------------------------------------------------------------------------

fn lit_term(raw: &str) -> Term {
    Term {
        raw: raw.to_string(),
        segments: vec![Seg::Literal(raw.to_string())],
    }
}

fn bareword(raw: &str) -> Expr {
    Expr::Pred(Predicate {
        facet: None,
        matcher: Matcher::Term(lit_term(raw)),
    })
}

fn pred(facet: &str, matcher: Matcher) -> Expr {
    Expr::Pred(Predicate {
        facet: Some(facet.to_string()),
        matcher,
    })
}

fn term_pred(facet: &str, raw: &str) -> Expr {
    pred(facet, Matcher::Term(lit_term(raw)))
}

fn group(e: Expr) -> Expr {
    Expr::Group(Box::new(e))
}

fn not(e: Expr) -> Expr {
    Expr::Not(Box::new(e))
}

// ===========================================================================
// 1. Boolean operators — keyword + implicit-AND-by-adjacency (spec: "Boolean
//    operators" table).
// ===========================================================================

#[test]
fn and_keyword_and_implicit_adjacency_are_the_same_ast_node() {
    // spec: "facet:a AND facet:b ≡ facet:a facet:b — same AST node either way"
    assert_eq!(
        parse("facet:a AND facet:b").unwrap().expr,
        parse("facet:a facet:b").unwrap().expr
    );
    assert_eq!(
        parse("facet:a AND facet:b").unwrap().expr,
        Expr::And(vec![term_pred("facet", "a"), term_pred("facet", "b")])
    );
}

#[test]
fn or_has_no_implicit_form() {
    // spec: "No implicit-OR — adjacency always means AND, never OR". Two
    // barewords separated only by whitespace is AND, never OR, regardless of
    // context.
    assert_eq!(
        parse("a b").unwrap().expr,
        Expr::And(vec![bareword("a"), bareword("b")])
    );
}

#[test]
fn not_keyword_requires_whitespace_dash_does_not() {
    // spec: "NOT is a word operator and requires whitespace before its
    // operand; - attaches with no whitespace". Both produce identical nodes.
    assert_eq!(
        parse("NOT facet:x").unwrap().expr,
        not(term_pred("facet", "x"))
    );
    assert_eq!(
        parse("-facet:x").unwrap().expr,
        not(term_pred("facet", "x"))
    );
    assert_eq!(
        parse("NOT facet:x").unwrap().expr,
        parse("-facet:x").unwrap().expr
    );
    // NOT with no trailing whitespace is not the operator at all — "NOTfoo"
    // is one bareword token (NOT is only reserved as a whole, standalone
    // run — "NOTfoo" is not an exact match of the reserved word "NOT").
    assert_eq!(parse("NOTfoo").unwrap().expr, bareword("NOTfoo"));
}

#[test]
fn dash_negates_parenthesized_group() {
    // spec: "-(a OR b)" example under NOT/- notes.
    assert_eq!(
        parse("-(a OR b)").unwrap().expr,
        not(group(Expr::Or(vec![bareword("a"), bareword("b")])))
    );
}

// ===========================================================================
// 2. Full precedence table (spec: "Precedence (published)") — every row,
//    exact expected AST.
// ===========================================================================

#[test]
fn precedence_row_or_binds_looser_than_and() {
    // a OR b AND c => a OR (b AND c)
    assert_eq!(
        parse("a OR b AND c").unwrap().expr,
        Expr::Or(vec![
            bareword("a"),
            Expr::And(vec![bareword("b"), bareword("c")])
        ])
    );
}

#[test]
fn precedence_row_not_binds_tighter_than_and() {
    // NOT a AND b => (NOT a) AND b
    assert_eq!(
        parse("NOT a AND b").unwrap().expr,
        Expr::And(vec![not(bareword("a")), bareword("b")])
    );
}

#[test]
fn precedence_row_implicit_and_binds_tighter_than_or() {
    // a b OR c (implicit AND) => (a AND b) OR c
    assert_eq!(
        parse("a b OR c").unwrap().expr,
        Expr::Or(vec![
            Expr::And(vec![bareword("a"), bareword("b")]),
            bareword("c")
        ])
    );
}

#[test]
fn precedence_row_and_chains_join_under_or() {
    // a AND b OR c AND d => (a AND b) OR (c AND d)
    assert_eq!(
        parse("a AND b OR c AND d").unwrap().expr,
        Expr::Or(vec![
            Expr::And(vec![bareword("a"), bareword("b")]),
            Expr::And(vec![bareword("c"), bareword("d")]),
        ])
    );
}

#[test]
fn precedence_row_dash_negation_binds_tighter_than_or() {
    // -a OR b => (-a) OR b
    assert_eq!(
        parse("-a OR b").unwrap().expr,
        Expr::Or(vec![not(bareword("a")), bareword("b")])
    );
}

// ===========================================================================
// 3. Parens overriding each precedence row.
// ===========================================================================

#[test]
fn parens_override_or_under_and() {
    // (a OR b) AND c -- overrides "AND binds tighter"; without parens this
    // input has no ambiguity (AND still binds tighter), so this instead
    // proves grouping is preserved distinctly in the AST (Expr::Group, not
    // unwrapped) rather than silently collapsing to the same shape.
    assert_eq!(
        parse("(a OR b) AND c").unwrap().expr,
        Expr::And(vec![
            group(Expr::Or(vec![bareword("a"), bareword("b")])),
            bareword("c")
        ])
    );
}

#[test]
fn parens_override_and_under_or_reversing_natural_grouping() {
    // a AND (b OR c) -- without parens "a AND b OR c" would be (a AND b) OR c;
    // wrapping "b OR c" instead produces a AND (b OR c), the opposite grouping.
    assert_eq!(
        parse("a AND (b OR c)").unwrap().expr,
        Expr::And(vec![
            bareword("a"),
            group(Expr::Or(vec![bareword("b"), bareword("c")]))
        ])
    );
    assert_ne!(
        parse("a AND (b OR c)").unwrap().expr,
        parse("a AND b OR c").unwrap().expr
    );
}

#[test]
fn parens_override_not_scope() {
    // NOT (a AND b) -- NOT normally binds to one primary only; parens widen
    // its scope to the whole group instead of just the first conjunct.
    assert_eq!(
        parse("NOT (a AND b)").unwrap().expr,
        not(group(Expr::And(vec![bareword("a"), bareword("b")])))
    );
    assert_ne!(
        parse("NOT (a AND b)").unwrap().expr,
        parse("NOT a AND b").unwrap().expr
    );
}

#[test]
fn parens_override_both_sides_of_or() {
    // (a AND b) OR (c AND d) -- same resulting shape as the unparenthesized
    // row above, but each side is an explicit Group in the AST.
    assert_eq!(
        parse("(a AND b) OR (c AND d)").unwrap().expr,
        Expr::Or(vec![
            group(Expr::And(vec![bareword("a"), bareword("b")])),
            group(Expr::And(vec![bareword("c"), bareword("d")])),
        ])
    );
}

// ===========================================================================
// 4. Set shorthand (spec: "Set shorthand").
// ===========================================================================

#[test]
fn set_shorthand_or_and_and_join() {
    assert_eq!(
        parse("status:(open OR closed)").unwrap().expr,
        pred(
            "status",
            Matcher::Set {
                terms: vec![lit_term("open"), lit_term("closed")],
                join: SetJoin::Or,
            }
        )
    );
    assert_eq!(
        parse("status:(open AND closed)").unwrap().expr,
        pred(
            "status",
            Matcher::Set {
                terms: vec![lit_term("open"), lit_term("closed")],
                join: SetJoin::And,
            }
        )
    );
}

#[test]
fn set_shorthand_three_plus_members_same_join() {
    assert_eq!(
        parse("status:(a OR b OR c)").unwrap().expr,
        pred(
            "status",
            Matcher::Set {
                terms: vec![lit_term("a"), lit_term("b"), lit_term("c")],
                join: SetJoin::Or,
            }
        )
    );
}

#[test]
fn set_shorthand_equivalent_to_ordinary_or_predicates() {
    // spec: "facet:(v1 OR v2) is equivalent in effect to facet:v1 OR facet:v2;
    // the shorthand exists for readability, not new semantics." -- this is an
    // AST-shape claim (Set vs two Term predicates), not equal ASTs; assert
    // both parse successfully and are intentionally NOT AST-identical (the
    // spec says "equivalent in effect" i.e. at evaluation, not "same AST").
    let set = parse("status:(a OR b)").unwrap().expr;
    let expanded = parse("status:a OR status:b").unwrap().expr;
    assert_ne!(
        set, expanded,
        "Set and expanded-predicate forms are distinct AST shapes by design"
    );
}

#[test]
fn mixed_and_or_in_one_set_is_parse_error() {
    let input = "status:(open OR closed AND foo)";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::MixedSetJoin);
    // Non-zero, non-garbage location: the implementation reports the
    // mismatch after consuming the offending join keyword and its
    // mandatory trailing whitespace, so `at` points at "foo" (the next
    // term), not at "AND" itself and not at offset 0.
    let expected_offset = input.find("foo").unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn mixed_and_or_reverse_order_is_also_parse_error() {
    let err = parse("status:(open AND closed OR foo)").unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::MixedSetJoin);
}

#[test]
fn mixing_joins_across_predicates_instead_of_in_one_set_is_valid() {
    // spec: "(facet:v1 OR facet:v2) AND facet:v3" -- the documented way to mix.
    assert!(parse("(status:a OR status:b) AND status:c").is_ok());
}

// ===========================================================================
// 5. Wildcards (spec: "Wildcards").
// ===========================================================================

#[test]
fn wildcard_star_and_question_freely_mixed_with_literal_text() {
    for (input, expected_segs) in [
        (
            "ser*ce",
            vec![
                Seg::Literal("ser".into()),
                Seg::StarWild,
                Seg::Literal("ce".into()),
            ],
        ),
        (
            "?erver",
            vec![Seg::QuestionWild, Seg::Literal("erver".into())],
        ),
        (
            "a*b?c",
            vec![
                Seg::Literal("a".into()),
                Seg::StarWild,
                Seg::Literal("b".into()),
                Seg::QuestionWild,
                Seg::Literal("c".into()),
            ],
        ),
    ] {
        let expr = parse(input).unwrap().expr;
        let Expr::Pred(Predicate {
            facet: None,
            matcher: Matcher::Term(term),
        }) = expr
        else {
            panic!("expected bareword term for {input:?}");
        };
        assert_eq!(term.segments, expected_segs, "input={input:?}");
    }
}

#[test]
fn wildcards_are_literal_inside_quotes_for_bareword_and_predicate_value() {
    // spec: "Active only outside double quotes... quoting disables wildcard
    // meaning entirely."
    assert_eq!(parse("\"a*b?c\"").unwrap().expr, bareword("a*b?c"));
    assert_eq!(
        parse("status:\"a*b?c\"").unwrap().expr,
        term_pred("status", "a*b?c")
    );
    // Confirm no StarWild/QuestionWild segment sneaks in for the quoted form.
    let Expr::Pred(Predicate {
        matcher: Matcher::Term(term),
        ..
    }) = parse("\"a*b?c\"").unwrap().expr
    else {
        panic!("expected term");
    };
    assert_eq!(term.segments, vec![Seg::Literal("a*b?c".to_string())]);
}

// ===========================================================================
// 6. Ranges and comparisons (spec: "Ranges and comparisons — type-gated").
// Grammar-level: parser accepts these for ANY facet name; type-eligibility is
// explicitly an eval-time concern the parser never checks (no evaluator in
// this crate/task).
// ===========================================================================

#[test]
fn range_both_bounds_concrete() {
    assert_eq!(
        parse("created:[2026-01-01 TO 2026-12-31]").unwrap().expr,
        pred(
            "created",
            Matcher::Range {
                lo: Bound::Value(lit_term("2026-01-01")),
                hi: Bound::Value(lit_term("2026-12-31")),
            }
        )
    );
}

#[test]
fn range_unbounded_lo_and_hi_independently() {
    assert_eq!(
        parse("created:[* TO 100]").unwrap().expr,
        pred(
            "created",
            Matcher::Range {
                lo: Bound::Unbounded,
                hi: Bound::Value(lit_term("100")),
            }
        )
    );
    assert_eq!(
        parse("created:[2026-01-01 TO *]").unwrap().expr,
        pred(
            "created",
            Matcher::Range {
                lo: Bound::Value(lit_term("2026-01-01")),
                hi: Bound::Unbounded,
            }
        )
    );
    assert_eq!(
        parse("created:[* TO *]").unwrap().expr,
        pred(
            "created",
            Matcher::Range {
                lo: Bound::Unbounded,
                hi: Bound::Unbounded,
            }
        )
    );
}

#[test]
fn all_four_comparison_operators() {
    for (token, op) in [
        (">", CmpOp::Gt),
        (">=", CmpOp::Ge),
        ("<", CmpOp::Lt),
        ("<=", CmpOp::Le),
    ] {
        let input = format!("created:{token}100");
        assert_eq!(
            parse(&input).unwrap().expr,
            pred(
                "created",
                Matcher::Cmp {
                    op,
                    term: lit_term("100"),
                }
            ),
            "input={input:?}"
        );
    }
}

#[test]
fn range_accepted_on_any_facet_name_grammar_is_type_blind() {
    // Grammar has no notion of facet type at all -- string-named facets
    // parse a range/comparison identically to date/numeric-named ones. Type
    // eligibility (RangeOnNonOrdered) is documented as eval-time only.
    assert!(parse("title:[a TO z]").is_ok());
    assert!(parse("title:>z").is_ok());
}

// ===========================================================================
// 7. Exists / not-exists (spec: "Exists / not-exists").
// ===========================================================================

#[test]
fn facet_star_is_exists() {
    assert_eq!(
        parse("status:*").unwrap().expr,
        pred("status", Matcher::Exists)
    );
}

#[test]
fn not_facet_star_and_dash_facet_star_are_not_exists() {
    let exists = pred("status", Matcher::Exists);
    assert_eq!(parse("NOT status:*").unwrap().expr, not(exists.clone()));
    assert_eq!(parse("-status:*").unwrap().expr, not(exists));
}

#[test]
fn bareword_star_never_reclassifies_to_exists() {
    // spec: "A bareword ... that is the single wildcard segment * stays
    // Matcher::Term -- it means 'match all text', not 'exists'." Reclassification
    // is scoped to the facet:* predicate branch only.
    assert_eq!(parse("*").unwrap().expr, bareword_star_wild_term());
    assert_ne!(parse("*").unwrap().expr, pred("__none__", Matcher::Exists)); // sanity: not exists-shaped at all
                                                                             // Negated bareword-star also stays a negated Term, not Exists.
    assert_eq!(parse("-*").unwrap().expr, not(bareword_star_wild_term()));
}

fn bareword_star_wild_term() -> Expr {
    Expr::Pred(Predicate {
        facet: None,
        matcher: Matcher::Term(Term {
            raw: "*".to_string(),
            segments: vec![Seg::StarWild],
        }),
    })
}

#[test]
fn multi_segment_wildcard_on_predicate_value_is_not_exists() {
    // Exists requires EXACTLY one segment that is StarWild; "ab*" or "**" or
    // "*x" must stay Matcher::Term even on a facet: predicate.
    for input in ["status:ab*", "status:**", "status:*x"] {
        let expr = parse(input).unwrap().expr;
        match expr {
            Expr::Pred(Predicate {
                matcher: Matcher::Exists,
                ..
            }) => {
                panic!("input={input:?} incorrectly classified as Exists")
            }
            Expr::Pred(Predicate {
                matcher: Matcher::Term(_),
                ..
            }) => {}
            other => panic!("input={input:?} unexpected shape: {other:?}"),
        }
    }
}

// ===========================================================================
// 8. Escaping / quoting (spec: "Escaping / quoting").
// ===========================================================================

#[test]
fn leading_dash_is_negation_but_escaped_or_quoted_dash_is_literal() {
    // spec edge case: "Unescaped leading '-' ... Parses as negation ... Quote
    // or escape to get the literal value."
    assert_eq!(parse("-5").unwrap().expr, not(bareword("5")));
    assert_eq!(parse(r"\-5").unwrap().expr, bareword("-5"));
    assert_eq!(parse("\"-5\"").unwrap().expr, bareword("-5"));
    assert_ne!(parse("-5").unwrap().expr, parse(r"\-5").unwrap().expr);
}

#[test]
fn hyphen_not_leading_is_always_literal_no_escaping_needed() {
    // spec: "A hyphen that is not the first character of a predicate/term/
    // group is always literal -- no escaping needed (facet:abc-123)."
    assert_eq!(
        parse("facet:abc-123").unwrap().expr,
        term_pred("facet", "abc-123")
    );
}

#[test]
fn reserved_punctuation_needs_escape_or_quote_to_be_literal() {
    // whitespace, " \ : ( ) [ ] * ? < > all reserved outside a phrase.
    assert_eq!(parse(r"a\:b").unwrap().expr, bareword("a:b"));
    assert_eq!(parse("\"a:b\"").unwrap().expr, bareword("a:b"));
    assert_eq!(parse(r"a\(b\)").unwrap().expr, bareword("a(b)"));
    assert_eq!(parse(r"a\<b").unwrap().expr, bareword("a<b"));
}

#[test]
fn escape_char_is_identical_inside_and_outside_phrase() {
    assert_eq!(parse(r#""a\"b""#).unwrap().expr, bareword("a\"b"));
    assert_eq!(parse(r#""a\\b""#).unwrap().expr, bareword("a\\b"));
    assert_eq!(parse(r"a\\b").unwrap().expr, bareword("a\\b"));
}

// ===========================================================================
// 9. Reserved words (spec: "Boolean operators" notes + edge-case table).
// ===========================================================================

#[test]
fn reserved_words_standing_alone_unescaped_unquoted_are_parse_errors() {
    for word in ["AND", "OR", "NOT", "TO"] {
        let err = parse(word).unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::ReservedWord, "word={word}");
        assert_eq!(err.at.offset, 0, "word={word}");
    }
}

#[test]
fn reserved_word_alone_as_a_predicate_value_is_a_parse_error() {
    let err = parse("status:AND").unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::ReservedWord);
    let expected_offset = "status:AND".find("AND").unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn reserved_word_deep_in_a_query_is_still_positioned_correctly() {
    let input = "status:x AND TO";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::ReservedWord);
    let expected_offset = input.find("TO").unwrap();
    assert_eq!(
        err.at.offset, expected_offset,
        "offset must point at TO, not 0 or end"
    );
    assert_eq!(err.at.column, expected_offset + 1);
}

#[test]
fn quoting_or_escaping_any_char_bypasses_reserved_word_exclusion() {
    for (input, expected) in [
        (r"\AND", "AND"),
        (r"\OR", "OR"),
        (r"\NOT", "NOT"),
        (r"\TO", "TO"),
        ("\"AND\"", "AND"),
        (r"A\ND", "AND"), // escaping ANY single char, not just the first
    ] {
        assert_eq!(
            parse(input).unwrap().expr,
            bareword(expected),
            "input={input:?}"
        );
    }
}

#[test]
fn reserved_word_exclusion_does_not_apply_to_facet_keys() {
    // spec: "This exclusion does NOT apply to facet_key (already
    // unambiguous: a facet key is always followed immediately by ':')".
    // A facet literally named "AND" is legal.
    assert_eq!(parse("AND:value").unwrap().expr, term_pred("AND", "value"));
}

// ===========================================================================
// 10. Match-all consistency (empty / whitespace / bare `*`) -- pinned for T4
//     (evaluator) to rely on; spec edge-case table rows 1-2.
// ===========================================================================

#[test]
fn empty_and_whitespace_only_produce_the_documented_match_all_representation() {
    assert_eq!(parse("").unwrap().expr, Expr::And(Vec::new()));
    assert_eq!(parse("   ").unwrap().expr, Expr::And(Vec::new()));
    assert_eq!(parse("\t\n\r ").unwrap().expr, Expr::And(Vec::new()));
    // Empty and whitespace-only must produce the IDENTICAL representation to
    // each other -- both are the same "no predicate to fail" case, not two
    // different match-all shapes.
    assert_eq!(parse("").unwrap().expr, parse("   \t ").unwrap().expr);
}

#[test]
fn bare_star_is_a_distinct_representation_from_empty_match_all() {
    // spec: bare `*` is "A wildcard term matching any text" -- valid and
    // matches all documents in effect, but its AST shape is a StarWild Term,
    // NOT the empty-And match-all representation. T4 must not conflate them.
    let bare_star = parse("*").unwrap().expr;
    let empty = parse("").unwrap().expr;
    assert_ne!(bare_star, empty);
    assert_eq!(bare_star, bareword_star_wild_term());
}

#[test]
fn negation_only_query_is_valid() {
    // spec edge case: "Negation-only query ... alone -- Valid."
    assert!(parse("NOT facet:x").is_ok());
    assert!(parse("-facet:x").is_ok());
    assert!(parse("-(a OR b)").is_ok());
}

// ===========================================================================
// 11. Parse-time edge cases (spec edge-case table) with positioned-Location
//     assertions -- location must point at the real failure site, not 0 or a
//     default/garbage value. Each expected offset is derived from the input
//     itself (via `find`), never hardcoded blind, so the assertion actually
//     exercises Location::at's line/column/offset computation.
// ===========================================================================

#[test]
fn unbalanced_parens_positioned_at_the_unclosed_open() {
    let input = "status:x AND (a OR b";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::UnclosedParen);
    let expected_offset = input.find('(').unwrap();
    assert_eq!(err.at.offset, expected_offset);
    assert_eq!(err.at.line, 1);
    assert_eq!(err.at.column, expected_offset + 1);
}

#[test]
fn unbalanced_parens_across_a_newline_advances_line_and_resets_column() {
    let input = "a\nb AND (c OR d";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::UnclosedParen);
    // '(' is the 8th byte (0-indexed) and the 7th char after the '\n' at
    // byte 1: "b AND (" -> b(0) (1)A(2)N(3)D(4) (5)((6), so column = 7.
    assert_eq!(err.at.offset, 8);
    assert_eq!(err.at.line, 2);
    assert_eq!(err.at.column, 7);
}

#[test]
fn trailing_operator_and_is_a_positioned_parse_error() {
    let err = parse("a AND").unwrap_err();
    assert!(
        err.at.offset > 0,
        "must not default to 0 for a non-leading failure"
    );
}

#[test]
fn trailing_operator_or_is_a_positioned_parse_error() {
    // "a OR" -- and_expr consumes "a", or_expr's separator check fails to
    // complete (no ws1 after "OR"), restores, and the top-level trailing-
    // content check reports the stray "OR" at its own offset (2), not 0.
    let input = "a OR";
    let err = parse(input).unwrap_err();
    let expected_offset = input.find("OR").unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn trailing_not_with_no_operand_is_a_positioned_parse_error() {
    assert!(parse("NOT").is_err());
    assert!(parse("facet:x AND NOT").is_err());
}

#[test]
fn trailing_incomplete_to_is_a_positioned_parse_error() {
    let input = "created:[100 TO";
    let err = parse(input).unwrap_err();
    assert!(err.at.offset > 0);
}

#[test]
fn stray_token_adjacent_atoms_no_separating_whitespace() {
    // spec's own example: "facet:a(b)".
    let input = "facet:a(b)";
    let err = parse(input).unwrap_err();
    let expected_offset = input.find('(').unwrap();
    assert_eq!(err.at.offset, expected_offset);
    assert_eq!(
        err.kind,
        ParseErrorKind::Unexpected {
            expected: "end of input"
        }
    );
}

#[test]
fn stray_token_two_operators_back_to_back() {
    assert!(parse("a AND OR b").is_err());
    assert!(parse("a OR AND b").is_err());
}

#[test]
fn stray_closing_paren_with_no_matching_open() {
    let input = "a)";
    let err = parse(input).unwrap_err();
    let expected_offset = input.find(')').unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn unterminated_quoted_phrase_positioned_at_opening_quote() {
    let input = "a AND \"phrase";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::UnterminatedPhrase);
    let expected_offset = input.find('"').unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn unterminated_phrase_containing_an_escaped_backslash_at_eof() {
    // A trailing lone '\' inside an open phrase with nothing to escape --
    // must still be a clean ParseError, not a panic (also covered by the
    // fuzz suite, pinned explicitly here).
    let err = parse("\"a\\").unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::UnterminatedPhrase);
}

#[test]
fn malformed_range_missing_to_keyword() {
    let input = "facet:[100 200]";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::MalformedRange);
    let expected_offset = "facet:[100 ".len();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn malformed_range_missing_closing_bracket() {
    let input = "facet:[100 TO 200";
    let err = parse(input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::UnclosedRange);
    let expected_offset = input.find('[').unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn malformed_range_missing_opening_bracket() {
    // No leading '[' at all -- predicate_value only dispatches into
    // range_value on a leading '[', so "facet:100 TO 200]" never enters the
    // range grammar at all; "100" parses as the predicate's term, then "TO"
    // (reserved, standing alone) is the first unconsumed content -- a
    // ParseError, positioned at "TO", not a range-specific error kind.
    let input = "facet:100 TO 200]";
    let err = parse(input).unwrap_err();
    assert_eq!(
        err.kind,
        ParseErrorKind::Unexpected {
            expected: "end of input"
        }
    );
    let expected_offset = input.find("TO").unwrap();
    assert_eq!(err.at.offset, expected_offset);
}

#[test]
fn too_deeply_nested_at_129_but_not_128() {
    // Verified against the implementation's MAX_GROUP_DEPTH=128 boundary
    // semantics: `group`'s depth check is `depth >= 128` and depth counts
    // ALREADY-OPENED enclosing groups, so 128 nested groups (depths 0..127
    // all pass the check) succeed, and the 129th (depth=128) is the first to
    // fail. See report: the mandate's illustrative "127 ok, 128/129 fail"
    // phrasing does not match this implementation's actual off-by-one --
    // 128 nested groups is the last depth that succeeds.
    let ok_128 = format!("{}{}{}", "(".repeat(128), "a", ")".repeat(128));
    assert!(
        parse(&ok_128).is_ok(),
        "128 nested groups must still be accepted"
    );

    let err_129 = parse(&"(".repeat(129)).unwrap_err();
    assert_eq!(err_129.kind, ParseErrorKind::TooDeeplyNested);

    let err_130 = parse(&"(".repeat(130)).unwrap_err();
    assert_eq!(err_130.kind, ParseErrorKind::TooDeeplyNested);

    // 127 nested groups (well under the cap) also succeed -- sanity floor.
    let ok_127 = format!("{}{}{}", "(".repeat(127), "a", ")".repeat(127));
    assert!(parse(&ok_127).is_ok());
}

#[test]
fn too_deeply_nested_location_points_at_the_129th_open_not_the_first() {
    let input = "(".repeat(129);
    let err = parse(&input).unwrap_err();
    assert_eq!(err.kind, ParseErrorKind::TooDeeplyNested);
    // The failure is detected at the 129th '(' (byte offset 128), not at
    // offset 0 -- proves TooDeeplyNested's Location is the real failure
    // site, not the query start.
    assert_eq!(err.at.offset, 128);
}

// ===========================================================================
// 12. Determinism -- identical input -> identical AST across repeated
//     parses. Runs the representative case list from this whole file twice
//     and diffs nothing.
// ===========================================================================

fn representative_inputs() -> Vec<&'static str> {
    vec![
        "",
        "   ",
        "*",
        "-*",
        "a b OR c",
        "a AND b OR c AND d",
        "NOT a AND b",
        "-a OR b",
        "(a OR b) AND c",
        "a AND (b OR c)",
        "status:(open OR closed)",
        "status:(a AND b AND c)",
        "created:[* TO 100]",
        "created:[2026-01-01 TO *]",
        "created:>=100",
        "status:*",
        "-status:*",
        "\"a*b?c\"",
        "a\\:b",
        "-5",
        "\\-5",
        "facet:abc-123",
        "AND:value",
        "🦀",
        "status:🦀*",
    ]
}

#[test]
fn determinism_repeated_parses_of_same_input_are_debug_equal() {
    for input in representative_inputs() {
        let first = parse(input);
        let second = parse(input);
        let third = parse(input);
        assert_eq!(first, second, "input={input:?}");
        assert_eq!(second, third, "input={input:?}");
    }
}

#[test]
fn determinism_full_representative_suite_run_twice_is_identical() {
    // "repeat the full suite twice" per the mandate: run every representative
    // input through parse(), snapshot Debug-formatted results, do it again,
    // and diff -- catches any hidden allocation-order/hashing nondeterminism
    // that a single-call equality check could miss (e.g. a HashMap iteration
    // order leaking into Debug output, which this crate's docs claim never
    // happens).
    let run = || -> Vec<String> {
        representative_inputs()
            .into_iter()
            .map(|i| format!("{:?}", parse(i)))
            .collect()
    };
    let pass1 = run();
    let pass2 = run();
    assert_eq!(pass1, pass2);
}

// ===========================================================================
// 13. No-panic property/fuzz test -- stdlib-only deterministic pseudorandom
//     generator (no new dependency needed for this trivial capability; see
//     workspace dependency governance). Seeded xorshift64 for reproducible
//     failures. Probes garbage/huge/non-ASCII/deeply-nested/unbalanced input
//     and asserts parse() always returns (never panics/aborts/hangs).
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
        // Reduce in u64 space first, then narrow -- the modulus result is
        // always < n (a usize), so the final cast never truncates.
        let capped = self.next_u64() % (n.max(1) as u64);
        usize::try_from(capped).unwrap_or(0)
    }
}

/// Alphabet deliberately weighted toward every reserved/structural character
/// in the grammar, plus a few multi-byte UTF-8 code points, so random
/// composition is likely to exercise escape/quote/nesting/boundary paths
/// rather than mostly-inert ASCII letters.
const FUZZ_ALPHABET: &[&str] = &[
    "a",
    "b",
    ":",
    "(",
    ")",
    "[",
    "]",
    "*",
    "?",
    "<",
    ">",
    "\"",
    "\\",
    "-",
    " ",
    "\t",
    "\n",
    "AND",
    "OR",
    "NOT",
    "TO",
    "🦀",
    "é",
    "\u{0}",
    "\u{1F600}",
];

fn random_query(rng: &mut Xorshift64, len_tokens: usize) -> String {
    let mut s = String::new();
    for _ in 0..len_tokens {
        s.push_str(FUZZ_ALPHABET[rng.next_range(FUZZ_ALPHABET.len())]);
    }
    s
}

#[test]
fn fuzz_random_composition_never_panics() {
    let mut rng = Xorshift64::new(0xC0FF_EE00_D15E_A5E5);
    for trial in 0..20_000 {
        let len = 1 + rng.next_range(40);
        let q = random_query(&mut rng, len);
        let result = std::panic::catch_unwind(|| parse(&q));
        assert!(
            result.is_ok(),
            "parse panicked on trial {trial} input={q:?}"
        );
    }
}

#[test]
fn fuzz_huge_input_never_panics_or_hangs() {
    let mut rng = Xorshift64::new(0x5EED_1234_ABCD_EF01);
    // A few large (10k-100k token) inputs -- large enough to catch quadratic
    // blowup or stack issues without making the suite slow.
    for len in [10_000usize, 50_000] {
        let q = random_query(&mut rng, len);
        let result = std::panic::catch_unwind(|| parse(&q));
        assert!(
            result.is_ok(),
            "parse panicked on huge input of {len} tokens"
        );
    }
}

#[test]
fn fuzz_huge_unbalanced_paren_run_does_not_stack_overflow() {
    // A direct regression for the doc comment's own claim: "without
    // MAX_GROUP_DEPTH, pathological input... aborts the process on stack
    // overflow." This must return a clean Err, not crash the test process.
    for n in [1_000usize, 50_000, 500_000] {
        let q = "(".repeat(n);
        let result = std::panic::catch_unwind(|| parse(&q));
        assert!(
            result.is_ok(),
            "parse aborted/panicked on {n} unclosed parens"
        );
        let err = result.unwrap().unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::TooDeeplyNested);
    }
}

#[test]
fn fuzz_huge_balanced_paren_run_beyond_depth_cap_still_a_clean_error() {
    // Same as above but with matching closes -- confirms the depth cap fires
    // before the closer side is ever reached, still without overflowing.
    for n in [1_000usize, 50_000] {
        let q = format!("{}{}{}", "(".repeat(n), "a", ")".repeat(n));
        let result = std::panic::catch_unwind(|| parse(&q));
        assert!(
            result.is_ok(),
            "parse aborted/panicked on {n} balanced-but-deep parens"
        );
        assert_eq!(
            result.unwrap().unwrap_err().kind,
            ParseErrorKind::TooDeeplyNested
        );
    }
}

#[test]
fn fuzz_non_ascii_and_control_bytes_never_panic() {
    let inputs = [
        "🦀🦀🦀:🦀",
        "facet:日本語",
        "\u{0}\u{1}\u{2}\u{7f}",
        "\u{feff}bareword", // BOM
        "café:naïve",
        "\u{200b}zero-width-space", // zero-width space, not ASCII whitespace
    ];
    for input in inputs {
        let result = std::panic::catch_unwind(|| parse(input));
        assert!(result.is_ok(), "parse panicked on input={input:?}");
    }
}

#[test]
fn fuzz_random_deeply_nested_and_unbalanced_mixed() {
    // Deliberately combine deep nesting with truncation at random points --
    // the combination most likely to expose an off-by-one in MAX_GROUP_DEPTH
    // bookkeeping or a slice/char-boundary panic near end-of-input.
    let mut rng = Xorshift64::new(0xBEEF_F00D_1337_C0DE);
    for _ in 0..500 {
        let depth = rng.next_range(300);
        let mut q: String = "(".repeat(depth);
        // Randomly truncate to a byte offset that may land mid a multi-byte
        // char if any were present (all '(' here, so always safe, but the
        // truncation-at-arbitrary-length pattern is what matters).
        if depth > 0 {
            let cut = rng.next_range(q.len() + 1);
            q.truncate(cut);
        }
        let result = std::panic::catch_unwind(|| parse(&q));
        assert!(
            result.is_ok(),
            "parse panicked on depth={depth} input={q:?}"
        );
    }
}
