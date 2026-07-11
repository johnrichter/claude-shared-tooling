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

use crate::error::FrontmatterParseError;
use crate::value::{FrontmatterValue, RawFields};
use crate::ParsedFrontmatter;
use yaml_rust2::{Yaml, YamlLoader};

/// Parses `input` (a whole `.md` file's contents) into its frontmatter and
/// body.
///
/// # Errors
/// Returns [`FrontmatterParseError::UnclosedDelimiter`] when the input opens
/// with `---` but never closes it, [`FrontmatterParseError::MalformedYaml`]
/// when the YAML backend cannot scan/parse the frontmatter block, and
/// [`FrontmatterParseError::NotAMapping`] when the frontmatter block parses
/// but its top level is not a YAML mapping. Never panics on any input,
/// including an empty string.
pub fn parse(input: &str) -> Result<ParsedFrontmatter, FrontmatterParseError> {
    let lines: Vec<&str> = input.split('\n').collect();

    let Some(first_line) = lines.first() else {
        // `str::split` on any input (including "") always yields at least
        // one element, so this branch is unreachable in practice; kept as
        // a typed fallback instead of an `unwrap`/`expect` per the crate's
        // no-panic contract.
        return Ok(ParsedFrontmatter::body_only(input.to_string()));
    };

    if trim_delimiter_line(first_line) != Some(()) {
        // No opening delimiter -- the whole input is body, verbatim.
        return Ok(ParsedFrontmatter::body_only(input.to_string()));
    }

    let Some(closing_index) = lines
        .iter()
        .enumerate()
        .skip(1)
        .find(|(_, line)| trim_delimiter_line(line).is_some())
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
/// source files). Returns `Some(())` on a match so call sites read as
/// `.is_some()`/`!= Some(())` without a throwaway bool-vs-unit distinction.
fn trim_delimiter_line(line: &str) -> Option<()> {
    (line.strip_suffix('\r').unwrap_or(line) == "---").then_some(())
}

/// Parses `yaml_text` (the raw text between the two delimiter lines) and
/// converts it into an insertion-ordered [`RawFields`].
///
/// Empty input (the `---\n---` empty-frontmatter case) and a YAML document
/// that resolves to `Null` both mean "frontmatter present, zero fields" --
/// not an error, and not the same as "no frontmatter" (that case never
/// reaches this function; see [`parse`]).
fn parse_yaml_mapping(yaml_text: &str) -> Result<RawFields, FrontmatterParseError> {
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
        // `Null`, `Hash`, `Alias`, `BadValue` all land here -- none of them
        // is a plain scalar or a sequence, which is exactly what `Other`
        // means. A `Null` (`key:` with nothing after it) is deliberately
        // NOT a `Scalar("")`: an explicit empty string and "no value given"
        // are different YAML shapes and the validator needs to tell them
        // apart.
        _ => FrontmatterValue::Other,
    }
}

/// Renders a YAML scalar (string, integer, float, boolean) as its string
/// form, preserving the source representation rather than re-formatting it
/// (e.g. `Yaml::Real` already stores the original numeral text). Returns
/// `None` for anything that is not one of these four scalar variants.
fn scalar_to_string(value: &Yaml) -> Option<String> {
    match value {
        // `String` and `Real` both hold their source text verbatim already
        // (yaml-rust2 never reformats a float's numeral), so one arm covers
        // both.
        Yaml::String(s) | Yaml::Real(s) => Some(s.clone()),
        Yaml::Integer(i) => Some(i.to_string()),
        Yaml::Boolean(b) => Some(b.to_string()),
        _ => None,
    }
}
