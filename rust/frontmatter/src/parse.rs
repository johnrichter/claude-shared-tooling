//! The one frontmatter parser: splits a document into its leading YAML
//! frontmatter block and Markdown body, decodes the YAML via `yaml-rust2`,
//! and assembles a [`ParsedFrontmatter`]. This is the crate's only module
//! that references `yaml_rust2` types directly — see the crate root docs
//! for why that matters.
//!
//! # Delimiter grammar this module recognizes
//! - **Frontmatter present:** input's first line is exactly `---` (a
//!   trailing `\r` is tolerated); the block ends at the next line that is
//!   exactly `---`. Everything after that closing line is `body_text`,
//!   byte-identical to the source.
//! - **No frontmatter:** input's first line is not `---` (including an
//!   empty file, or any body-only document). The whole input becomes
//!   `body_text`; every convenience field is `None`/empty and `raw_fields`
//!   is empty. This is a successful parse, not an error — a document
//!   without frontmatter is ordinary input, not malformed input.
//! - **Unclosed frontmatter:** first line is `---` but no later line is
//!   `---` — [`FrontmatterParseError::UnclosedDelimiter`].
//! - **Malformed YAML / non-mapping top level:** the text between the
//!   delimiters fails to parse, or parses to something other than a
//!   mapping (empty frontmatter, `---\n---`, parses to no document at all
//!   and is treated as an empty mapping, not an error).
//! - **Anchors/aliases, or nesting past [`MAX_NESTING_DEPTH`]:** rejected by
//!   [`prescan_events`] before the YAML backend ever builds a DOM -- see
//!   that function's doc comment for why (hostile input that would
//!   otherwise abort the whole process, not just fail to parse).
//! - **Leading BOM:** exactly one leading `\u{feff}` (UTF-8 byte order
//!   mark) is stripped before delimiter detection; a BOM anywhere else in
//!   the input is left untouched.

use crate::error::FrontmatterParseError;
use crate::value::{FrontmatterValue, RawFields};
use crate::ParsedFrontmatter;
use yaml_rust2::parser::{Event, Parser};
use yaml_rust2::{Yaml, YamlLoader};

/// Maximum block/flow mapping+sequence nesting depth [`prescan_events`]
/// accepts before returning [`FrontmatterParseError::TooDeeplyNested`].
///
/// A conservative cap, not a measured stack-overflow threshold: real
/// frontmatter (a flat mapping of scalars/sequences, with at most the
/// `workspace:` nested map -- itself only 3-4 levels deep, or `tags:`-style
/// sequences at depth 2) never comes close. 64 leaves generous headroom
/// above any legitimate document while rejecting the adversarial deep-nesting
/// case (hundreds to thousands of levels) that would otherwise overflow
/// [`YamlLoader::load_from_str`]'s recursive-descent DOM builder.
pub const MAX_NESTING_DEPTH: usize = 64;

/// Parses `input` (a whole `.md` file's contents) into its frontmatter and
/// body.
///
/// # Errors
/// Returns [`FrontmatterParseError::UnclosedDelimiter`] when the input opens
/// with `---` but never closes it; [`FrontmatterParseError::MalformedYaml`]
/// when the YAML backend cannot scan/parse the frontmatter block, or the
/// block contains a YAML anchor/alias; [`FrontmatterParseError::TooDeeplyNested`]
/// when the block's mapping/sequence nesting exceeds this crate's nesting
/// cap (`MAX_NESTING_DEPTH`, currently 64); and
/// [`FrontmatterParseError::NotAMapping`] when the frontmatter block parses
/// but its top level is not a YAML mapping. Never panics or aborts the
/// process on any input, including an empty string, arbitrarily deep
/// nesting, or anchor/alias amplification -- see `prescan_events` below.
pub fn parse(input: &str) -> Result<ParsedFrontmatter, FrontmatterParseError> {
    // A leading UTF-8 BOM (U+FEFF) is invisible in an editor but makes line
    // 1 `\u{feff}---`, which fails the exact `== "---"` delimiter check
    // below -- without this strip, a BOM-saved file's entire frontmatter
    // silently routes to `body_text` with no error (MAJOR finding 3). Only
    // one leading BOM is consumed, and only at the absolute start of the
    // input; a BOM anywhere else (e.g. mid-body) is left verbatim.
    let input = input.strip_prefix('\u{feff}').unwrap_or(input);
    let lines: Vec<&str> = input.split('\n').collect();

    let Some(first_line) = lines.first() else {
        // `str::split` on any input (including "") always yields at least
        // one element, so this branch is unreachable in practice; kept as
        // a typed fallback instead of an `unwrap`/`expect` per the crate's
        // no-panic contract.
        return Ok(ParsedFrontmatter::body_only(input.to_string()));
    };

    if !is_delimiter_line(first_line) {
        // No opening delimiter -- the whole input is body, verbatim.
        return Ok(ParsedFrontmatter::body_only(input.to_string()));
    }

    let Some(closing_index) = lines
        .iter()
        .enumerate()
        .skip(1)
        .find(|(_, line)| is_delimiter_line(line))
        .map(|(idx, _)| idx)
    else {
        return Err(FrontmatterParseError::UnclosedDelimiter);
    };

    let yaml_text = lines[1..closing_index].join("\n");
    let body_text = lines[closing_index + 1..].join("\n");

    let raw_fields = parse_yaml_mapping(&yaml_text)?;
    Ok(ParsedFrontmatter::from_raw_fields(raw_fields, body_text))
}

