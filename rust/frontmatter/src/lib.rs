//! `frontmatter` — the ONE YAML-frontmatter-plus-Markdown-body parser for
//! navigator. Every navigator subcommand and the cache it feeds consumes
//! this crate's [`ParsedFrontmatter`] — there is exactly one parse
//! implementation in navigator, not one per consumer.
//!
//! # Scope (`M2.P2.T1a` — this task)
//! Parsing only: split a document into its frontmatter block and Markdown
//! body, decode the YAML, and hand back a typed, order-preserving result.
//! No schema validation, no required-field checks, no `FrontmatterEntry`
//! shape -- those are `M2.P2.T1b`'s `validate` module, landing next in this
//! same crate (see "Module layout" below).
//!
//! # What's here
//! - [`ParsedFrontmatter`] -- the crate's one public result type. Four
//!   convenience fields (`tags`, `name`, `id`, `description`) for the
//!   happy path, plus `body_text` (the Markdown after the frontmatter) and
//!   `raw_fields` (every top-level key, in source order, typed enough to
//!   tell scalar/sequence/other apart) for anything needing more than the
//!   four convenience fields.
//! - [`FrontmatterValue`] / [`RawFields`] -- the backend-agnostic value
//!   representation `raw_fields` is built from. No `yaml-rust2` type
//!   ([`yaml_rust2::Yaml`] or otherwise) appears anywhere in this crate's
//!   public API -- only [`parse::parse`] (module-private beyond its `pub
//!   fn`) ever imports `yaml_rust2`, so the YAML backend is swappable later
//!   without a breaking change to any consumer.
//! - [`FrontmatterParseError`] -- the parser's only two failure modes
//!   (unclosed delimiter, malformed/non-mapping YAML). A document with NO
//!   frontmatter at all is not an error -- see [`parse::parse`]'s doc
//!   comment.
//!
//! # Module layout (and where `validate` slots in next)
//! - `parse` -- this task's parser. Owns the only `yaml_rust2` import in
//!   the crate.
//! - `value` -- [`FrontmatterValue`] and [`RawFields`], the backend-agnostic
//!   result shape `parse` produces and any future module consumes.
//! - `error` -- [`FrontmatterParseError`].
//! - (this file) -- [`ParsedFrontmatter`] and the crate's public
//!   re-exports.
//!
//! `M2.P2.T1b` adds a sibling `validate` module (`pub mod validate;` here)
//! that takes a `&ParsedFrontmatter` (specifically its `raw_fields`) and
//! produces the schema-checked `FrontmatterEntry` shape -- it needs nothing
//! from `parse` beyond the already-public [`ParsedFrontmatter`] type, so it
//! slots in as a new sibling module with no change to this task's code.
//!
//! # Determinism
//! [`parse::parse`] is a pure function of its `&str` input: identical input
//! bytes always produce a byte-identical [`ParsedFrontmatter`] (including
//! `raw_fields`' key order), on every platform and call. No filesystem,
//! network, clock, or map-iteration-order dependency anywhere in this
//! crate.

#![deny(unsafe_code)]

mod error;
mod parse;
mod value;

pub use error::FrontmatterParseError;
pub use parse::parse;
pub use value::{FrontmatterValue, RawFields};

/// The result of parsing one `.md` file's leading YAML frontmatter and
/// trailing Markdown body.
///
/// Produced only by [`parse`]. The four convenience fields
/// (`tags`/`name`/`id`/`description`) are the happy-path accessors: each is
/// populated when the corresponding top-level frontmatter key is present
/// AND holds the expected shape (a sequence for `tags`, a scalar for the
/// other three); otherwise it is `None`/empty, never an error -- a
/// wrong-typed or absent convenience field is a validation concern
/// (`M2.P2.T1b`), not a parse failure.
///
/// `raw_fields` is the source of truth for every top-level key this
/// document had, in source order, typed enough to distinguish
/// scalar/sequence/other -- everything the validator will need that the
/// four convenience fields don't carry.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedFrontmatter {
    /// `tags:` when present and a sequence; empty otherwise (absent, or
    /// present but not a sequence -- e.g. a bare scalar).
    pub tags: Vec<String>,
    /// `name:` when present and a scalar; `None` otherwise.
    pub name: Option<String>,
    /// `id:` when present and a scalar; `None` otherwise.
    pub id: Option<String>,
    /// `description:` when present and a scalar; `None` otherwise.
    pub description: Option<String>,
    /// The Markdown content after the closing frontmatter delimiter,
    /// byte-identical to the source. When there is no frontmatter at all,
    /// this is the entire input, byte-identical.
    pub body_text: String,
    /// Every top-level frontmatter key, in source insertion order, each
    /// tagged scalar/sequence/other. Empty when there is no frontmatter, or
    /// frontmatter is present but has zero fields (`---\n---`).
    pub raw_fields: RawFields,
}

