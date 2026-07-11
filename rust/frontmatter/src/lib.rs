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

/// SDET adversarial probing for `M2.P2.T1a` verification -- targets the
/// robustness + determinism contract this crate promises the sole
/// platform-wide validator that consumes it: CRLF, duplicate keys,
/// non-string mapping keys, non-mapping top levels, YAML features that could
/// sneak past the "frontmatter is a flat mapping of scalars/sequences"
/// assumption, YAML-1.2-vs-1.1 type pinning, encoding/size edges, delimiter
/// edge cases, and determinism. Every case here asserts a well-typed
/// `Ok`/`Err`, never a crash -- except `extreme_nesting_depth_...`, which
/// documents a case that crashes the *process* (not a catchable panic) and
/// is therefore isolated in a child process rather than asserted in-line.
#[cfg(test)]
mod sdet_adversarial_tests {
    use super::*;

    // -- CRLF line endings --------------------------------------------------

    #[test]
    fn crlf_delimiters_and_body_parse_and_preserve_crlf_in_body() {
        let input =
            "---\r\nname: \"x\"\r\ntags:\r\n  - a\r\n---\r\nbody line one\r\nbody line two\r\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("x".to_string()));
        assert_eq!(parsed.tags, vec!["a".to_string()]);
        // `\r` is not part of the delimiter grammar's match target beyond
        // the tolerated trailing strip on the delimiter line itself -- body
        // text is copied verbatim, so its `\r\n` line endings survive.
        assert_eq!(parsed.body_text, "body line one\r\nbody line two\r\n");
    }

    #[test]
    fn crlf_closing_delimiter_with_trailing_content_on_same_physical_line_still_closes() {
        // The closing delimiter line is `---\r` after `split('\n')` -- the
        // trailing `\r` must be stripped for the match to fire.
        let input = "---\r\nname: \"x\"\r\n---\r\nbody\r\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("x".to_string()));
        assert_eq!(parsed.body_text, "body\r\n");
    }

    // -- Duplicate top-level keys (research-flagged parity point) ----------

    #[test]
    fn duplicate_top_level_key_is_a_typed_malformed_yaml_error_not_last_wins() {
        // PINS current behavior: yaml-rust2 treats a duplicate mapping key
        // as a *scan error*, not silent last-wins. This is a parity
        // divergence from PyYAML (silent last-wins under YAML 1.1) that the
        // task spec flagged as a research point -- recording it here so
        // the divergence is visible and any cutover-parity decision is made
        // deliberately, not discovered later. Deterministic and does not
        // panic either way.
        let input = "---\nid: first\nid: second\n---\nbody\n";
        let result = parse(input);
        assert!(
            matches!(result, Err(FrontmatterParseError::MalformedYaml(ref msg)) if msg.contains("duplicated key")),
            "expected a MalformedYaml error mentioning the duplicate key, got {result:?}"
        );
    }

    #[test]
    fn duplicate_key_error_is_deterministic_across_repeated_parses() {
        let input = "---\nid: first\nid: second\n---\nbody\n";
        assert_eq!(parse(input), parse(input));
    }

    // -- Non-string mapping keys --------------------------------------------