/// A delimiter line is exactly `---`, tolerating one trailing `\r` (CRLF
/// source files).
fn is_delimiter_line(line: &str) -> bool {
    line.strip_suffix('\r').unwrap_or(line) == "---"
}

/// Linear, non-recursive event-level guard run before [`YamlLoader::load_from_str`]
/// builds a DOM: rejects two hostile-input shapes that would otherwise abort
/// the whole process (not a catchable panic) rather than return an `Err`
/// (BLOCKING findings 1+2).
///
/// Drives `yaml_rust2::parser::Parser::next_token` directly in a flat loop --
/// deliberately NOT `Parser::load`, whose `load_node`/`load_mapping`/
/// `load_sequence` trio is mutually recursive over nesting depth and would
/// reintroduce the same stack-growth problem this function exists to avoid.
/// `next_token` (backed by `Parser::state_machine`) emits one [`Event`] per
/// call with no recursion of its own, so this loop's call stack never grows
/// with input nesting depth, however deep.
///
/// # Errors
/// - [`FrontmatterParseError::TooDeeplyNested`] once combined
///   mapping/sequence nesting exceeds [`MAX_NESTING_DEPTH`]
///   ([`Event::MappingStart`]/[`Event::SequenceStart`] increment,
///   [`Event::MappingEnd`]/[`Event::SequenceEnd`] decrement).
/// - [`FrontmatterParseError::MalformedYaml`] the moment any
///   [`Event::Alias`] event appears. Frontmatter never legitimately uses
///   anchors/aliases; rejecting outright avoids having to size/count-cap an
///   alias's expansion (a resolved alias is a clone of its anchored node --
///   a chain of `N` doubling anchors is `2^N` nodes once expanded, the
///   "billion laughs" attack). This function itself never expands an
///   alias -- it only observes the event once.
/// - [`FrontmatterParseError::MalformedYaml`] if the backend's own scanner
///   errors first (e.g. genuinely malformed YAML) -- the pre-scan surfaces
///   that as the same variant [`parse_yaml_mapping`]'s later
///   `YamlLoader::load_from_str` call would have produced, so a caller sees
///   one consistent error shape regardless of which pass caught the
///   problem.
///
/// Returns `Ok(())` when the event stream exhausts (`Event::StreamEnd`)
/// within the depth cap and with no alias observed -- the subsequent
/// `YamlLoader::load_from_str` DOM build is unchanged and still runs.
fn prescan_events(yaml_text: &str) -> Result<(), FrontmatterParseError> {
    let mut parser = Parser::new_from_str(yaml_text);
    let mut depth: usize = 0;
    loop {
        let (event, _mark) = parser
            .next_token()
            .map_err(|err| FrontmatterParseError::MalformedYaml(err.to_string()))?;
        match event {
            Event::StreamEnd => return Ok(()),
            Event::MappingStart(..) | Event::SequenceStart(..) => {
                depth += 1;
                if depth > MAX_NESTING_DEPTH {
                    return Err(FrontmatterParseError::TooDeeplyNested);
                }
            }
            Event::MappingEnd | Event::SequenceEnd => {
                depth = depth.saturating_sub(1);
            }
            Event::Alias(..) => {
                return Err(FrontmatterParseError::MalformedYaml(
                    "frontmatter YAML contains a YAML anchor/alias, which is not supported"
                        .to_string(),
                ));
            }
            Event::Nothing
            | Event::StreamStart
            | Event::DocumentStart
            | Event::DocumentEnd
            | Event::Scalar(..) => {}
        }
    }
}

