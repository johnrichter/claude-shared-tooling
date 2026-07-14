//! SDET verification for M2.P4.T2 (`FacetSource` adapter + Exists refinement).
//! Targets the mandate's specific adversarial compositions, not just the
//! implementer's own unit tests: double-negative `NOT facet:*`, AND/OR over
//! a mixed present/absent facet pair, multibyte + wildcard bareword text,
//! glob-backtrack blowup resistance, and cross-call determinism.
//! Public-API-only (`frontmatter::{parse, matches, Profile}` and friends) --
//! exercises the crate the way a real caller would, not its internals.

use frontmatter::{matches, Profile, RawFields};

/// A self-authored extension pack -- not a shipped bundle -- carrying the
/// generic namespace/rule-set shape this file's adversarial cases need
/// (`type`/`status`/`privacy`/`owner`/`topic`/`source`/`period` + a
/// `type:report` rule-set), so the query/validate compositions under test
/// never couple to a specific real pack's vocabulary. Deliberately omits
/// `bogus`, the undeclared facet several cases below query on purpose.
const SYNTHETIC_PACK_JSON: &str = r#"{
  "kind": "extension-pack",
  "profile": "synthetic-adversarial-test",
  "version": "synthetic-adversarial-test@1",
  "extends": "core@2",
  "description": "Self-authored test fixture pack for query/validate adversarial coverage.",
  "required_fields": [
    { "field": "name", "authorship": "human_authored", "source": "test fixture" },
    { "field": "description", "authorship": "human_authored", "source": "test fixture" },
    { "field": "id", "authorship": "human_authored", "source": "test fixture" },
    { "field": "tags", "authorship": "human_authored", "source": "test fixture" },
    { "field": "links", "authorship": "human_authored", "source": "test fixture" },
    { "field": "updated", "authorship": "machine_derivable", "source": "test fixture" }
  ],
  "description_caps": { "context": 350, "skill": 500, "agent": 750, "source": "test fixture" },
  "file_class": { "default": "context", "rules": [], "note": "test fixture" },
  "namespaces": [
    { "name": "type", "cardinality": "singleton", "source": "test fixture" },
    { "name": "status", "cardinality": "singleton", "source": "test fixture" },
    { "name": "privacy", "cardinality": "singleton", "source": "test fixture" },
    { "name": "owner", "cardinality": "singleton", "source": "test fixture" },
    { "name": "topic", "cardinality": "at_least_one", "source": "test fixture" },
    { "name": "source", "cardinality": "optional", "source": "test fixture" },
    { "name": "period", "cardinality": "optional", "type": "date_interval", "source": "test fixture" }
  ],
  "rule_sets": [
    {
      "match": { "namespace": "type", "value": "report" },
      "apply": {
        "require_namespaces": ["source", "period"],
        "forbidden_unless_matched": ["source", "period"],
        "value_formats": [
          { "namespace": "period", "regex": "^[0-9]{4}-[0-9]{2}-[0-9]{2}/[0-9]{4}-[0-9]{2}-[0-9]{2}$", "message": "'{value}' is not YYYY-MM-DD/YYYY-MM-DD" }
        ]
      },
      "source": "test fixture"
    }
  ],
  "exempt": { "filenames": [], "dir_components": [], "path_globs": [], "source": "test fixture" }
}"#;

fn profile() -> Profile {
    Profile::from_pack_json(SYNTHETIC_PACK_JSON)
        .expect("SYNTHETIC_PACK_JSON must deserialize into a valid Profile")
}

fn parsed(tags: &[&str], name: Option<&str>, body: &str) -> frontmatter::ParsedFrontmatter {
    frontmatter::ParsedFrontmatter {
        tags: tags.iter().map(ToString::to_string).collect(),
        name: name.map(ToString::to_string),
        id: None,
        description: None,
        body_text: body.to_string(),
        raw_fields: RawFields::from_ordered_pairs(Vec::new()),
    }
}

fn run(q: &str, p: &frontmatter::ParsedFrontmatter, profile: &Profile) -> facetquery::MatchResult {
    let query = facetquery::parse(q).expect("test query must parse");
    matches(p, &query, profile)
}

// -- NOT facet:* double-negative -------------------------------------------
// The mandate's crux case: `NOT topic:*` on a file WITHOUT a `topic` tag
// must now MATCH (absence of a facet fails Exists, and NOT negates that
// failure), and on a file WITH a `topic` tag must NOT match. This is the
// one composition a naive `Exists = any Present` implementation would get
// backwards in the negated form even if the un-negated `topic:*` looked
// right, because `NOT` only inverts a bool -- it can't distinguish
// "Present-empty" from "Present-nonempty" itself; that distinction has to
// already be correct inside `Exists` before negation is applied.
#[test]
fn not_topic_star_matches_a_file_with_no_topic_tag() {
    let profile = profile();
    let no_topic = parsed(&["type:knowledge"], None, "");
    let result = run("NOT topic:*", &no_topic, &profile);
    assert!(
        result.matched,
        "NOT topic:* must match a file with zero topic tags"
    );
    assert!(
        result.diagnostics.is_empty(),
        "type is schema-known; no UnknownFacet expected"
    );
}

