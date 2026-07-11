//! Tokenization for BM25 term extraction.
//!
//! Ported faithfully from ka's `retrieve::bm25::tokenize` (the seed): a
//! whole-identifier tokenizer that keeps `snake_case`/`SCREAMING_SNAKE`
//! identifiers as one token (`dd_trace` never splits into `dd` + `trace`).
//! This preserves exact-match behavior for identifier-heavy corpora.
//!
//! # M1.P2.T2
//! An additional case-splitting tokenizer mode lands here (`camelCase`,
//! `snake_case`, `TitleCase`, `PascalCase`, `SCREAMING_SNAKE`, kebab-case, each
//! split into sub-tokens — `dd_trace` → `dd` + `trace`). The two modes will
//! be exposed as sibling functions (or variants of one enum/trait) so a
//! caller selects at call time without either mode's logic touching the
//! other's — see `M1.P2.T3` for the call-time selection surface.

use std::sync::LazyLock;

use regex::Regex;

/// Matches runs of lowercase letters, digits, and underscores. Applied after
/// lowercasing the input, so it also captures what were originally uppercase
/// letters — the net effect is "keep `[A-Za-z0-9_]` runs together as one
/// token, case-insensitively."
static WHOLE_IDENTIFIER_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"[a-z0-9_]+").expect("static regex is valid"));

/// Whole-identifier tokenizer (the seed mode): lowercases `text`, then splits
/// on anything that isn't `[a-z0-9_]`, keeping each surviving run as one
/// token. Identifiers with underscores stay intact — `dd_trace` is one
/// token, not two — because `_` is included in the token-character class.
///
/// Deterministic and pure: same `text` always yields the same `Vec<String>`,
/// in left-to-right order, with no locale-, thread-, or platform-dependent
/// behavior (lowercasing here is ASCII-oriented via the regex character
/// class, not `Unicode`-locale-sensitive casing).
///
/// Returns an empty vector for empty or all-punctuation input — never
/// panics.
#[must_use]
pub fn tokenize_whole_identifier(text: &str) -> Vec<String> {
    let lowered = text.to_lowercase();
    WHOLE_IDENTIFIER_RE
        .find_iter(&lowered)
        .map(|m| m.as_str().to_string())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Golden case named in the task spec: `dd_trace` must survive as one
    /// token — the whole point of the whole-identifier mode.
    #[test]
    fn keeps_underscored_identifier_whole() {
        assert_eq!(tokenize_whole_identifier("dd_trace"), vec!["dd_trace"]);
    }

    #[test]
    fn lowercases_mixed_case_input() {
        assert_eq!(tokenize_whole_identifier("DD_TRACE"), vec!["dd_trace"]);
        assert_eq!(
            tokenize_whole_identifier("DdTrace_Config"),
            vec!["ddtrace_config"]
        );
    }

    #[test]
    fn splits_on_punctuation_and_whitespace() {
        assert_eq!(
            tokenize_whole_identifier("dd_trace.Span, dd-agent!"),
            vec!["dd_trace", "span", "dd", "agent"]
        );
    }

    #[test]
    fn empty_and_all_punctuation_input_yields_no_tokens() {
        assert_eq!(tokenize_whole_identifier(""), Vec::<String>::new());
        assert_eq!(tokenize_whole_identifier("!!!---..."), Vec::<String>::new());
    }

    /// Digits inside and outside an identifier survive as part of the token
    /// — matches ka's `[a-z0-9_]+` (digits are in the character class).
    #[test]
    fn keeps_digits_in_and_out_of_identifiers() {
        assert_eq!(tokenize_whole_identifier("v2_client"), vec!["v2_client"]);
        assert_eq!(tokenize_whole_identifier("12345"), vec!["12345"]);
        assert_eq!(
            tokenize_whole_identifier("span_id123 456"),
            vec!["span_id123", "456"]
        );
    }

    /// Non-ASCII input: ka's `[a-z0-9_]+` character class is ASCII-only, so a
    /// non-ASCII code point is never part of a token — it acts exactly like
    /// any other non-matching punctuation and splits the surrounding run.
    /// `to_lowercase()` is Unicode-aware (it does NOT strip `é`), but the
    /// token regex still only keeps the ASCII-matching prefix/suffix runs.
    /// This is defined behavior, not a panic or a dropped document, and it
    /// matches ka's regex exactly (same pattern, same non-match on `é`).
    #[test]
    fn non_ascii_input_splits_around_the_non_matching_code_point() {
        assert_eq!(tokenize_whole_identifier("café"), vec!["caf"]);
        assert_eq!(
            tokenize_whole_identifier("naïve_parser"),
            vec!["na", "ve_parser"]
        );
        assert_eq!(tokenize_whole_identifier("日本語"), Vec::<String>::new());
    }
}