impl ParsedFrontmatter {
    /// Builds the "no frontmatter present" result: every convenience field
    /// empty, `raw_fields` empty, `body_text` set to the entire input
    /// verbatim. Used by [`parse::parse`] for a document whose first line
    /// is not an opening `---` delimiter (including an empty file).
    pub(crate) fn body_only(body_text: String) -> Self {
        Self {
            tags: Vec::new(),
            name: None,
            id: None,
            description: None,
            body_text,
            raw_fields: RawFields::from_ordered_pairs(Vec::new()),
        }
    }

    /// Builds a result from an already-decoded [`RawFields`] and the
    /// verbatim body text, deriving the four convenience fields from
    /// `raw_fields` by looking up the well-known keys and checking their
    /// [`FrontmatterValue`] shape.
    pub(crate) fn from_raw_fields(raw_fields: RawFields, body_text: String) -> Self {
        let tags = match raw_fields.get("tags") {
            Some(FrontmatterValue::Sequence(items)) => items.clone(),
            _ => Vec::new(),
        };
        let scalar_field = |key: &str| match raw_fields.get(key) {
            Some(FrontmatterValue::Scalar(s)) => Some(s.clone()),
            _ => None,
        };
        Self {
            tags,
            name: scalar_field("name"),
            id: scalar_field("id"),
            description: scalar_field("description"),
            body_text,
            raw_fields,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // -- Happy path: convenience fields ---------------------------------

    #[test]
    fn extracts_all_four_convenience_fields_and_body() {
        let input = "---\n\
name: \"Test Doc\"\n\
id: \"knowledge-base:test:doc\"\n\
description: \"A test document\"\n\
tags:\n\
  - type:knowledge\n\
  - topic:testing\n\
updated: 2026-07-11T00:00:00Z\n\
---\n\
# Body heading\n\
Body content.\n";
        let parsed = parse(input).expect("well-formed frontmatter must parse");
        assert_eq!(parsed.name, Some("Test Doc".to_string()));
        assert_eq!(parsed.id, Some("knowledge-base:test:doc".to_string()));
        assert_eq!(parsed.description, Some("A test document".to_string()));
        assert_eq!(
            parsed.tags,
            vec!["type:knowledge".to_string(), "topic:testing".to_string()]
        );
        assert_eq!(parsed.body_text, "# Body heading\nBody content.\n");
    }

    #[test]
    fn raw_fields_preserves_all_keys_order_and_scalar_vs_sequence() {
        let input = "---\n\
name: \"Doc\"\n\
tags:\n\
  - a:b\n\
updated: 2026-07-11T00:00:00Z\n\
status: complete\n\
---\n\
body\n";
        let parsed = parse(input).unwrap();
        let keys: Vec<&str> = parsed.raw_fields.iter().map(|(k, _)| k).collect();
        assert_eq!(
            keys,
            vec!["name", "tags", "updated", "status"],
            "raw_fields must replay source key order"
        );
        assert_eq!(
            parsed.raw_fields.get("name"),
            Some(&FrontmatterValue::Scalar("Doc".to_string()))
        );
        assert_eq!(
            parsed.raw_fields.get("tags"),
            Some(&FrontmatterValue::Sequence(vec!["a:b".to_string()]))
        );
    }

    #[test]
    fn body_text_extraction_is_verbatim() {
        let input = "---\nname: \"x\"\n---\nLine one.\n\nLine two.\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.body_text, "Line one.\n\nLine two.\n");
    }

    // -- No-panic edge cases ---------------------------------------------

    #[test]
    fn missing_frontmatter_returns_whole_input_as_body() {
        let input = "# Just a heading\nNo frontmatter here.\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.body_text, input);
        assert!(parsed.raw_fields.is_empty());
        assert_eq!(parsed.name, None);
    }

    #[test]
    fn empty_frontmatter_yields_empty_raw_fields_and_preserves_body() {
        let input = "---\n---\nbody text\n";
        let parsed = parse(input).unwrap();
        assert!(parsed.raw_fields.is_empty());
        assert_eq!(parsed.body_text, "body text\n");
    }

    #[test]
    fn empty_file_does_not_panic_and_has_empty_body() {
        let parsed = parse("").unwrap();
        assert_eq!(parsed.body_text, "");
        assert!(parsed.raw_fields.is_empty());
    }

    #[test]
    fn frontmatter_with_no_body_yields_empty_body_text() {
        let input = "---\nname: \"x\"\n---";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.body_text, "");
        assert_eq!(parsed.name, Some("x".to_string()));
    }

    #[test]
    fn body_with_no_frontmatter_is_not_an_error() {
        let input = "no delimiters at all, just prose --- mid-sentence\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.body_text, input);
    }

    #[test]
    fn malformed_yaml_returns_typed_error_not_panic() {
        let input = "---\ntags: [unclosed\n---\nbody\n";
        let result = parse(input);
        assert!(matches!(
            result,
            Err(FrontmatterParseError::MalformedYaml(_))
        ));
    }

    #[test]
    fn unclosed_frontmatter_returns_typed_error() {
        let input = "---\nname: \"x\"\nbody with no closing delimiter\n";
        let result = parse(input);
        assert_eq!(result, Err(FrontmatterParseError::UnclosedDelimiter));
    }

    #[test]
    fn non_mapping_frontmatter_returns_typed_error() {
        let input = "---\n- just\n- a\n- list\n---\nbody\n";
        let result = parse(input);
        assert_eq!(result, Err(FrontmatterParseError::NotAMapping));
    }

    // -- Wrong-type convenience fields ------------------------------------

    #[test]
    fn tags_as_scalar_yields_empty_convenience_tags_but_raw_fields_keeps_the_scalar() {
        let input = "---\ntags: not-a-list\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.tags, Vec::<String>::new());
        assert_eq!(
            parsed.raw_fields.get("tags"),
            Some(&FrontmatterValue::Scalar("not-a-list".to_string()))
        );
    }

    #[test]
    fn description_as_sequence_yields_none_convenience_field_but_raw_fields_keeps_the_sequence() {
        let input = "---\ndescription:\n  - one\n  - two\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.description, None);
        assert_eq!(
            parsed.raw_fields.get("description"),
            Some(&FrontmatterValue::Sequence(vec![
                "one".to_string(),
                "two".to_string()
            ]))
        );
    }

    // -- YAML 1.2 strict-typing pin ----------------------------------------

    #[test]
    fn unquoted_iso_timestamp_lands_as_scalar_string_not_a_typed_timestamp() {
        let input = "---\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(
            parsed.raw_fields.get("updated"),
            Some(&FrontmatterValue::Scalar(
                "2026-07-11T00:00:00Z".to_string()
            )),
            "yaml-rust2 is strict YAML 1.2: an unquoted timestamp is a string, not a \
             backend-typed timestamp -- this pins that behavior"
        );
    }

    #[test]
    fn bare_yes_no_land_as_scalar_strings_not_booleans() {
        let input = "---\nprivacy_flag: yes\nother_flag: no\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(
            parsed.raw_fields.get("privacy_flag"),
            Some(&FrontmatterValue::Scalar("yes".to_string())),
            "yaml-rust2 is strict YAML 1.2: bare 'yes' is a string, not a boolean (PyYAML \
             1.1 would type this as bool) -- this pins that behavior"
        );
        assert_eq!(
            parsed.raw_fields.get("other_flag"),
            Some(&FrontmatterValue::Scalar("no".to_string()))
        );
    }

    // -- Determinism -------------------------------------------------------

    #[test]
    fn identical_input_yields_byte_identical_result() {
        let input = "---\nname: \"x\"\ntags:\n  - a:b\n  - c:d\n---\nbody\n";
        let first = parse(input).unwrap();
        let second = parse(input).unwrap();
        assert_eq!(first, second);
    }
}
