//! Bridges a parsed [`ParsedFrontmatter`] + a merged [`Profile`] to
//! `facetquery`'s generic [`facetquery::FacetSource`] trait, so a
//! `facetquery@1` query string can be matched against a frontmatter file.
//!
//! # Facet -> namespace mapping
//! A query facet name (`facet:value`'s `facet`) maps 1:1 to a frontmatter
//! tag NAMESPACE -- the part of a `namespace:value` tag before the colon
//! (e.g. `topic` in `topic:apm`). [`Profile::namespace_facet_type`] answers
//! whether the schema knows that namespace at all, and if so, its
//! [`FacetType`] (`String`/`Date`/`Numeric`/`DateInterval`); the
//! namespace's values are every `namespace:value` tag on the file,
//! value-only (the namespace prefix stripped), in tag order.
//!
//! # Exists / unknown / known-but-absent
//! [`facetquery::FacetLookup::Present`] is returned for every
//! SCHEMA-KNOWN namespace, even one this file has zero tags for --
//! `values` is simply empty in that case. `Unknown` is reserved for a
//! facet name that is not a schema namespace at all. This split matters
//! because `facetquery`'s `Exists` matcher (`facet:*`) requires a
//! non-empty `values` to match (see `facetquery::eval`'s `FacetLookup`
//! doc): returning `Unknown` for a known-but-absent namespace would make
//! every ordinary `topic:apm` predicate against a file with no `topic` tag
//! misreport as an `UnknownFacet` diagnostic, when `topic` is a perfectly
//! valid namespace the file just doesn't use.
//!
//! # Text fields for bareword matching
//! A bareword (no `facet:` prefix) full-text-searches `name`, `id`,
//! `description`, and `body_text` -- the whole convenience-field surface
//! plus the document body; a hit on any one field is a match. Unlike a
//! facet value's `Matcher::Term` (which glob-matches a whole value), a
//! bareword substring-searches: the term's pattern is matched anywhere
//! within a text field, not only against the field's entire content, since
//! a bareword's job is finding a word inside prose, not equating it to one.
//! Case-sensitive, same as every other match in the language (facet lookup
//! is documented case-sensitive too; this adapter doesn't special-case text).

use facetquery::{
    FacetLookup, FacetSource, FacetType as QueryFacetType, MatchResult, Query, Seg, Term,
};

use crate::{FacetType as SchemaFacetType, ParsedFrontmatter, Profile};

/// A [`facetquery::FacetSource`] over one parsed frontmatter file, scoped
/// by the [`Profile`] that defines which namespaces exist and their types.
pub struct FrontmatterFacetSource<'a> {
    parsed: &'a ParsedFrontmatter,
    profile: &'a Profile,
}

impl<'a> FrontmatterFacetSource<'a> {
    /// Builds a source over `parsed`, scoped by `profile`.
    #[must_use]
    pub fn new(parsed: &'a ParsedFrontmatter, profile: &'a Profile) -> Self {
        Self { parsed, profile }
    }

    /// This file's values for tag namespace `key` -- every `key:value` tag,
    /// value-only, in tag order. Empty if the file has no such tag,
    /// regardless of whether `key` is a schema-known namespace.
    fn namespace_values(&self, key: &str) -> Vec<String> {
        let prefix = format!("{key}:");
        self.parsed
            .tags
            .iter()
            .filter_map(|tag| tag.strip_prefix(&prefix).map(str::to_string))
            .collect()
    }

    /// The text fields a bareword full-text-searches, in a fixed order (not
    /// load-bearing for matching -- any hit anywhere makes `text_matches`
    /// `true` -- but fixed so the module's own tests can pin field
    /// coverage).
    fn text_fields(&self) -> [&str; 4] {
        [
            self.parsed.name.as_deref().unwrap_or(""),
            self.parsed.id.as_deref().unwrap_or(""),
            self.parsed.description.as_deref().unwrap_or(""),
            self.parsed.body_text.as_str(),
        ]
    }
}

impl FacetSource for FrontmatterFacetSource<'_> {
    fn facet(&self, key: &str) -> FacetLookup {
        let Some(schema_ty) = self.profile.namespace_facet_type(key) else {
            return FacetLookup::Unknown;
        };
        FacetLookup::Present {
            ty: to_query_facet_type(schema_ty),
            values: self.namespace_values(key),
        }
    }

    fn text_matches(&self, term: &Term) -> bool {
        self.text_fields()
            .iter()
            .any(|field| term_matches_substring(term, field))
    }
}

/// Maps this crate's schema-declared [`SchemaFacetType`] to `facetquery`'s
/// own [`QueryFacetType`] -- two independent enums (this crate's has no
/// `facetquery` dependency of its own to reuse) with the same variants,
/// kept in lockstep by the exhaustive match below.
fn to_query_facet_type(ty: SchemaFacetType) -> QueryFacetType {
    match ty {
        SchemaFacetType::String => QueryFacetType::String,
        SchemaFacetType::Date => QueryFacetType::Date,
        SchemaFacetType::Numeric => QueryFacetType::Numeric,
        SchemaFacetType::DateInterval => QueryFacetType::DateInterval,
    }
}

