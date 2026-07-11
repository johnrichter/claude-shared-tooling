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
/// Four ways a frontmatter-bearing document can fail: the opening `---` is
/// present but never closed; the YAML between the delimiters does not parse
/// or uses anchors/aliases (frontmatter never legitimately needs either, and
/// aliases are a billion-laughs amplification vector — see
/// [`Self::MalformedYaml`]); the frontmatter's block/flow nesting exceeds the
/// depth this crate will walk (see [`Self::TooDeeplyNested`]); or the YAML
/// parses but its top level is not a mapping. Anything else this crate is
/// asked to handle (missing frontmatter, empty frontmatter, wrong-typed
/// individual fields, an empty file, a body with no frontmatter, frontmatter
/// with no body) is representable inside a successful
/// [`crate::ParsedFrontmatter`] and is never an error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FrontmatterParseError {
    /// The input opens with a `---` delimiter line but no closing `---`
    /// delimiter line is ever found before end of input.
    UnclosedDelimiter,
    /// The YAML between the delimiters is not well-formed YAML at all (a
    /// scan/parse error from the YAML backend), OR the pre-scan
    /// (`parse::prescan_events`) found a YAML anchor/alias — frontmatter
    /// never legitimately uses anchors/aliases, and an alias event is a
    /// single un-expanded reference in the backend's DOM builder, so
    /// resolving it (as `YamlLoader` does) can amplify a tiny document into
    /// an exponential blowup (billion laughs); rejecting outright is
    /// simpler and safer than a size/count cap. The wrapped `String` is a
    /// diagnostic message (the YAML backend's own text, or this crate's own
    /// "contains a YAML anchor/alias" message) — for logging/debugging
    /// only, never pattern-matched on by a caller.
    MalformedYaml(String),
    /// The pre-scan (`parse::prescan_events`, module-private) found
    /// block/flow mapping or sequence nesting deeper than the crate's
    /// nesting cap (`parse::MAX_NESTING_DEPTH`, currently 64, also
    /// module-private) before building the YAML DOM. `YamlLoader`'s DOM
    /// builder is recursive descent with no depth cap of its own, so an
    /// unbounded nesting depth can overflow the call stack (a hard process
    /// abort, not a catchable panic) — this cap turns that into an ordinary
    /// typed error instead. Real frontmatter (a flat mapping of
    /// scalars/sequences, at most a couple of levels for a nested
    /// `workspace:` map) never approaches the cap.
    TooDeeplyNested,
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
            Self::TooDeeplyNested => write!(
                f,
                "frontmatter YAML nesting exceeds the depth this crate accepts"
            ),
            Self::NotAMapping => {
                write!(f, "frontmatter block's top level is not a YAML mapping")
            }
        }
    }
}

impl std::error::Error for FrontmatterParseError {}
