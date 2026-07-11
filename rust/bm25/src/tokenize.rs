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

/// All-case-splitting tokenizer mode: lowercases and splits `text` into
/// sub-tokens across every common identifier-casing convention, so
/// `ddTrace`, `dd_trace`, `DD_TRACE`, `DdTrace`, and `dd-trace` all tokenize
/// to `["dd", "trace"]` — a `trace` query then matches every casing.
/// Complements [`tokenize_whole_identifier`], which keeps `dd_trace` as one
/// token; this fn is the sibling for lexical search where sub-word matches
/// matter more than exact-identifier matches.
///
/// # Case-boundary rules (the crux)
/// Scans `text` char-by-char (not via `regex`: the split conditions below
/// need one-character lookahead/lookbehind, which the `regex` crate's
/// automaton engine doesn't support). A new token starts at index `i` when:
/// - The char at `i` is not ASCII alphanumeric — separator (`_`, `-`, `.`,
///   whitespace, punctuation, any non-ASCII code point) — never emitted,
///   just ends the current token, e.g. `dd_trace` and `dd-trace` and
///   `dd.trace` all yield `["dd", "trace"]`.
/// - Lower/digit → upper transition (camelCase boundary): `ddTrace` splits
///   between `d` and `T`.
/// - Upper → upper → lower transition (acronym boundary): a run of capitals
///   ends one char before a new capitalized word starts, so `HTTPServer`
///   splits between `P` and `S` → `http`, `server` (not `h`,`t`,`t`,
///   `pserver`), and `DDTrace`/`DD_TRACE`/`DdTrace` all agree with `ddTrace`.
///
/// Every emitted token is ASCII-lowercased char-by-char as it's built —
/// deterministic left-to-right, no locale-sensitive `Unicode` casing.
///
/// # Digit handling (decided — QR-confirmed M1.P2.T2, kept as implemented)
/// The spec doesn't pin digit behavior; the rule is: **digits attach to the
/// run they're adjacent to, like a lowercase letter, EXCEPT that a digit
/// immediately followed by an uppercase letter still triggers the camelCase
/// split** (digit is in the lower/digit → upper trigger above). Complete rule,
/// including every boundary sub-case:
/// - `span2` → `["span2"]`, `utf8` → `["utf8"]`, `id123` → `["id123"]` —
///   digit suffixes stay attached to their word (most useful for lexical
///   identifier search: `span2` is one lexical unit, not noise-token `2`).
/// - `v2Client` → `["v2", "client"]` — a digit run still ends a token right
///   before a new camelCase word starts, same as a lowercase letter would.
/// - `2Client` → `["2", "client"]` — a **bare leading digit** (nothing before
///   it) followed by an uppercase word still splits, emitting the digit as a
///   lone single-char token. This is the corner of the digit→upper rule: the
///   split fires on the transition regardless of what precedes the digit.
/// - `2fast` → `["2fast"]` — digit→lowercase never splits.
/// - `HTTP2Server` → `["http2", "server"]` — a digit fused onto a preceding
///   acronym run attaches to the acronym (no digit→upper transition at the
///   acronym's own tail), then splits before the next camelCase word.
///
/// Rationale (why kept): bare numeric tokens (`2`) rarely help retrieval on
/// their own, so digits default to merging with their neighboring letters;
/// but digits must not swallow the next real word when a case transition is
/// present. The only cost is a rare lone `2` token from `2Client`-shaped
/// input — identifiers almost never start with a digit (most languages forbid
/// it), so the lone-digit-token noise is negligible and does not outweigh the
/// benefit of keeping `v2`/`client` separately matchable. Query `trace` still
/// matches every casing of `dd_trace` because digits never alter the letter
/// tokens.
///
/// # Unicode (decided — drop non-ASCII, for parity; one known limitation)
/// Token membership is ASCII-alphanumeric-only (`is_ascii_alphanumeric`). A
/// non-ASCII code point is treated exactly like punctuation — it ends the
/// current token and is never itself emitted — so `café` → `["caf"]`. This is
/// deliberate: it holds this mode in lockstep with [`tokenize_whole_identifier`]
/// (a faithful port of ka's ASCII-only `[a-z0-9_]+` regex) on the ASCII subset,
/// so a query and an indexed document always drop non-ASCII identically. The
/// two modes MUST agree here — they may differ only in the extra case-boundary
/// splits — so changing Unicode handling is out of scope for a single mode.
///
/// Known limitation (accepted for v1): dropping loses fidelity on accented
/// proper nouns in natural-language frontmatter — e.g. `José` → `["jos"]`, so
/// a `jose` query will NOT match. This is acceptable for an ASCII-identifier /
/// English-frontmatter search tool, and no token is silently *lost* (the
/// ASCII remainder is still indexed), but it is a real recall gap for accented
/// names. If accented `name:`-field content later proves to matter, the fix is
/// Unicode-lowercase-and-keep or transliteration (e.g. `é`→`e`) applied to
/// **both** tokenizers together to preserve their parity — a cross-mode change
/// the operator should schedule as its own task, not a per-mode divergence.
///
/// Deterministic and pure: same `text` always yields the same `Vec<String>`
/// in left-to-right order; no I/O, clock, or locale dependency. Returns an
/// empty vector for empty or all-separator input — never panics.
///
/// # M1.P2.T3
/// Same signature as [`tokenize_whole_identifier`] — `fn(&str) -> Vec<String>`
/// — by design, so the call-time selection API in M1.P2.T3 can hold either
/// tokenizer behind one function-pointer/closure type and inject it into
/// `okapi`/`bm25f` uniformly, without either scorer knowing which mode it
/// got.
#[must_use]
pub fn tokenize_all_case_split(text: &str) -> Vec<String> {
    let chars: Vec<char> = text.chars().collect();
    let mut tokens = Vec::new();
    let mut current = String::new();

    for i in 0..chars.len() {
        let c = chars[i];

        if !c.is_ascii_alphanumeric() {
            if !current.is_empty() {
                tokens.push(std::mem::take(&mut current));
            }
            continue;
        }

        if !current.is_empty() {
            // Safe: `current` non-empty means the previous char was ASCII
            // alphanumeric too (any separator would have flushed and reset
            // it above), so `chars[i - 1]` is the char last pushed.
            let prev = chars[i - 1];
            let acronym_boundary = prev.is_ascii_uppercase()
                && matches!(chars.get(i + 1), Some(&next) if next.is_ascii_lowercase());
            let camel_boundary =
                prev.is_ascii_lowercase() || prev.is_ascii_digit() || acronym_boundary;
            if c.is_ascii_uppercase() && camel_boundary {
                tokens.push(std::mem::take(&mut current));
            }
        }

        current.push(c.to_ascii_lowercase());
    }

    if !current.is_empty() {
        tokens.push(current);
    }

    tokens
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

#[cfg(test)]
mod all_case_split_tests {
    use super::*;

    /// The 5 REQUIRED examples from the task spec: every casing of the same
    /// identifier must tokenize identically, so a `trace` query matches all.
    #[test]
    fn required_examples_all_agree() {
        assert_eq!(tokenize_all_case_split("ddTrace"), vec!["dd", "trace"]);
        assert_eq!(tokenize_all_case_split("dd_trace"), vec!["dd", "trace"]);
        assert_eq!(tokenize_all_case_split("DD_TRACE"), vec!["dd", "trace"]);
        assert_eq!(tokenize_all_case_split("DdTrace"), vec!["dd", "trace"]);
        assert_eq!(tokenize_all_case_split("dd-trace"), vec!["dd", "trace"]);
    }

    #[test]
    fn splits_camel_case() {
        assert_eq!(
            tokenize_all_case_split("getUserId"),
            vec!["get", "user", "id"]
        );
    }

    #[test]
    fn splits_snake_case() {
        assert_eq!(
            tokenize_all_case_split("get_user_id"),
            vec!["get", "user", "id"]
        );
    }

    #[test]
    fn splits_title_case_and_pascal_case() {
        assert_eq!(
            tokenize_all_case_split("GetUserId"),
            vec!["get", "user", "id"]
        );
        assert_eq!(
            tokenize_all_case_split("PascalCaseIdentifier"),
            vec!["pascal", "case", "identifier"]
        );
    }

    #[test]
    fn splits_screaming_snake_case() {
        assert_eq!(
            tokenize_all_case_split("GET_USER_ID"),
            vec!["get", "user", "id"]
        );
    }

    #[test]
    fn splits_kebab_case() {
        assert_eq!(
            tokenize_all_case_split("get-user-id"),
            vec!["get", "user", "id"]
        );
    }

    #[test]
    fn splits_dotted_identifiers() {
        assert_eq!(
            tokenize_all_case_split("dd_trace.SpanContext"),
            vec!["dd", "trace", "span", "context"]
        );
    }

    #[test]
    fn splits_whitespace_and_punctuation_separated() {
        assert_eq!(
            tokenize_all_case_split("get user, id! (span)"),
            vec!["get", "user", "id", "span"]
        );
    }

    /// Acronym boundary: an uppercase run ends one char before a new
    /// capitalized word starts (`HTTP` + `Response`, not `H`,`T`,`T`,
    /// `PResponse`), so mixed acronym/camelCase identifiers split correctly.
    #[test]
    fn mixed_identifiers_with_acronyms() {
        assert_eq!(
            tokenize_all_case_split("getHTTPResponseCode"),
            vec!["get", "http", "response", "code"]
        );
        assert_eq!(
            tokenize_all_case_split("parseURL2JSON"),
            vec!["parse", "url2", "json"]
        );
    }

    /// Digit-handling decision (decided rule — see doc comment on
    /// [`tokenize_all_case_split`]): digits attach to the adjacent letter
    /// run like a lowercase letter (`span2`, `utf8`, `v2`, `id123` stay
    /// whole), EXCEPT a digit immediately followed by an uppercase letter
    /// still triggers the camelCase split, same as a lowercase letter would.
    #[test]
    fn digits_attach_to_adjacent_letters_unless_followed_by_uppercase() {
        // Digit stays attached: no following uppercase letter.
        assert_eq!(tokenize_all_case_split("span2"), vec!["span2"]);
        assert_eq!(tokenize_all_case_split("utf8"), vec!["utf8"]);
        assert_eq!(tokenize_all_case_split("id123"), vec!["id123"]);
        // Digit run still ends a token when a new camelCase word follows.
        assert_eq!(tokenize_all_case_split("v2Client"), vec!["v2", "client"]);
    }

    #[test]
    fn empty_and_all_separator_input_yields_no_tokens() {
        assert_eq!(tokenize_all_case_split(""), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("!!!---..."), Vec::<String>::new());
    }

    /// Unicode: non-ASCII code points act like punctuation — they end the
    /// current token and are never emitted — matching
    /// [`tokenize_whole_identifier`]'s behavior on the same input.
    #[test]
    fn non_ascii_input_splits_around_the_non_matching_code_point() {
        assert_eq!(tokenize_all_case_split("café"), vec!["caf"]);
        assert_eq!(tokenize_all_case_split("日本語"), Vec::<String>::new());
    }

    #[test]
    fn same_input_twice_yields_identical_output() {
        let input = "getHTTPResponseCode_v2.JSON";
        assert_eq!(
            tokenize_all_case_split(input),
            tokenize_all_case_split(input)
        );
    }

    // -- Adversarial acronym-boundary probing (SDET-added, M1.P2.T2 verification) --

    /// A bare acronym with no trailing lowercase word must not spuriously
    /// split into single letters — the acronym-boundary rule only fires when
    /// an uppercase run is followed by a *new capitalized word*
    /// (upper->upper->lower), never at the run's own tail.
    #[test]
    fn bare_acronym_alone_does_not_split() {
        assert_eq!(tokenize_all_case_split("HTTP"), vec!["http"]);
        assert_eq!(tokenize_all_case_split("ID"), vec!["id"]);
        assert_eq!(tokenize_all_case_split("A"), vec!["a"]);
    }

    /// A single leading capital followed by a capitalized word is the
    /// degenerate (length-1 acronym) case of the acronym boundary: it must
    /// still split, treating the lone capital as its own token.
    #[test]
    fn single_leading_capital_then_word_splits_as_its_own_token() {
        assert_eq!(tokenize_all_case_split("AProtocol"), vec!["a", "protocol"]);
    }

    /// Consecutive single-capital runs: the acronym boundary must fire at
    /// exactly the right offset — one char before the new word — without
    /// dropping or duplicating the pivot character (`D` here belongs to
    /// "Def", not "Abc").
    #[test]
    fn consecutive_capitals_then_lowercase_splits_before_final_capital() {
        assert_eq!(tokenize_all_case_split("ABCDef"), vec!["abc", "def"]);
    }

    /// Chained acronym boundaries: multiple acronym->word transitions in one
    /// identifier must each split correctly and independently.
    #[test]
    fn chained_acronym_boundaries_all_split() {
        assert_eq!(
            tokenize_all_case_split("parseHTTPResponse"),
            vec!["parse", "http", "response"]
        );
        assert_eq!(
            tokenize_all_case_split("XMLHTTPRequestID"),
            vec!["xmlhttp", "request", "id"]
        );
    }

    /// Leading/trailing/doubled separators must never produce an empty
    /// token and must never panic.
    #[test]
    fn leading_trailing_and_doubled_separators_yield_no_empty_tokens() {
        assert_eq!(tokenize_all_case_split("_dd__trace_"), vec!["dd", "trace"]);
        assert_eq!(tokenize_all_case_split("--a-b--"), vec!["a", "b"]);
        assert_eq!(tokenize_all_case_split("  x  "), vec!["x"]);
    }

    /// Single-character and degenerate inputs must not panic and must
    /// produce exactly the expected 0-or-1 token.
    #[test]
    fn single_char_and_degenerate_inputs() {
        assert_eq!(tokenize_all_case_split("a"), vec!["a"]);
        assert_eq!(tokenize_all_case_split("A"), vec!["a"]);
        assert_eq!(tokenize_all_case_split("1"), vec!["1"]);
        assert_eq!(tokenize_all_case_split("_"), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split(""), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("!!!"), Vec::<String>::new());
    }

    /// Real-world mixed identifiers spanning multiple conventions in one
    /// string, beyond the single-convention examples already covered.
    #[test]
    fn mixed_real_world_identifiers() {
        assert_eq!(
            tokenize_all_case_split("getHTTPResponseCode"),
            vec!["get", "http", "response", "code"]
        );
        assert_eq!(
            tokenize_all_case_split("dd_trace.SpanContext"),
            vec!["dd", "trace", "span", "context"]
        );
        assert_eq!(
            tokenize_all_case_split("SCREAMING_SNAKE_CASE"),
            vec!["screaming", "snake", "case"]
        );
        assert_eq!(
            tokenize_all_case_split("kebab-case-thing"),
            vec!["kebab", "case", "thing"]
        );
        assert_eq!(
            tokenize_all_case_split("PascalCaseWord"),
            vec!["pascal", "case", "word"]
        );
        assert_eq!(
            tokenize_all_case_split("snake_case_word"),
            vec!["snake", "case", "word"]
        );
    }

    /// Char-conservation invariant: concatenating the output tokens must
    /// equal the input with every non-ASCII-alphanumeric char removed and
    /// every ASCII letter lowercased — no char may be dropped or duplicated
    /// at a split boundary. Reference computed independently of
    /// `tokenize_all_case_split` (filter + lowercase), not by re-deriving
    /// the same char-scan logic, so this actually checks the boundary
    /// bookkeeping rather than restating it.
    #[test]
    fn char_conservation_invariant_holds_across_inputs() {
        let inputs = [
            "ddTrace",
            "dd_trace",
            "DD_TRACE",
            "DdTrace",
            "dd-trace",
            "HTTPServer",
            "parseHTTPResponse",
            "AProtocol",
            "ABCDef",
            "_dd__trace_",
            "--a-b--",
            "  x  ",
            "getHTTPResponseCode_v2.JSON",
            "café",
            "naïve_parser",
            "日本語",
            "v2Client",
            "span2",
            "utf8",
            "id123",
            "2fast",
            "HTTP2Server",
            "",
            "!!!---...",
        ];
        for input in inputs {
            let tokens = tokenize_all_case_split(input);
            let actual: String = tokens.concat();
            let expected: String = input
                .chars()
                .filter(char::is_ascii_alphanumeric)
                .map(|c| c.to_ascii_lowercase())
                .collect();
            assert_eq!(actual, expected, "char-conservation failed for {input:?}");
            // No empty tokens should ever be emitted.
            assert!(
                tokens.iter().all(|t| !t.is_empty()),
                "empty token emitted for {input:?}: {tokens:?}"
            );
        }
    }

    /// Digit-handling self-consistency probe (decided rule, doc
    /// comment on `tokenize_all_case_split`): a digit attaches to an
    /// adjacent letter run like a lowercase letter, EXCEPT a digit
    /// immediately followed by an uppercase letter still splits — verify
    /// this holds even in the edge sub-cases the doc doesn't spell out:
    /// a bare leading digit before an uppercase word, and a digit fused
    /// onto a preceding acronym run.
    #[test]
    fn digit_rule_self_consistency_edge_cases() {
        // Doc's stated baseline examples.
        assert_eq!(tokenize_all_case_split("span2"), vec!["span2"]);
        assert_eq!(tokenize_all_case_split("utf8"), vec!["utf8"]);
        assert_eq!(tokenize_all_case_split("id123"), vec!["id123"]);
        assert_eq!(tokenize_all_case_split("v2Client"), vec!["v2", "client"]);
        // Not in the doc, but implied by "digit->upper always splits": a
        // digit with nothing before it still splits off as its own
        // single-char token when an uppercase word follows.
        assert_eq!(tokenize_all_case_split("2Client"), vec!["2", "client"]);
        // Not in the doc: digit followed by lowercase never splits.
        assert_eq!(tokenize_all_case_split("2fast"), vec!["2fast"]);
        // Not in the doc: a digit fused onto a preceding acronym run
        // attaches to the acronym (no digit->upper transition at the
        // acronym's own boundary), then still splits before the next
        // camelCase word.
        assert_eq!(
            tokenize_all_case_split("HTTP2Server"),
            vec!["http2", "server"]
        );
    }

    /// Unicode consistency: `tokenize_all_case_split` must agree with
    /// `tokenize_whole_identifier` on the ASCII-alphanumeric subset of any
    /// input containing non-ASCII code points — both drop non-ASCII the
    /// same way, differing only in where they insert *additional* splits
    /// for case/underscore boundaries.
    #[test]
    fn unicode_handling_agrees_with_whole_identifier_on_ascii_subset() {
        assert_eq!(tokenize_all_case_split("café"), vec!["caf"]);
        assert_eq!(tokenize_whole_identifier("café"), vec!["caf"]);

        assert_eq!(
            tokenize_all_case_split("naïve_parser"),
            vec!["na", "ve", "parser"]
        );
        // whole_identifier keeps `_` inside the token, all_case_split
        // splits on it — both agree on dropping `ï` and on the surviving
        // ASCII letters, differing only in the underscore boundary.
        assert_eq!(
            tokenize_whole_identifier("naïve_parser"),
            vec!["na", "ve_parser"]
        );

        assert_eq!(tokenize_all_case_split("日本語"), Vec::<String>::new());
        assert_eq!(tokenize_whole_identifier("日本語"), Vec::<String>::new());
    }

    /// Determinism: repeated calls on the same input yield identical
    /// output, and output order is strictly left-to-right (verified by
    /// checking tokens appear in the same order as their first-char
    /// position in the source string).
    #[test]
    fn deterministic_and_strictly_left_to_right() {
        let input = "getHTTPResponseCode_v2.JSON";
        let first = tokenize_all_case_split(input);
        let second = tokenize_all_case_split(input);
        assert_eq!(first, second);

        // Left-to-right: each token's first occurrence position in the
        // lowercased+separator-stripped source must be non-decreasing.
        let stripped: String = input
            .chars()
            .filter(char::is_ascii_alphanumeric)
            .map(|c| c.to_ascii_lowercase())
            .collect();
        let mut cursor = 0;
        for token in &first {
            let pos = stripped[cursor..]
                .find(token.as_str())
                .expect("token must appear in stripped source at or after cursor");
            cursor += pos + token.len();
        }
    }

    /// Signature parity (M1.P2.T3 precondition): both tokenizer modes must
    /// share the exact same `fn(&str) -> Vec<String>` shape so a caller can
    /// hold either behind one function pointer type without wrapping.
    #[test]
    fn signature_parity_between_tokenizer_modes() {
        let a: fn(&str) -> Vec<String> = tokenize_all_case_split;
        let b: fn(&str) -> Vec<String> = tokenize_whole_identifier;
        assert_eq!(a("ddTrace"), vec!["dd", "trace"]);
        assert_eq!(b("dd_trace"), vec!["dd_trace"]);
    }
}