/// Whether `term`'s pattern matches anywhere within `text` -- a substring
/// glob search, not the whole-value equality [`facetquery`]'s own facet-value
/// matching uses. Implemented by padding the flattened pattern with an
/// implicit leading and trailing `*` and running one whole-string anchored
/// match: the added stars absorb whatever prefix/suffix of `text` falls
/// outside wherever the term's own pattern (which may itself contain `*`/`?`)
/// happens to land, which is exactly "found somewhere in the text".
fn term_matches_substring(term: &Term, text: &str) -> bool {
    let mut pattern = vec![CharTok::Star];
    pattern.extend(flatten(&term.segments));
    pattern.push(CharTok::Star);
    let chars: Vec<char> = text.chars().collect();
    glob_match_anchored(&pattern, &chars)
}

/// One token of a [`Term`]'s pattern, flattened from its `segments`.
enum CharTok {
    Char(char),
    Star,
    Question,
}

fn flatten(segments: &[Seg]) -> Vec<CharTok> {
    let mut out = Vec::new();
    for seg in segments {
        match seg {
            Seg::Literal(s) => out.extend(s.chars().map(CharTok::Char)),
            Seg::StarWild => out.push(CharTok::Star),
            Seg::QuestionWild => out.push(CharTok::Question),
            // `Seg` is `#[non_exhaustive]` (facetquery may add a segment
            // kind in a future minor version) -- an unrecognized segment
            // contributes nothing to the pattern rather than failing to
            // compile against a wider `Seg`.
            _ => {}
        }
    }
    out
}