    #[test]
    fn integer_and_boolean_mapping_keys_stringify_via_the_scalar_fallback() {
        let input = "---\n123: x\ntrue: y\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(
            parsed.raw_fields.get("123"),
            Some(&FrontmatterValue::Scalar("x".to_string()))
        );
        assert_eq!(
            parsed.raw_fields.get("true"),
            Some(&FrontmatterValue::Scalar("y".to_string()))
        );
    }

    #[test]
    fn sequence_and_mapping_keys_stringify_via_the_debug_fallback_without_panicking() {
        // Neither `[a]` nor `{}` is a scalar key, so `yaml_key_to_string`
        // falls through to `format!("{key:?}")`. The exact debug rendering
        // is an implementation detail; what matters is it never panics and
        // produces a stable, lookupable key.
        let input = "---\n[a]: z\n{}: w\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.raw_fields.len(), 2);
        let keys: Vec<&str> = parsed.raw_fields.iter().map(|(k, _)| k).collect();
        assert!(keys.iter().any(|k| k.contains('a')), "keys were: {keys:?}");
        assert!(
            keys.iter().any(|k| k.contains("Hash") || k.contains('{')),
            "keys were: {keys:?}"
        );
    }

    // -- NotAMapping at top level --------------------------------------------

    #[test]
    fn bare_sequence_top_level_is_not_a_mapping_error() {
        let input = "---\n- a\n- b\n---\nbody\n";
        assert_eq!(parse(input), Err(FrontmatterParseError::NotAMapping));
    }

    #[test]
    fn bare_scalar_top_level_is_not_a_mapping_error() {
        let input = "---\njust a scalar\n---\nbody\n";
        assert_eq!(parse(input), Err(FrontmatterParseError::NotAMapping));
    }

    // -- YAML features that could sneak in -----------------------------------

    #[test]
    fn anchors_and_aliases_resolve_to_their_scalar_value_without_panicking() {
        let input = "---\nname: &a foo\nid: *a\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("foo".to_string()));
        assert_eq!(parsed.id, Some("foo".to_string()));
    }

    #[test]
    fn block_scalar_literal_preserves_embedded_newlines_as_one_scalar() {
        let input = "---\ndescription: |\n  line1\n  line2\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.description, Some("line1\nline2\n".to_string()));
    }

    #[test]
    fn flow_sequence_is_a_sequence_and_flow_mapping_is_other() {
        let input = "---\ntags: [a, b]\nmeta: {k: v}\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.tags, vec!["a".to_string(), "b".to_string()]);
        assert_eq!(
            parsed.raw_fields.get("meta"),
            Some(&FrontmatterValue::Other)
        );
    }

    #[test]
    fn tabs_as_block_indentation_are_spec_invalid_and_surface_as_malformed_yaml() {
        // YAML forbids tabs for block indentation; yaml-rust2 rejects this
        // as a scan error rather than silently accepting or panicking.
        let input = "---\nname: x\ntags:\n\t- a\n---\nbody\n";
        let result = parse(input);
        assert!(
            matches!(result, Err(FrontmatterParseError::MalformedYaml(_))),
            "expected MalformedYaml for tab indentation, got {result:?}"
        );
    }

    #[test]
    fn document_marker_inside_the_frontmatter_block_ends_the_block_at_first_dashes() {
        // A `---` line inside what looks like a multi-document YAML stream
        // is, from this crate's own delimiter grammar (not YAML's), the
        // *closing* delimiter -- the parser's line-based scan finds it
        // before any YAML multi-document semantics would apply. Everything
        // from that line onward becomes body_text, including a second
        // `key: value` line that never reaches the YAML backend at all.
        let input = "---\nname: x\n---\nname2: y\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("x".to_string()));
        assert_eq!(parsed.raw_fields.len(), 1);
        assert_eq!(parsed.body_text, "name2: y\n---\nbody\n");
    }

    // -- YAML 1.2 vs PyYAML 1.1 type pinning ---------------------------------

    #[test]
    fn bare_on_off_land_as_scalar_strings_not_booleans() {
        let input = "---\nflag_a: on\nflag_b: off\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(
            parsed.raw_fields.get("flag_a"),
            Some(&FrontmatterValue::Scalar("on".to_string()))
        );
        assert_eq!(
            parsed.raw_fields.get("flag_b"),
            Some(&FrontmatterValue::Scalar("off".to_string()))
        );
    }

    #[test]
    fn null_and_tilde_are_other_not_a_scalar_empty_string() {
        // Explicit YAML null (`~`, `null`) is deliberately `Other`, not
        // `Scalar("")` -- see `yaml_to_value`'s doc comment. Pinning both
        // spellings.
        let input = "---\nfoo: ~\nbar: null\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.raw_fields.get("foo"), Some(&FrontmatterValue::Other));
        assert_eq!(parsed.raw_fields.get("bar"), Some(&FrontmatterValue::Other));
    }

    #[test]
    fn numeric_looking_scalars_preserve_source_numeral_text() {
        let input = "---\ncount: 123\nratio: 1.5\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(
            parsed.raw_fields.get("count"),
            Some(&FrontmatterValue::Scalar("123".to_string()))
        );
        assert_eq!(
            parsed.raw_fields.get("ratio"),
            Some(&FrontmatterValue::Scalar("1.5".to_string()))
        );
    }

    // -- Encoding / size edges ------------------------------------------------

    #[test]
    fn utf8_bom_prefix_before_opening_delimiter_defeats_delimiter_detection() {
        // A literal U+FEFF BOM character is not `-`, so the first line is
        // not exactly `---` and the whole input -- BOM included -- becomes
        // body text. Not an error, but flags a real-world gotcha: a
        // BOM-prefixed file silently loses its frontmatter rather than
        // failing loudly. Escalation-worthy but not a panic/crash, so
        // documented here rather than "fixed" unilaterally.
        let input = "\u{feff}---\nname: x\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, None);
        assert_eq!(parsed.body_text, input);
    }

    #[test]
    fn large_flat_frontmatter_block_parses_without_hanging() {
        use std::fmt::Write as _;
        let mut input = String::from("---\n");
        for i in 0..5_000 {
            let _ = writeln!(input, "key{i}: value{i}");
        }
        input.push_str("---\nbody\n");
        let start = std::time::Instant::now();
        let parsed = parse(&input).unwrap();
        assert_eq!(parsed.raw_fields.len(), 5_000);
        assert!(
            start.elapsed().as_secs() < 5,
            "large flat frontmatter block took too long: {:?}",
            start.elapsed()
        );
    }

    /// Builds a YAML block sequence nested `depth` levels deep under one
    /// top-level key, e.g. depth 3: `key:\n- nested:\n  - nested:\n    -
    /// nested:\n`.
    fn deeply_nested_yaml(depth: usize) -> String {
        let mut yaml = String::from("key:\n");
        for i in 0..depth {
            yaml.push_str(&"  ".repeat(i));
            yaml.push_str("- nested:\n");
        }
        format!("---\n{yaml}---\nbody\n")
    }

    #[test]
    fn moderately_deep_nesting_parses_without_panicking() {
        // Within the depth a libtest worker thread's default (smaller than
        // the process main thread's) stack tolerates. Empirically bracketed
        // on a `std::thread::spawn`-default-stack thread during
        // verification: depth 300 -> Ok, depth 400 -> stack overflow;
        // 100 keeps a comfortable margin below that thread-local threshold.
        let input = deeply_nested_yaml(100);
        let result = parse(&input);
        assert!(result.is_ok(), "expected Ok for depth 100, got {result:?}");
    }

    #[test]
    fn extreme_nesting_depth_crashes_the_process_not_a_catchable_panic_escalated_defect() {
        // DEFECT, escalated rather than fixed: `YamlLoader::load_from_str`
        // is a recursive-descent parser with no depth cap. On a default-size
        // thread stack it overflows well under 1000 levels of nested block
        // sequences (empirically bracketed on a libtest-style worker thread
        // during verification: depth 300 -> Ok, depth 400 -> stack
        // overflow -- far lower than the ~1000-2000 threshold measured on a
        // full-size main-thread stack). It blows the OS thread stack, which
        // Rust cannot turn into a catchable `panic!` -- it aborts the whole
        // process (`std::panic::catch_unwind` does not help; this is a hard
        // abort, not unwinding). That violates this crate's own "never
        // panics on any input" contract in the strictest possible way: a
        // hostile frontmatter block can kill the *entire host process*
        // (e.g. every in-flight navigator request, not just one parse), and
        // the depth needed is well within what a real file could contain.
        //
        // This is deliberately run in an isolated child process: were it
        // run in-line, the abort would kill this whole test binary (every
        // sibling test, not just this one). The child process is expected
        // to fail to complete normally at depth 5000 -- that IS the
        // documented, pinned defect; a green assertion here would mean the
        // bug regressed to something worse (e.g. silently corrupting
        // memory) or was fixed (worth re-checking this test then).
        if std::env::var("FRONTMATTER_SDET_DEEP_NEST_CHILD").is_ok() {
            let input = deeply_nested_yaml(5_000);
            let _ = parse(&input); // never reached if the abort fires first
            return;
        }

        let exe = std::env::current_exe().expect("test binary path");
        let status = std::process::Command::new(exe)
            .args([
                "--exact",
                "sdet_adversarial_tests::extreme_nesting_depth_crashes_the_process_not_a_catchable_panic_escalated_defect",
                "--test-threads=1",
                "--nocapture",
            ])
            .env("FRONTMATTER_SDET_DEEP_NEST_CHILD", "1")
            .status()
            .expect("failed to spawn isolated child process for the deep-nesting probe");
        assert!(
            !status.success(),
            "expected the child process to abort on extreme nesting depth (documenting a \
             known stack-overflow defect in yaml-rust2's recursive descent) -- it exited \
             successfully instead, which means either the defect was fixed (update this \
             test to assert Ok and remove the escalation) or the depth chosen here no longer \
             reproduces it (re-bracket the threshold)"
        );
    }

    // -- Delimiter edge cases -------------------------------------------------

    #[test]
    fn opener_not_on_line_one_is_treated_as_no_frontmatter() {
        let input = "line0\n---\nname: x\n---\nbody\n";
        let parsed = parse(input).unwrap();
        assert!(parsed.raw_fields.is_empty());
        assert_eq!(parsed.body_text, input);
    }

    #[test]
    fn triple_dash_inside_body_text_is_left_alone() {
        let input = "---\nname: x\n---\nbody\n---\nmore\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("x".to_string()));
        assert_eq!(parsed.body_text, "body\n---\nmore\n");
    }

    #[test]
    fn only_an_opener_with_no_closing_line_is_unclosed_delimiter() {
        assert_eq!(
            parse("---\n"),
            Err(FrontmatterParseError::UnclosedDelimiter)
        );
        assert_eq!(parse("---"), Err(FrontmatterParseError::UnclosedDelimiter));
    }

    #[test]
    fn empty_string_input_does_not_panic() {
        let parsed = parse("").unwrap();
        assert_eq!(parsed.body_text, "");
        assert!(parsed.raw_fields.is_empty());
    }

    #[test]
    fn whitespace_only_input_is_treated_as_no_frontmatter() {
        let input = "   \n\t\n";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.body_text, input);
        assert!(parsed.raw_fields.is_empty());
    }

    #[test]
    fn empty_frontmatter_block_dashes_only_yields_zero_fields() {
        let input = "---\n---\n";
        let parsed = parse(input).unwrap();
        assert!(parsed.raw_fields.is_empty());
        assert_eq!(parsed.body_text, "");
    }

    #[test]
    fn trailing_body_content_with_no_final_newline_is_preserved_verbatim() {
        let input = "---\nname: x\n---\nbody-no-newline";
        let parsed = parse(input).unwrap();
        assert_eq!(parsed.name, Some("x".to_string()));
        assert_eq!(parsed.body_text, "body-no-newline");
    }

    // -- Determinism ----------------------------------------------------------

    #[test]
    fn repeated_parse_of_a_non_trivial_input_is_field_and_order_identical() {
        let input = "---\nname: \"Doc\"\nid: knowledge-base:test:doc\ntags:\n  - a:b\n  - c:d\nupdated: 2026-07-11T00:00:00Z\nflag: yes\ncount: 42\n---\n# Body\nSome text.\n";
        let first = parse(input).unwrap();
        let second = parse(input).unwrap();
        assert_eq!(
            first, second,
            "two parses of identical input must be field-equal"
        );
        let first_keys: Vec<&str> = first.raw_fields.iter().map(|(k, _)| k).collect();
        let second_keys: Vec<&str> = second.raw_fields.iter().map(|(k, _)| k).collect();
        assert_eq!(
            first_keys, second_keys,
            "raw_fields order must also be identical"
        );
    }

    #[test]
    fn duplicate_key_error_message_is_stable_across_repeated_parses() {
        // Determinism must hold on the error path too, not just Ok.
        let input = "---\nid: first\nid: second\n---\nbody\n";
        let first = parse(input);
        let second = parse(input);
        assert_eq!(first, second);
    }
}