#[test]
fn not_topic_star_does_not_match_a_file_with_a_topic_tag() {
    let profile = profile();
    let with_topic = parsed(&["topic:apm"], None, "");
    let result = run("NOT topic:*", &with_topic, &profile);
    assert!(
        !result.matched,
        "NOT topic:* must not match a file that has a topic tag"
    );
}

#[test]
fn dash_prefixed_not_exists_is_equivalent_to_the_not_keyword_form() {
    let profile = profile();
    let no_topic = parsed(&["type:knowledge"], None, "");
    let with_topic = parsed(&["topic:apm"], None, "");
    assert_eq!(
        run("-topic:*", &no_topic, &profile).matched,
        run("NOT topic:*", &no_topic, &profile).matched
    );
    assert_eq!(
        run("-topic:*", &with_topic, &profile).matched,
        run("NOT topic:*", &with_topic, &profile).matched
    );
}

// -- AND / OR over one present + one absent schema-known facet -------------

#[test]
fn and_of_exists_over_present_and_absent_facet_requires_both() {
    let profile = profile();
    // has topic, no period -- use type (present) and topic (present) vs a
    // genuinely absent-but-known namespace. period is schema-known and
    // commonly unset on a non-report file.
    let has_topic_no_period = parsed(&["topic:apm", "type:knowledge"], None, "");
    let both_present = parsed(&["topic:apm", "period:2026-01-01"], None, "");

    let and_result = run("topic:* AND period:*", &has_topic_no_period, &profile);
    assert!(
        !and_result.matched,
        "AND must fail when one side has zero values"
    );
    assert!(
        and_result.diagnostics.is_empty(),
        "both facets are schema-known; no diagnostics"
    );

    assert!(run("topic:* AND period:*", &both_present, &profile).matched);
}

#[test]
fn or_of_exists_over_present_and_absent_facet_needs_only_one() {
    let profile = profile();
    let only_topic = parsed(&["topic:apm"], None, "");
    let neither = parsed(&["type:knowledge"], None, "");

    assert!(run("topic:* OR period:*", &only_topic, &profile).matched);
    assert!(!run("topic:* OR period:*", &neither, &profile).matched);
}

#[test]
fn not_exists_composed_with_and_isolates_the_negated_side() {
    let profile = profile();
    // topic present, period absent: `topic:* AND NOT period:*` should
    // match (has topic, and truthfully lacks period).
    let file = parsed(&["topic:apm"], None, "");
    assert!(run("topic:* AND NOT period:*", &file, &profile).matched);
    // Once period is set, the same query must flip to no-match.
    let with_period = parsed(&["topic:apm", "period:2026-01-01"], None, "");
    assert!(!run("topic:* AND NOT period:*", &with_period, &profile).matched);
}

// -- multi-valued namespace, tag order preserved ---------------------------

#[test]
fn multi_valued_namespace_preserves_file_tag_order_in_values() {
    let profile = profile();
    let file = parsed(&["topic:tracing", "topic:apm", "topic:profiling"], None, "");
    let source = frontmatter::FrontmatterFacetSource::new(&file, &profile);
    match facetquery::FacetSource::facet(&source, "topic") {
        facetquery::FacetLookup::Present { values, .. } => {
            assert_eq!(values, vec!["tracing", "apm", "profiling"]);
        }
        other => panic!("expected Present, got {other:?}"),
    }
}

// -- text_matches hardening: multibyte + wildcard bareword ------------------

#[test]
fn multibyte_bareword_matches_inside_multibyte_body_text() {
    let profile = profile();
    // Body contains multibyte (Japanese) text with the target term
    // embedded mid-string, flanked by other multibyte characters on both
    // sides -- exercises char-index (not byte-index) substring walking.
    let file = parsed(&[], None, "背景はAPMのトレーシング機能について説明します");
    assert!(run("APM", &file, &profile).matched);
}

#[test]
fn multibyte_bareword_with_question_wildcard_matches_one_char_gap() {
    let profile = profile();
    let file = parsed(&[], None, "トレース の 話");
    // "?" must consume exactly one multibyte char, not one byte.
    assert!(run("トレ?ス", &file, &profile).matched);
}

#[test]
fn bareword_with_star_wildcard_matches_across_a_gap_in_body() {
    let profile = profile();
    let file = parsed(&[], None, "the rollout of the new tracing feature");
    assert!(run("rollout*tracing", &file, &profile).matched);
}

#[test]
fn bareword_case_sensitivity_is_consistent_with_facet_matching() {
    let profile = profile();
    let file = parsed(&["topic:apm"], None, "Rollout Plan");
    // Facet matching is documented case-sensitive; text_matches must not
    // silently diverge into case-insensitive behavior.
    assert!(
        !run("rollout", &file, &profile).matched,
        "bareword should be case-sensitive like facet matching"
    );
    assert!(run("Rollout", &file, &profile).matched);
}