/// Parses `yaml_text` (the raw text between the two delimiter lines) and
/// converts it into an insertion-ordered [`RawFields`].
///
/// Empty input (the `---\n---` empty-frontmatter case) and a YAML document
/// that resolves to `Null` both mean "frontmatter present, zero fields" --
/// not an error, and not the same as "no frontmatter" (that case never
/// reaches this function; see [`parse`]).
fn parse_yaml_mapping(yaml_text: &str) -> Result<RawFields, FrontmatterParseError> {
    prescan_events(yaml_text)?;

    let docs = YamlLoader::load_from_str(yaml_text)
        .map_err(|err| FrontmatterParseError::MalformedYaml(err.to_string()))?;

    let Some(doc) = docs.first() else {
        return Ok(RawFields::from_ordered_pairs(Vec::new()));
    };

    match doc {
        Yaml::Null => Ok(RawFields::from_ordered_pairs(Vec::new())),
        Yaml::Hash(hash) => {
            let pairs = hash
                .iter()
                .map(|(k, v)| (yaml_key_to_string(k), yaml_to_value(v)))
                .collect();
            Ok(RawFields::from_ordered_pairs(pairs))
        }
        _ => Err(FrontmatterParseError::NotAMapping),
    }
}

/// Renders a mapping key as a `String`. Real frontmatter keys are always
/// plain YAML strings (`name:`, `tags:`, ...); this falls back to the same
/// best-effort scalar rendering [`yaml_to_value`] uses for scalar values, so
/// a YAML mapping with a non-string key (legal YAML, never seen in practice
/// here) still gets a stable, non-panicking key instead of being dropped.
fn yaml_key_to_string(key: &Yaml) -> String {
    scalar_to_string(key).unwrap_or_else(|| format!("{key:?}"))
}

/// Converts one YAML value into this crate's backend-agnostic
/// [`FrontmatterValue`]. See [`FrontmatterValue`]'s doc comment for the
/// scalar/sequence/other split rationale.
fn yaml_to_value(value: &Yaml) -> FrontmatterValue {
    if let Some(scalar) = scalar_to_string(value) {
        return FrontmatterValue::Scalar(scalar);
    }
    match value {
        Yaml::Array(items) => FrontmatterValue::Sequence(
            items
                .iter()
                .map(|item| scalar_to_string(item).unwrap_or_else(|| format!("{item:?}")))
                .collect(),
        ),
        Yaml::Hash(hash) => FrontmatterValue::Mapping(RawFields::from_ordered_pairs(
            hash.iter()
                .map(|(k, v)| (yaml_key_to_string(k), yaml_to_value(v)))
                .collect(),
        )),
        // `Null` and `BadValue` land here -- neither is a plain scalar, a
        // sequence, nor a mapping, which is exactly what `Other` means. A
        // `Null` (`key:` with nothing after it) is deliberately NOT a
        // `Scalar("")`: an explicit empty string and "no value given" are
        // different YAML shapes and the validator needs to tell them apart.
        // `Yaml::Alias` is NOT reachable here: `prescan_events` rejects any
        // document containing an `Event::Alias` before `YamlLoader` ever
        // builds a DOM, and aliases are otherwise resolved to their
        // anchored value at load time -- an unresolved `Yaml::Alias` never
        // surfaces from a successful `YamlLoader::load_from_str` call.
        _ => FrontmatterValue::Other,
    }
}

/// Renders a YAML scalar (string, integer, float, boolean) as its string
/// form. `Yaml::String` and `Yaml::Real` preserve the source representation
/// verbatim (`Yaml::Real` already stores the original numeral text
/// unmodified). `Yaml::Integer`, however, is NOT verbatim: `yaml-rust2`
/// parses the source numeral to an `i64` and this function re-renders that
/// `i64` in decimal via `i.to_string()` -- so a hex literal (`0x10`) comes
/// back as `"16"` and a leading-zero octal-looking literal (`007`) comes
/// back as `"7"`, not the original source text. Returns `None` for anything
/// that is not one of these four scalar variants.
fn scalar_to_string(value: &Yaml) -> Option<String> {
    match value {
        // `String` and `Real` both hold their source text verbatim already
        // (yaml-rust2 never reformats a float's numeral), so one arm covers
        // both.
        Yaml::String(s) | Yaml::Real(s) => Some(s.clone()),
        // Canonicalized to decimal, NOT verbatim -- see this function's doc
        // comment.
        Yaml::Integer(i) => Some(i.to_string()),
        Yaml::Boolean(b) => Some(b.to_string()),
        _ => None,
    }
}