/// Classic wildcard-glob matching, anchored at `text`'s start and end --
/// the same two-pointer-with-backtrack algorithm `facetquery`'s own
/// (private) evaluator uses for facet-value equality, reimplemented here
/// since it isn't exposed publicly. Always anchored; [`term_matches_substring`]
/// gets substring behavior by padding the pattern with `*` on both ends
/// before calling this, not by relaxing the anchoring here.
fn glob_match_anchored(pattern: &[CharTok], text: &[char]) -> bool {
    let (mut ti, mut pi) = (0usize, 0usize);
    let mut star: Option<usize> = None;
    let mut star_ti = 0usize;

    while ti < text.len() {
        match pattern.get(pi) {
            Some(CharTok::Char(c)) if *c == text[ti] => {
                pi += 1;
                ti += 1;
            }
            Some(CharTok::Question) => {
                pi += 1;
                ti += 1;
            }
            Some(CharTok::Star) => {
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
    while matches!(pattern.get(pi), Some(CharTok::Star)) {
        pi += 1;
    }
    pi == pattern.len()
}

/// Matches an already-parsed `query` against one parsed frontmatter file,
/// scoped by `profile`. Takes `&Query` (not a query string) so a caller
/// matching many files against the same query parses once (via
/// [`facetquery::parse`]) and calls this per file, rather than re-parsing
/// on every call.
#[must_use]
pub fn matches(parsed: &ParsedFrontmatter, query: &Query, profile: &Profile) -> MatchResult {
    let source = FrontmatterFacetSource::new(parsed, profile);
    facetquery::evaluate(query, &source)
}

#[cfg(test)]
mod tests {
    use super::*;
    use facetquery::EvalDiagnostic;

    fn parsed_with(tags: &[&str], name: Option<&str>, body: &str) -> ParsedFrontmatter {
        ParsedFrontmatter {
            tags: tags.iter().map(ToString::to_string).collect(),
            name: name.map(ToString::to_string),
            id: None,
            description: None,
            body_text: body.to_string(),
            raw_fields: crate::RawFields::from_ordered_pairs(Vec::new()),
        }
    }

    fn run(query_str: &str, parsed: &ParsedFrontmatter, profile: &Profile) -> MatchResult {
        let query = facetquery::parse(query_str).expect("test query must parse");
        matches(parsed, &query, profile)
    }

    // -- facet(): known-present / known-absent / unknown ---------------------

    #[test]
    fn facet_lookup_for_known_present_namespace_returns_type_and_values() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["topic:apm", "topic:tracing"], None, "");
        let source = FrontmatterFacetSource::new(&parsed, &profile);
        assert_eq!(
            source.facet("topic"),
            FacetLookup::Present {
                ty: QueryFacetType::String,
                values: vec!["apm".to_string(), "tracing".to_string()],
            }
        );
    }

    #[test]
    fn facet_lookup_for_known_but_absent_namespace_is_present_with_empty_values() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["type:knowledge"], None, "");
        let source = FrontmatterFacetSource::new(&parsed, &profile);
        assert_eq!(
            source.facet("topic"),
            FacetLookup::Present {
                ty: QueryFacetType::String,
                values: Vec::new(),
            },
            "a schema-known namespace this file doesn't use must be Present, not Unknown"
        );
    }

    #[test]
    fn facet_lookup_for_interval_typed_namespace_maps_to_query_date_interval_type() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["period:2026-04-01/2026-06-30"], None, "");
        let source = FrontmatterFacetSource::new(&parsed, &profile);
        assert_eq!(
            source.facet("period"),
            FacetLookup::Present {
                ty: QueryFacetType::DateInterval,
                values: vec!["2026-04-01/2026-06-30".to_string()],
            }
        );
    }

    #[test]
    fn facet_lookup_for_unknown_namespace_is_unknown() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["topic:apm"], None, "");
        let source = FrontmatterFacetSource::new(&parsed, &profile);
        assert_eq!(source.facet("not_a_real_namespace"), FacetLookup::Unknown);
    }

    // -- topic:apm / topic:* / genuinely-unknown facet ------------------------

    #[test]
    fn equality_predicate_matches_a_file_with_the_tag_not_one_without() {
        let profile = Profile::bundled_psa_apm();
        let with_tag = parsed_with(&["topic:apm"], None, "");
        let without_tag = parsed_with(&["type:knowledge"], None, "");
        assert!(run("topic:apm", &with_tag, &profile).matched);
        assert!(!run("topic:apm", &without_tag, &profile).matched);
    }

    #[test]
    fn exists_matches_iff_the_file_has_at_least_one_tag_in_that_namespace() {
        let profile = Profile::bundled_psa_apm();
        let with_tag = parsed_with(&["topic:apm"], None, "");
        let without_tag = parsed_with(&["type:knowledge"], None, "");
        assert!(run("topic:*", &with_tag, &profile).matched);
        assert!(!run("topic:*", &without_tag, &profile).matched);
    }

    #[test]
    fn known_but_absent_namespace_never_raises_unknown_facet() {
        let profile = Profile::bundled_psa_apm();
        let without_tag = parsed_with(&["type:knowledge"], None, "");
        let result = run("topic:apm", &without_tag, &profile);
        assert!(!result.matched);
        assert!(result.diagnostics.is_empty());
    }

    #[test]
    fn genuinely_unknown_facet_raises_unknown_facet_diagnostic() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["topic:apm"], None, "");
        let result = run("not_a_real_namespace:x", &parsed, &profile);
        assert!(!result.matched);
        assert_eq!(
            result.diagnostics,
            vec![EvalDiagnostic::UnknownFacet(
                "not_a_real_namespace".to_string()
            )]
        );
    }

    // -- period range (DateInterval-typed, overlap semantics) ----------------

    #[test]
    fn period_range_matches_an_interval_wholly_inside_the_query_window() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["period:2026-04-01/2026-06-30"], None, "");
        assert!(run("period:[2026-01-01 TO 2026-12-31]", &parsed, &profile).matched);
    }

    #[test]
    fn period_range_matches_a_single_day_within_the_stored_interval() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["period:2026-04-01/2026-06-30"], None, "");
        assert!(run("period:2026-05-15", &parsed, &profile).matched);
    }

    #[test]
    fn period_range_matches_a_partially_overlapping_window() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["period:2026-04-01/2026-06-30"], None, "");
        // The window starts before the stored interval ends and ends after
        // it starts -- a partial overlap, not containment either way.
        assert!(run("period:[2026-06-01 TO 2026-09-30]", &parsed, &profile).matched);
    }

    #[test]
    fn period_range_does_not_match_a_disjoint_window() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&["period:2026-04-01/2026-06-30"], None, "");
        assert!(!run("period:[2027-01-01 TO 2027-12-31]", &parsed, &profile).matched);
    }

    // -- text_matches: bareword over name/id/description/body ----------------

    #[test]
    fn bareword_matches_across_every_text_field() {
        let profile = Profile::bundled_psa_apm();
        let via_name = parsed_with(&[], Some("APM rollout plan"), "");
        let via_body = parsed_with(&[], None, "discusses the APM rollout in depth");
        let miss = parsed_with(&[], Some("unrelated"), "unrelated body");
        assert!(run("rollout", &via_name, &profile).matched);
        assert!(run("rollout", &via_body, &profile).matched);
        assert!(!run("rollout", &miss, &profile).matched);
    }

    #[test]
    fn bareword_substring_search_does_not_require_a_whole_field_match() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(&[], None, "one two three");
        assert!(run("two", &parsed, &profile).matched);
    }

    // -- matches(): end-to-end -------------------------------------------------

    #[test]
    fn matches_end_to_end_parses_a_query_string_and_matches_a_parsed_file() {
        let profile = Profile::bundled_psa_apm();
        let parsed = parsed_with(
            &["topic:apm", "type:knowledge"],
            Some("APM Doc"),
            "body text",
        );
        let query = facetquery::parse("topic:apm AND type:knowledge").unwrap();
        let result = matches(&parsed, &query, &profile);
        assert!(result.matched);
        assert_eq!(result.matched_facets, vec!["topic", "type"]);
    }
}
