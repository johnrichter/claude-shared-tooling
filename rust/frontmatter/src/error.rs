//! Error type for [`crate::parse::parse`].
//!
//! No input this crate accepts ever panics — every malformed-input case
//! documented on [`crate::ParsedFrontmatter`] surfaces here as a typed
//! variant instead. A file with NO frontmatter block at all is deliberately
//! NOT an error (see [`crate::parse::parse`] doc comment) — it is a valid,
//! ordinary "body-only" document.

use std::fmt;

/// Why [`crate::parse::parse`] could not produce a [`crate::ParsedFrontmatter`].
///
/// Only two ways a frontmatter-bearing document can fail: the opening `---`
/// is present but never closed, or the YAML between the delimiters does not
/// parse. Anything else this crate is asked to handle (missing frontmatter,
/// empty frontmatter, wrong-typed individual fields, an empty file, a body
/// with no frontmatter, frontmatter with no body) is representable inside a
/// successful [`crate::ParsedFrontmatter`] and is never an error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FrontmatterParseError {
    /// The input opens with a `---` delimiter line but no closing `---`
    /// delimiter line is ever found before end of input.
    UnclosedDelimiter,
    /// The YAML between the delimiters is not well-formed YAML at all (a
    /// scan/parse error from the YAML backend). The wrapped `String` is the
    /// backend's own diagnostic message, for logging/debugging only — never
    /// pattern-matched on by a caller.
    MalformedYaml(String),
    /// The YAML between the delimiters parses, but its top-level shape is
    /// not a mapping (e.g. the frontmatter block is a bare scalar or a
    /// sequence) — frontmatter fields are only extractable from a mapping.
    NotAMapping,
}

impl fmt::Display for FrontmatterParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::UnclosedDelimiter => {
                write!(f, "frontmatter opening delimiter '---' is never closed")
            }
            Self::MalformedYaml(msg) => write!(f, "frontmatter YAML did not parse: {msg}"),
            Self::NotAMapping => {
                write!(f, "frontmatter block's top level is not a YAML mapping")
            }
        }
    }
}

impl std::error::Error for FrontmatterParseError {}