#[test]
fn many_star_bareword_pattern_does_not_hang_on_a_non_matching_long_text() {
    let profile = profile();
    // A long text with no `z` at all, against a many-`*` pattern that
    // ultimately requires a `z` -- classic catastrophic-backtrack bait for
    // a naive backtracking glob. The two-pointer/star-checkpoint algorithm
    // used here is linear-time per star, not exponential; this must
    // complete promptly regardless.
    let body = "a".repeat(20_000);
    let file = parsed(&[], None, &body);
    let pattern = "*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*z*";
    let result = run(pattern, &file, &profile);
    assert!(
        !result.matched,
        "text has no 'z' at all, so this must be a clean no-match"
    );
}

// -- purity + determinism ---------------------------------------------------

#[test]
fn matches_is_deterministic_across_repeated_calls_on_the_same_inputs() {
    let profile = profile();
    let file = parsed(
        &["topic:apm", "topic:tracing", "type:knowledge"],
        Some("APM Doc"),
        "body",
    );
    let query = facetquery::parse("topic:apm AND type:* AND NOT status:*").unwrap();
    let first = matches(&file, &query, &profile);
    let second = matches(&file, &query, &profile);
    assert_eq!(
        first, second,
        "matches must be a pure function of (parsed, query, profile)"
    );
}

// -- unknown facet inside a boolean composition ------------------------------

#[test]
fn unknown_facet_inside_and_composition_is_a_no_match_with_diagnostic_but_query_still_evaluates() {
    let profile = profile();
    let file = parsed(&["topic:apm"], None, "");
    let result = run("topic:apm AND bogus:x", &file, &profile);
    assert!(!result.matched);
    assert_eq!(
        result.diagnostics,
        vec![facetquery::EvalDiagnostic::UnknownFacet(
            "bogus".to_string()
        )]
    );
    // topic:apm on its own still contributed to matched_facets even though
    // the overall AND failed -- matched_facets is independent of the
    // overall boolean outcome per facetquery::eval's documented contract.
    assert_eq!(result.matched_facets, vec!["topic".to_string()]);
}

// -- report file: type:report + period range end-to-end ---------------------

#[test]
fn report_file_with_period_range_and_type_report_matches_end_to_end() {
    let profile = profile();
    let file = parsed(&["type:report", "period:2026-04-01/2026-06-30"], None, "");
    let result = run(
        "type:report AND period:[2026-01-01 TO 2026-12-31]",
        &file,
        &profile,
    );
    assert!(result.matched);
}

// -- Interval Stage 2 hardening: end-to-end schema/query interaction -------
// The pack's `period.regex` is a pure digit/slash shape check with no
// ordering constraint, so an inverted interval (`end/start`) or a legacy
// `..`-separated value interact with schema validation and query overlap
// in ways worth pinning explicitly at the crate's public API, not just
// inside facetquery's own unit tests.
//
// `validate()` now closes the shape-only gap with a real-calendar second
// gate (`is_real_calendar_interval`): a shape-valid but inverted interval
// is `INVALID_PERIOD_FORMAT`, matching what the query side already treated
// as unmatchable.

#[test]
fn inverted_period_value_fails_schema_validation_and_never_matches_a_query() {
    let profile = profile();
    let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\n  - period:2026-06-30/2026-04-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
    let parsed = frontmatter::parse(input).unwrap();
    let entry = frontmatter::validate(&parsed, "some/report.md", &profile);
    assert!(
        !entry.is_valid,
        "a shape-valid but inverted interval must fail the real-calendar gate"
    );
    assert!(entry
        .violations
        .iter()
        .any(|v| v.code == "INVALID_PERIOD_FORMAT" && v.field == "period"));

    // The same value is also silently unmatchable by ANY query -- overlap
    // evaluation rejects start > end, so a file with only this period value
    // can never surface under a period query, consistent with it also now
    // being flagged invalid at validation time.
    let result = run("period:[2026-01-01 TO 2026-12-31]", &parsed, &profile);
    assert!(
        !result.matched,
        "inverted interval must never satisfy any period query"
    );
    assert!(result.diagnostics.is_empty());
}

#[test]
fn legacy_dotdot_period_value_is_rejected_by_the_pack_regex() {
    let profile = profile();
    let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\n  - period:2026-04-01..2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
    let parsed = frontmatter::parse(input).unwrap();
    let entry = frontmatter::validate(&parsed, "some/report.md", &profile);
    assert!(
        !entry.is_valid,
        "legacy `..` separator must now fail validation"
    );
    assert!(entry
        .violations
        .iter()
        .any(|v| v.code == "INVALID_PERIOD_FORMAT"));
}

#[test]
fn one_side_malformed_period_value_is_rejected_by_the_pack_regex() {
    let profile = profile();
    for bad in ["2026-04-01/bad", "2026-04-01/2026-06-30/2026-07-01"] {
        let input = format!("---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\n  - period:{bad}\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n");
        let parsed = frontmatter::parse(&input).unwrap();
        let entry = frontmatter::validate(&parsed, "some/report.md", &profile);
        assert!(
            !entry.is_valid,
            "malformed period {bad:?} must fail validation"
        );
    }
}
