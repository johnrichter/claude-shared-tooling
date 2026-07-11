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
//!
//! # M1.P3.T2 — drop-pure-digit + Unicode-aware, emoji-as-boundary
//! Two operator decisions land in both tokenizers below:
//! - **(E) drop pure-digit tokens.** A token made entirely of digits carries
//!   no lexical/symbol value, so it's dropped after splitting — `12345` →
//!   `[]`, `2Client` → `["client"]` (the bare `2` is dropped, `client`
//!   survives). Any token with ≥1 letter is kept even if it also has digits
//!   (`span2`, `utf8`, `v2` all survive).
//! - **(F) Unicode-aware, emoji/symbols/punctuation as boundaries.** Token
//!   content is now Unicode alphanumerics (letters, digits, and combining
//!   marks) via `classify_char`, not an ASCII-only character class.
//!   Lowercasing is Unicode case-folding (`char::to_lowercase`), so `José` →
//!   `["josé"]` (previously dropped to `["jos"]`). An emoji, symbol, or any
//!   other non-alphanumeric code point is a boundary — it ends the current
//!   token and is never itself emitted, exactly like ASCII punctuation.
//!   `classify_char` is the shared seam BOTH tokenizers consume for this —
//!   see its doc comment for the future emoji→name extension point (option
//!   (c), not implemented here).
//!
//! ## Known Unicode limitations (both tokenizers, accepted for v1)
//! - **Normalization-sensitive.** Input is NOT Unicode-normalized, so NFC
//!   (precomposed `é`) and NFD (`e` + combining acute) forms of the same
//!   visual string tokenize to DIFFERENT tokens — content and query must use
//!   the same normalization form to match. Normalize to one form (e.g. NFC)
//!   on both paths before calling.
//! - **Combining-mark coverage is best-effort.** `is_combining_mark`
//!   (module-private) is a fixed 5-range table, not the full Unicode Mark
//!   category; a mark outside those ranges (and not already
//!   `char::is_alphanumeric`) is treated as a boundary — it splits its base
//!   letter's token and is dropped.
//!
//!   Fuller fix (assessed, NOT implemented — operator decision): adding the
//!   `unicode-normalization` crate and NFC-normalizing input at the tokenizer
//!   entry would make NFC/NFD converge (closing the first limitation) and
//!   precompose most Latin/Greek/Cyrillic marks into single alphanumerics
//!   (shrinking reliance on the table). Marks with no precomposed form (many
//!   Indic/Arabic/Hebrew marks) would still need explicit Mark-category
//!   classification. Not added here to avoid a new dependency without sign-off.

/// Per-char classification shared by both tokenizers below — the seam a
/// future emoji→name mode (decided-against-for-now option (c)) slots into
/// without touching either tokenizer's scan loop.
///
/// Both tokenizers agree on which chars are `CharClass::AlphanumericContent`
/// vs `CharClass::Boundary` (Unicode alphanumerics + combining marks are
/// content; everything else — whitespace, ASCII punctuation, symbols, emoji —
/// is a boundary). They deliberately do NOT agree on `CharClass::Connector`
/// (`_`): [`tokenize_whole_identifier`] folds it into the token as content
/// (keeps `dd_trace` whole); [`tokenize_all_case_split`] treats it as a
/// boundary (the `snake_case` separator). That distinction is each tokenizer's
/// own call, not `classify_char`'s — the enum exposes the `_` case as its own
/// variant precisely so callers can each decide, rather than baking one
/// answer into the shared classifier.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CharClass {
    /// A Unicode letter, digit, or combining mark — token content in both
    /// tokenizer modes.
    AlphanumericContent,
    /// `_` — content in [`tokenize_whole_identifier`], a boundary in
    /// [`tokenize_all_case_split`]. See the enum's own doc comment.
    Connector,
    /// Whitespace, ASCII punctuation, symbols, or emoji. A word boundary:
    /// ends the current token (if any) and is never itself emitted.
    ///
    /// future: emoji→name (option c) — a later mode could special-case an
    /// emoji char here and map it to its Unicode name (e.g. `📈` →
    /// `"chart-increasing"`) instead of `Boundary`, emitting a name token
    /// instead of dropping it. Not implemented in this task: decision (a)
    /// (emoji-as-boundary) is what ships. Adding option (c) means adding a
    /// variant here (or a classification mode parameter) and handling it in
    /// each tokenizer's match arm — no rewrite of the scan loops themselves.
    Boundary,
}

/// Classifies one char per `CharClass`'s rules. Built only from
/// `char::is_alphanumeric` (Unicode-table-based, not locale-based) and a
/// fixed table of combining-mark code-point ranges — so classification is
/// identical on every platform, thread, and run; no locale, timezone, or
/// environment dependency.
fn classify_char(c: char) -> CharClass {
    if c == '_' {
        CharClass::Connector
    } else if c.is_alphanumeric() || is_combining_mark(c) {
        CharClass::AlphanumericContent
    } else {
        CharClass::Boundary
    }
}

/// Combining marks (Unicode general category `Mn`/`Mc`/`Me`) attach to the
/// base letter they modify, so decision (F) counts them as token content —
/// e.g. `e` + U+0301 (combining acute accent) is one accented letter, not a
/// letter followed by a boundary. `char::is_alphabetic`/`is_alphanumeric`
/// do NOT cover most of these: the Unicode `Alphabetic` derived property is
/// `No` for the common combining-diacritic blocks (they modify an alphabetic
/// base char but aren't themselves alphabetic under that property).
///
/// Rather than pull in a Unicode-general-category crate for this one
/// property, this checks a fixed table of five code-point ranges (below).
/// Coverage is BEST-EFFORT, NOT the full Unicode Mark (`Mn`/`Mc`/`Me`)
/// category: the table holds the general combining-diacritic blocks
/// (decomposed Latin/Greek/Cyrillic accents, combining marks for symbols,
/// half marks) that cover the common accented-Latin-script case. A combining
/// mark OUTSIDE these ranges that is ALSO not `char::is_alphanumeric` —
/// e.g. Devanagari stress sign UDATTA (U+0951), Ethiopic combining
/// gemination (U+135D), and other script-specific marks — falls through as a
/// `CharClass::Boundary`: it splits its base letter's token in two and is
/// itself dropped from the output (it does not stay attached as an accent).
/// Many script marks are still handled correctly, but by `char::is_alphanumeric`
/// returning true via Unicode's `Other_Alphabetic` property, NOT by this
/// table. Fixed ranges, not a derived lookup that could grow with a Unicode
/// version bump — deterministic and dependency-free. See the module-level
/// note on the `unicode-normalization` option for the fuller fix.
fn is_combining_mark(c: char) -> bool {
    matches!(c as u32,
        0x0300..=0x036F   // Combining Diacritical Marks
        | 0x1AB0..=0x1AFF // Combining Diacritical Marks Extended
        | 0x1DC0..=0x1DFF // Combining Diacritical Marks Supplement
        | 0x20D0..=0x20FF // Combining Diacritical Marks for Symbols
        | 0xFE20..=0xFE2F // Combining Half Marks
    )
}

/// Decision (E): a token made ENTIRELY of Unicode digits (`char::is_numeric`)
/// carries no lexical/symbol value for identifier or lexical search, so it's
/// dropped post-split. A token with ≥1 letter or combining mark survives
/// even when mixed with digits (`span2`, `utf8`, `v2`) — only the
/// all-digit case is dropped (`12345`, or the lone `2` split off the front
/// of `2Client`).
fn is_pure_digit_token(token: &str) -> bool {
    token.chars().all(char::is_numeric)
}

/// Whole-identifier tokenizer (the seed mode): splits `text` on
/// `CharClass::Boundary` chars, keeping each surviving run of
/// `CharClass::AlphanumericContent`-and-`CharClass::Connector` chars as
/// one token, Unicode-case-folded to lowercase. Identifiers with underscores
/// stay intact — `dd_trace` is one token, not two — because `_` is a
/// `CharClass::Connector`, which this tokenizer folds into the token same
/// as content.
///
/// # Unicode (M1.P3.T2 — decided)
/// Token content is Unicode alphanumeric (`char::is_alphanumeric`, covering
/// every script's letters and digits) plus combining marks — not an
/// ASCII-only character class. Lowercasing is Unicode case-folding
/// (`str::to_lowercase`, which is 1-input-char-to-N-output-char safe, e.g.
/// `İ` → `i̇`). So `José` → `["josé"]` (previously ASCII-only behavior
/// dropped `é`, yielding `["jos"]`) and Greek/Cyrillic identifiers lowercase
/// and tokenize the same as Latin ones. An emoji, symbol, or any other
/// non-alphanumeric code point is a `CharClass::Boundary` — exactly like
/// ASCII punctuation, it ends the current token and is never itself
/// emitted: `hello📈world` → `["hello", "world"]`, a standalone emoji →
/// `[]`.
///
/// Known v1 limitation (accepted, not solved here): caseless, space-less
/// scripts (CJK, Thai, and similar) have no punctuation/whitespace word
/// boundaries and no case to detect — see [`tokenize_all_case_split`] for
/// where that matters — but even here, a contiguous run of such a script's
/// characters becomes one long token with no internal segmentation, since
/// there's no boundary signal at all in this mode either. Proper
/// segmentation of those scripts needs a dictionary or ML model; out of
/// scope for this crate.
///
/// Known v1 limitation (Unicode normalization — accepted, not solved here):
/// input is NOT Unicode-normalized before tokenizing, so NFC (precomposed
/// `é`, one code point) and NFD (`e` + combining acute, two code points)
/// forms of the same visual string produce DIFFERENT tokens — content
/// indexed in one form will not match a query typed in the other. Callers
/// indexing accented content should normalize to one form (e.g. NFC) on both
/// the index and query paths before calling.
///
/// Known v1 limitation (combining-mark coverage — accepted, not solved
/// here): combining marks are recognized best-effort over the ranges in
/// `is_combining_mark` (module-private), NOT the full Unicode Mark category.
/// A mark outside those ranges (e.g. Devanagari, Ethiopic, and other
/// script-specific marks not also covered by `char::is_alphanumeric`) is
/// treated as a boundary — it splits its base letter's token and is dropped
/// from output. See that fn's doc for exactly what is / is not covered.
///
/// # Digit handling (M1.P3.T2 — decided, decision E)
/// A token made entirely of digits is dropped post-split — `12345` → `[]`.
/// Digits mixed with letters are unaffected — `v2_client` → `["v2_client"]`.
///
/// Deterministic and pure: same `text` always yields the same `Vec<String>`,
/// in left-to-right order, with no locale-, thread-, or platform-dependent
/// behavior — `char::is_alphanumeric`/`to_lowercase` and this module's
/// combining-mark table are all Unicode-table-based, not locale-sensitive.
///
/// Returns an empty vector for empty, all-boundary, or all-digit input —
/// never panics.
#[must_use]
pub fn tokenize_whole_identifier(text: &str) -> Vec<String> {
    let mut tokens = Vec::new();
    let mut current = String::new();

    for c in text.chars() {
        match classify_char(c) {
            CharClass::AlphanumericContent | CharClass::Connector => {
                for lc in c.to_lowercase() {
                    current.push(lc);
                }
            }
            CharClass::Boundary => {
                if !current.is_empty() {
                    tokens.push(std::mem::take(&mut current));
                }
            }
        }
    }
    if !current.is_empty() {
        tokens.push(current);
    }

    tokens.retain(|t| !is_pure_digit_token(t));
    tokens
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
/// automaton engine doesn't support). Content vs boundary is decided by the
/// shared `classify_char` seam (see M1.P3.T2 section below); `_` classifies
/// as the connector class but THIS tokenizer treats it as a boundary
/// (`snake_case` separator) — the one place it disagrees with
/// [`tokenize_whole_identifier`]. A new token starts at index `i` when:
/// - The char at `i` is a boundary or connector (`_`) char
///   — never emitted, just ends the current token, e.g. `dd_trace` and
///   `dd-trace` and `dd.trace` all yield `["dd", "trace"]`.
/// - Lower/digit → upper transition (camelCase boundary): `ddTrace` splits
///   between `d` and `T`.
/// - Upper → upper → lower transition (acronym boundary): a run of capitals
///   ends one char before a new capitalized word starts, so `HTTPServer`
///   splits between `P` and `S` → `http`, `server` (not `h`,`t`,`t`,
///   `pserver`), and `DDTrace`/`DD_TRACE`/`DdTrace` all agree with `ddTrace`.
///
/// Every emitted token is Unicode-lowercased char-by-char as it's built —
/// deterministic left-to-right, table-based, not locale-sensitive.
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
/// - `2Client` → `["client"]` — a **bare leading digit** (nothing before it)
///   followed by an uppercase word still splits off the digit as its own
///   token — but as of decision (E) that lone digit token is then dropped
///   as pure-digit, so only `client` survives.
/// - `2fast` → `["2fast"]` — digit→lowercase never splits.
/// - `HTTP2Server` → `["http2", "server"]` — a digit fused onto a preceding
///   acronym run attaches to the acronym (no digit→upper transition at the
///   acronym's own tail), then splits before the next camelCase word.
///
/// Rationale (why kept): bare numeric tokens (`2`) rarely help retrieval on
/// their own, so digits default to merging with their neighboring letters;
/// but digits must not swallow the next real word when a case transition is
/// present. Since M1.P3.T2, a lone digit token from `2Client`-shaped input is
/// no longer even emitted (decision (E) drops it), so the historical cost of
/// this rule (a stray `2` token) is gone. Query `trace` still matches every
/// casing of `dd_trace` because digits never alter the letter tokens.
///
/// # Digit-drop (decision E — M1.P3.T2)
/// After all splitting above, any token made entirely of digits is dropped —
/// `12345` → `[]`. See `is_pure_digit_token` (module-private).
///
/// # Unicode (decision F — M1.P3.T2, supersedes the earlier drop-non-ASCII
/// rule)
/// Token content is Unicode alphanumeric + combining marks (via the shared
/// `classify_char` seam), not ASCII-only, and case-boundary detection uses
/// `char::is_uppercase`/`is_lowercase` (Unicode-table-based) instead of the
/// ASCII-only predicates. Concretely:
/// - `José` → `["josé"]` (previously `["jos"]` under the old drop-non-ASCII
///   rule) — accented letters are content, and `é` here is lowercase already
///   so no boundary/case-split logic changes anything about it.
/// - Cased, non-Latin scripts (Greek, Cyrillic, and similar) split on case
///   boundaries the same way ASCII does, since `is_uppercase`/`is_lowercase`
///   are true for their cased letters too.
/// - `is_uppercase`/`is_lowercase` are FALSE for caseless scripts (CJK,
///   Thai, Arabic, Hebrew, and similar) — correct, since those scripts have
///   no case to split on. See the CJK limitation below.
/// - An emoji, symbol, or any other non-alphanumeric code point is a
///   boundary — `hello📈world` → `["hello", "world"]`; a standalone emoji
///   → `[]`. See `classify_char`'s doc comment (module-private) for the
///   future emoji→name extension seam (option c, not implemented here).
///
/// Known v1 limitation (accepted, documented, not solved here): a caseless,
/// space-less script (CJK, Thai, and similar) has no word boundaries (no
/// whitespace/punctuation between characters in normal text) and no case to
/// detect — so a contiguous run of such a script's characters becomes ONE
/// token with no internal segmentation, e.g. `日本語` → `["日本語"]`. Proper
/// segmentation of these scripts needs a dictionary or ML model (e.g.
/// morphological analysis for CJK word boundaries); that's out of scope for
/// this crate's tokenizers, which are identifier/ASCII-word-boundary-shaped.
/// This is a recall gap for CJK/Thai-heavy content, not a correctness bug —
/// no character is lost or duplicated, the run is just under-segmented.
///
/// Known v1 limitations (accepted, not solved here) — the SAME two as
/// [`tokenize_whole_identifier`] apply verbatim to this tokenizer:
/// - **Unicode normalization**: input is not normalized, so NFC and NFD
///   forms of the same visual string tokenize to different tokens; normalize
///   to one form on both index and query paths before calling.
/// - **Combining-mark coverage**: marks are recognized best-effort over the
///   `is_combining_mark` ranges, not the full Unicode Mark category; a mark
///   outside them splits its base letter's token and is dropped.
///
/// Deterministic and pure: same `text` always yields the same `Vec<String>`
/// in left-to-right order; no I/O, clock, or locale dependency —
/// `char::is_alphanumeric`/`is_uppercase`/`is_lowercase`/`to_lowercase` and
/// this module's combining-mark table are all fixed Unicode-table lookups.
/// Returns an empty vector for empty, all-boundary, or all-digit input —
/// never panics.
///
/// # M1.P2.T3 (landed)
/// Same signature as [`tokenize_whole_identifier`] — `fn(&str) -> Vec<String>`
/// — by design: the call-time selection API now holds either tokenizer
/// behind [`Tokenizer`] (below), a closed enum injected into `okapi`/`bm25f`
/// at construction and reused at search, without either scorer knowing which
/// mode it got.
#[must_use]
pub fn tokenize_all_case_split(text: &str) -> Vec<String> {
    let chars: Vec<char> = text.chars().collect();
    let mut tokens = Vec::new();
    let mut current = String::new();

    for i in 0..chars.len() {
        let c = chars[i];

        if classify_char(c) != CharClass::AlphanumericContent {
            if !current.is_empty() {
                tokens.push(std::mem::take(&mut current));
            }
            continue;
        }

        if !current.is_empty() {
            // Safe: `current` non-empty means the previous char was content
            // too (any boundary/connector char would have flushed and reset
            // it above), so `chars[i - 1]` is the char last pushed.
            let prev = chars[i - 1];
            let acronym_boundary = prev.is_uppercase()
                && matches!(chars.get(i + 1), Some(&next) if next.is_lowercase());
            let camel_boundary = prev.is_lowercase() || prev.is_numeric() || acronym_boundary;
            if c.is_uppercase() && camel_boundary {
                tokens.push(std::mem::take(&mut current));
            }
        }

        for lc in c.to_lowercase() {
            current.push(lc);
        }
    }

    if !current.is_empty() {
        tokens.push(current);
    }

    tokens.retain(|t| !is_pure_digit_token(t));
    tokens
}

/// This crate's call-time-selectable tokenizer choice.
///
/// A closed, two-variant enum rather than a bare `fn(&str) -> Vec<String>`
/// pointer or a caller-supplied closure — deliberately, for two reasons:
/// 1. **AI/human legibility.** [`crate::okapi::OkapiIndex::build`] and
///    [`crate::bm25f::BM25FIndex::build`] take a `Tokenizer` argument whose
///    two named variants are documented right here; a caller (or an LLM
///    generating a call site) reads `Tokenizer::CaseSplit` and knows exactly
///    what it selects, vs. an opaque fn pointer that could be anything.
/// 2. **Closed set.** This crate owns tokenization (see the crate-level
///    docs) — a caller-supplied custom tokenizer would break the "query and
///    document always tokenize identically" contract this crate exists to
///    guarantee, since a custom fn could differ between build and search
///    calls with no compile-time signal. An enum makes "exactly these two
///    modes, nothing else" a type-level fact.
///
/// [`Default`] is [`Tokenizer::CaseSplit`] — navigator's default mode (see
/// the design doc, "Shared Rust library" §(a)); [`Tokenizer::WholeIdentifier`]
/// is the exact-symbol opt-in.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Tokenizer {
    /// All-case-splitting mode ([`tokenize_all_case_split`]) — navigator's
    /// default. Splits `camelCase`/`snake_case`/`PascalCase`/
    /// `SCREAMING_SNAKE`/kebab-case identifiers into sub-tokens.
    #[default]
    CaseSplit,
    /// Whole-identifier mode ([`tokenize_whole_identifier`]) — exact-symbol
    /// opt-in. Keeps an underscored/cased identifier as one token.
    WholeIdentifier,
}

impl Tokenizer {
    /// Tokenizes `text` with this variant's underlying tokenizer function.
    /// Pure and deterministic — see the two underlying fns' own docs for the
    /// per-mode splitting rules.
    #[must_use]
    pub fn tokenize(self, text: &str) -> Vec<String> {
        match self {
            Tokenizer::CaseSplit => tokenize_all_case_split(text),
            Tokenizer::WholeIdentifier => tokenize_whole_identifier(text),
        }
    }
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
    /// when mixed with letters/underscores — matches ka's `[a-z0-9_]+`
    /// intent on the identifier case; a bare all-digit token is dropped
    /// per decision (E).
    #[test]
    fn keeps_digits_in_and_out_of_identifiers() {
        assert_eq!(tokenize_whole_identifier("v2_client"), vec!["v2_client"]);
        assert_eq!(tokenize_whole_identifier("12345"), Vec::<String>::new());
        assert_eq!(
            tokenize_whole_identifier("span_id123 456"),
            vec!["span_id123"]
        );
    }

    /// Decision (E): a token made entirely of digits is dropped; a token
    /// with any letter (even fused to a leading digit) survives.
    #[test]
    fn drops_pure_digit_tokens() {
        // No case-split in this mode: `2Client` never splits into a bare
        // digit -- it's one token, "2client", which has letters and so is
        // kept whole (unlike `tokenize_all_case_split`'s "2Client").
        assert_eq!(tokenize_whole_identifier("2Client"), vec!["2client"]);
        assert_eq!(tokenize_whole_identifier("span2"), vec!["span2"]);
        assert_eq!(tokenize_whole_identifier("utf8"), vec!["utf8"]);
        assert_eq!(tokenize_whole_identifier("v2Client"), vec!["v2client"]);
    }

    /// Unicode input: `to_lowercase()` is Unicode-aware and this tokenizer's
    /// content class is Unicode-alphanumeric, so accented letters survive
    /// intact — `café` and `naïve_parser` no longer lose their diacritics.
    #[test]
    fn unicode_input_keeps_accented_letters() {
        assert_eq!(tokenize_whole_identifier("café"), vec!["café"]);
        assert_eq!(
            tokenize_whole_identifier("naïve_parser"),
            vec!["naïve_parser"]
        );
        assert_eq!(tokenize_whole_identifier("José"), vec!["josé"]);
    }

    /// CJK limitation (documented, accepted): no boundary and no case, so a
    /// contiguous run is one token, not `[]` and not per-character.
    #[test]
    fn cjk_run_is_a_single_token() {
        assert_eq!(tokenize_whole_identifier("日本語"), vec!["日本語"]);
    }

    /// Emoji is a boundary: ends the current token, never emitted itself.
    #[test]
    fn emoji_is_a_boundary_not_a_token() {
        assert_eq!(
            tokenize_whole_identifier("hello📈world"),
            vec!["hello", "world"]
        );
        assert_eq!(tokenize_whole_identifier("📈"), Vec::<String>::new());
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

    /// Decision (E): a pure-digit token is dropped after splitting.
    #[test]
    fn drops_pure_digit_tokens() {
        assert_eq!(tokenize_all_case_split("12345"), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("2Client"), vec!["client"]);
        assert_eq!(tokenize_all_case_split("span2"), vec!["span2"]);
        assert_eq!(tokenize_all_case_split("utf8"), vec!["utf8"]);
        assert_eq!(tokenize_all_case_split("v2Client"), vec!["v2", "client"]);
    }

    /// Unicode: accented letters are content and Unicode-lowercased, not
    /// dropped — `café` → `["café"]` (previously `["caf"]`). `José` is the
    /// task's required example: a lone leading capital with no case split
    /// inside the accented run.
    #[test]
    fn unicode_input_keeps_accented_letters() {
        assert_eq!(tokenize_all_case_split("café"), vec!["café"]);
        assert_eq!(tokenize_all_case_split("José"), vec!["josé"]);
    }

    /// Accented camelCase: the case-split boundary logic fires on Unicode
    /// `is_uppercase`/`is_lowercase`, so an accented letter inside a
    /// camelCase run does not block a legitimate split, and does not itself
    /// spuriously split.
    #[test]
    fn accented_camel_case_splits_correctly() {
        assert_eq!(
            tokenize_all_case_split("getCaféName"),
            vec!["get", "café", "name"]
        );
    }

    /// Greek and Cyrillic are cased scripts: `is_uppercase`/`is_lowercase`
    /// are true for their letters too, so camelCase-style splitting applies
    /// the same as it does to Latin identifiers.
    #[test]
    fn cased_non_latin_scripts_split_on_case_boundaries() {
        // Greek: Καλημέρα -> Καλη + μέρα (Κ is upper, α is lower, μ starts
        // a new capitalized-looking run only if it's uppercase; here we
        // build an explicit camelCase-shaped Greek example instead).
        assert_eq!(tokenize_all_case_split("καληΜέρα"), vec!["καλη", "μέρα"]);
        // Cyrillic: миррТест -> мирр + Тест boundary.
        assert_eq!(tokenize_all_case_split("миррТест"), vec!["мирр", "тест"]);
    }

    /// CJK limitation (documented, accepted): caseless + space-less, so a
    /// contiguous run is one token with no internal segmentation.
    #[test]
    fn cjk_run_is_a_single_token() {
        assert_eq!(tokenize_all_case_split("日本語"), vec!["日本語"]);
    }

    /// Combining marks: a decomposed accented letter (base char + combining
    /// diacritic as two separate code points) tokenizes as one token, not
    /// split at the combining mark.
    #[test]
    fn combining_mark_stays_attached_to_its_base_letter() {
        let decomposed = "e\u{0301}cole"; // "e" + combining acute accent + "cole"
                                          // Already all-lowercase input, no case boundary in it -- one token,
                                          // the combining mark preserved (not dropped as a boundary) and
                                          // still attached to the "e" it modifies.
        assert_eq!(tokenize_all_case_split(decomposed), vec![decomposed]);
    }

    /// Emoji is a boundary: ends the current token, never emitted itself.
    /// A standalone emoji yields no tokens; an emoji between separators is
    /// simply absorbed into the surrounding boundary run.
    #[test]
    fn emoji_is_a_boundary_not_a_token() {
        assert_eq!(
            tokenize_all_case_split("hello📈world"),
            vec!["hello", "world"]
        );
        assert_eq!(tokenize_all_case_split("📈"), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("a, 📈, b"), vec!["a", "b"]);
    }

    /// Parity: both tokenizers agree on content-vs-boundary classification
    /// for a shared Unicode input containing letters, digits, an
    /// underscore, and an emoji — they differ ONLY in what they do with the
    /// underscore (content for whole-identifier, boundary for case-split).
    #[test]
    fn tokenizers_agree_on_content_vs_boundary_except_underscore() {
        let input = "café_HTTPServer2📈日本語";
        // Both drop the emoji as a boundary and keep café/HTTPServer2/日本語
        // as content runs; case-split additionally splits on `_` and case.
        assert_eq!(
            tokenize_whole_identifier(input),
            vec!["café_httpserver2", "日本語"]
        );
        assert_eq!(
            tokenize_all_case_split(input),
            vec!["café", "http", "server2", "日本語"]
        );
    }

    #[test]
    fn same_input_twice_yields_identical_output() {
        let input = "getHTTPResponseCode_v2.JSON";
        assert_eq!(
            tokenize_all_case_split(input),
            tokenize_all_case_split(input)
        );
    }

    /// Determinism holds for Unicode/emoji input too, run twice.
    #[test]
    fn unicode_and_emoji_input_deterministic_across_runs() {
        let input = "José📈getCaféHTTPServer_日本語2Client";
        assert_eq!(
            tokenize_all_case_split(input),
            tokenize_all_case_split(input)
        );
        assert_eq!(
            tokenize_whole_identifier(input),
            tokenize_whole_identifier(input)
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
    /// produce exactly the expected 0-or-1 token (`"1"` is now dropped: a
    /// single digit is a pure-digit token under decision (E)).
    #[test]
    fn single_char_and_degenerate_inputs() {
        assert_eq!(tokenize_all_case_split("a"), vec!["a"]);
        assert_eq!(tokenize_all_case_split("A"), vec!["a"]);
        assert_eq!(tokenize_all_case_split("1"), Vec::<String>::new());
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
    /// equal the input with every non-content char removed and every letter
    /// lowercased (Unicode-aware), MINUS any pure-digit tokens dropped by
    /// decision (E) — so this reference independently recomputes the
    /// expected surviving chars, filters would-be-pure-digit runs the same
    /// way `tokenize_all_case_split` does, and compares.
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
                .filter(|c| classify_char(*c) == CharClass::AlphanumericContent)
                .flat_map(char::to_lowercase)
                .collect();
            // Reference must also drop what would be pure-digit tokens, to
            // stay a valid comparison post decision (E). None of these
            // fixture inputs contain a run that is entirely digits once
            // split, so no adjustment is needed here — verified by
            // `drops_pure_digit_tokens` separately.
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
    /// a bare leading digit before an uppercase word (now dropped per
    /// decision E), and a digit fused onto a preceding acronym run.
    #[test]
    fn digit_rule_self_consistency_edge_cases() {
        // Doc's stated baseline examples.
        assert_eq!(tokenize_all_case_split("span2"), vec!["span2"]);
        assert_eq!(tokenize_all_case_split("utf8"), vec!["utf8"]);
        assert_eq!(tokenize_all_case_split("id123"), vec!["id123"]);
        assert_eq!(tokenize_all_case_split("v2Client"), vec!["v2", "client"]);
        // A digit with nothing before it still splits off as its own
        // single-char token when an uppercase word follows -- but that lone
        // digit token is then dropped as pure-digit (decision E).
        assert_eq!(tokenize_all_case_split("2Client"), vec!["client"]);
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
    /// `tokenize_whole_identifier` on which chars are content across an
    /// input containing non-ASCII code points — both now KEEP accented
    /// letters (Unicode-aware since M1.P3.T2), differing only in where they
    /// insert additional splits for case/underscore boundaries.
    #[test]
    fn unicode_handling_agrees_with_whole_identifier_on_content() {
        assert_eq!(tokenize_all_case_split("café"), vec!["café"]);
        assert_eq!(tokenize_whole_identifier("café"), vec!["café"]);

        assert_eq!(
            tokenize_all_case_split("naïve_parser"),
            vec!["naïve", "parser"]
        );
        // whole_identifier keeps `_` inside the token, all_case_split
        // splits on it -- both agree on keeping `ï` and on the surviving
        // letters, differing only in the underscore boundary.
        assert_eq!(
            tokenize_whole_identifier("naïve_parser"),
            vec!["naïve_parser"]
        );

        assert_eq!(tokenize_all_case_split("日本語"), vec!["日本語"]);
        assert_eq!(tokenize_whole_identifier("日本語"), vec!["日本語"]);
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

#[cfg(test)]
mod tokenizer_enum_tests {
    use super::*;

    /// Each variant dispatches to its matching free fn — not a stand-in that
    /// happens to look right; direct equality against the fn call itself.
    #[test]
    fn each_variant_dispatches_to_its_matching_free_fn() {
        assert_eq!(
            Tokenizer::CaseSplit.tokenize("ddTrace"),
            tokenize_all_case_split("ddTrace")
        );
        assert_eq!(
            Tokenizer::WholeIdentifier.tokenize("dd_trace"),
            tokenize_whole_identifier("dd_trace")
        );
    }

    /// Navigator's default is case-splitting, per the design doc.
    #[test]
    fn default_is_case_split() {
        assert_eq!(Tokenizer::default(), Tokenizer::CaseSplit);
    }

    /// The two variants disagree on at least one input (otherwise
    /// "selectable" would be a no-op) — `ddTrace` splits under `CaseSplit`
    /// but stays whole under `WholeIdentifier`.
    #[test]
    fn variants_produce_different_output_for_the_same_input() {
        assert_ne!(
            Tokenizer::CaseSplit.tokenize("ddTrace"),
            Tokenizer::WholeIdentifier.tokenize("ddTrace")
        );
    }
}

/// SDET adversarial probing for M1.P3.T2 — verification-only, not part of
/// the implementation contract. Targets the Unicode edges the task spec
/// flagged: combining-mark table completeness, NFC/NFD normalization,
/// compound emoji, non-Latin case scripts, and drop-pure-digit corner cases
/// beyond what the SE's own tests already cover.
#[cfg(test)]
mod sdet_adversarial_tests {
    use super::*;

    // -- Drop-pure-digit corner cases --

    #[test]
    fn connector_joined_digit_run_is_not_pure_digit_in_whole_identifier_mode() {
        // `123_456` has a `_` (Connector, not a digit), so
        // `is_pure_digit_token`'s `chars().all(char::is_numeric)` is false —
        // the token survives whole. This is a real gap: an all-digit
        // identifier connected by `_` slips through the "no lexical value"
        // filter the SE's own doc comment says only catches "entirely
        // digits" tokens. Whether that's a defect or an accepted edge case
        // is a judgment call for the QR — flagging, not failing on it,
        // since the SE's doc comment is explicit that `_`-joined digits are
        // an intentional non-match ("connector, not entirely digits").
        assert_eq!(tokenize_whole_identifier("123_456"), vec!["123_456"]);
    }

    #[test]
    fn single_and_zero_padded_digit_tokens_are_dropped() {
        assert_eq!(tokenize_whole_identifier("0"), Vec::<String>::new());
        assert_eq!(tokenize_whole_identifier("007"), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("0"), Vec::<String>::new());
        assert_eq!(tokenize_all_case_split("007"), Vec::<String>::new());
    }

    #[test]
    fn digit_plus_combining_mark_token_is_kept_not_pure_digit() {
        // A digit followed by a combining mark (e.g. a combining enclosing
        // circle U+20DD, in the SE's own table) is AlphanumericContent for
        // both chars, so it forms one token — `is_pure_digit_token` checks
        // `char::is_numeric`, which is false for the combining mark, so the
        // token is NOT dropped even though it "looks like" a digit run.
        let input = "5\u{20DD}"; // '5' + combining enclosing circle
        assert_eq!(tokenize_whole_identifier(input), vec![input]);
        assert_eq!(tokenize_all_case_split(input), vec![input]);
    }

    #[test]
    fn leading_and_trailing_digit_boundary_cases() {
        assert_eq!(tokenize_whole_identifier("v2"), vec!["v2"]);
        assert_eq!(tokenize_whole_identifier("2v"), vec!["2v"]);
        assert_eq!(tokenize_all_case_split("v2"), vec!["v2"]);
        assert_eq!(tokenize_all_case_split("2v"), vec!["2v"]);
    }

    // -- Combining-mark table completeness --

    /// The SE's 5-range table covers the common decomposed-Latin/Greek/
    /// Cyrillic diacritic blocks, but real Unicode combining marks (general
    /// category Mn/Mc/Me) extend far beyond those ranges. Devanagari,
    /// Arabic, and Hebrew vowel/diacritic marks are covered NOT by the
    /// table but by `char::is_alphabetic` already returning true for them
    /// (Unicode's `Other_Alphabetic` property) — so those scripts work by
    /// accident of `is_alphanumeric`, not because of the table. But marks
    /// that are Mn and NOT `Other_Alphabetic`, and outside the table's
    /// ranges, fall through as boundaries. Two concrete examples:
    /// Devanagari stress sign UDATTA (U+0951) and Ethiopic combining
    /// gemination mark (U+135D) — both `is_alphanumeric() == false` and
    /// outside all 5 ranges.
    #[test]
    fn marks_outside_the_five_ranges_and_outside_other_alphabetic_are_misclassified_as_boundary() {
        assert_eq!(classify_char('\u{0951}'), CharClass::Boundary); // Devanagari UDATTA
        assert_eq!(classify_char('\u{135D}'), CharClass::Boundary); // Ethiopic gemination
                                                                    // Net effect on a real tokenizer input: the mark splits its base
                                                                    // letter's token in two instead of staying attached — the exact
                                                                    // failure mode decision (F)'s doc comment says combining marks
                                                                    // should NOT trigger.
        let input = "अ\u{0951}आ"; // Devanagari अ + UDATTA + आ, all one visual unit run
                                  // If the table were complete this would be one token. Instead the
                                  // mark's Boundary classification acts exactly like a space or
                                  // punctuation char sitting between the two base letters: it
                                  // flushes the first letter as its own token and starts a new one —
                                  // fracturing what should be one token into two.
        assert_eq!(tokenize_whole_identifier(input), vec!["अ", "आ"]);
    }

    /// A demonstration where the gap DOES visibly fracture a token: a
    /// boundary-classified mark sitting between an accepted content char and
    /// a subsequent boundary means the mark itself is silently dropped from
    /// the token text (not just misclassified) — i.e. it disappears from
    /// output entirely, which is the sourced defect regardless of whether it
    /// also fractures which run it belongs to.
    #[test]
    fn out_of_table_mark_is_silently_dropped_from_token_text() {
        let input = "test\u{135D}"; // "test" + Ethiopic gemination mark, no trailing content
                                    // The mark is classified Boundary, so it's dropped and never
                                    // emitted -- the mark itself vanishes from the token entirely
                                    // (not preserved as an accent on "test").
        assert_eq!(tokenize_whole_identifier(input), vec!["test"]);
    }

    /// In-table marks (the SE's claimed common case) DO stay attached, for
    /// contrast with the two failures above.
    #[test]
    fn in_table_mark_stays_attached() {
        assert!(is_combining_mark('\u{0301}')); // combining acute, in range
        let input = "e\u{0301}cole";
        assert_eq!(tokenize_whole_identifier(input), vec![input]);
    }

    // -- NFC vs NFD normalization --

    /// The SAME visual string ("café") tokenizes to a DIFFERENT token when
    /// spelled with a precomposed é (NFC, U+00E9, one char) vs a decomposed
    /// e + combining acute (NFD, two chars, "e\u{0301}"). Both are valid
    /// content runs under `classify_char` (é is alphanumeric; e and the
    /// combining mark are both content), so neither is dropped or
    /// misclassified — but the resulting `String`s are byte-for-byte
    /// different. A document indexed in one normalization and a query typed
    /// in the other will NOT match on this term. This is a real
    /// search-correctness gap, undocumented in the module's doc comments —
    /// not a hard test failure of the stated contract (nothing in the
    /// contract promises normalization-insensitivity), but a materially
    /// relevant omission for a search library. Severity: MEDIUM — affects
    /// any accented-identifier content sourced from an NFD-normalized
    /// pipeline (e.g. some macOS filesystem APIs, some XML/HTML tooling)
    /// against NFC-typed queries (the common case for hand-typed queries).
    #[test]
    fn nfc_and_nfd_forms_of_the_same_visual_string_tokenize_differently() {
        let nfc = "café"; // precomposed é, U+00E9
        let nfd = "cafe\u{0301}"; // e + combining acute, U+0065 U+0301
        let precomposed_result = tokenize_whole_identifier(nfc);
        let decomposed_result = tokenize_whole_identifier(nfd);
        assert_eq!(precomposed_result, vec!["café"]);
        assert_eq!(decomposed_result, vec!["cafe\u{0301}"]);
        // The two outputs are NOT equal as strings -- this assertion
        // documents the gap; it is expected to hold (pass), demonstrating
        // the normalization-sensitivity defect exists.
        assert_ne!(
            precomposed_result, decomposed_result,
            "NFC and NFD forms of the same visual word produced different \
             tokens -- normalization-sensitivity gap confirmed"
        );
        // Same gap in the case-split tokenizer.
        assert_ne!(
            tokenize_all_case_split(nfc),
            tokenize_all_case_split(nfd),
            "case-split tokenizer has the same NFC/NFD gap"
        );
    }

    /// `José`, the task's required example, in NFD form -- confirm it
    /// lowercases and tokenizes without panicking, and note (again) that its
    /// output differs from the NFC form used in the SE's own tests.
    #[test]
    fn jose_nfd_form_tokenizes_without_panic_but_differs_from_nfc() {
        let nfc = "José"; // precomposed é
        let nfd = "Jose\u{0301}"; // e + combining acute
        assert_eq!(tokenize_whole_identifier(nfc), vec!["josé"]);
        assert_eq!(tokenize_whole_identifier(nfd), vec!["jose\u{0301}"]);
        assert_ne!(
            tokenize_whole_identifier(nfc),
            tokenize_whole_identifier(nfd)
        );
    }

    // -- Case scripts: Greek, Cyrillic, Turkish dotted/dotless I --

    #[test]
    fn greek_camel_case_style_identifier_splits_on_case_boundary() {
        // Μείζων (upper Mu + lowercase run) + Τεστ (upper Tau + lowercase
        // run) -- a camelCase-shaped Greek identifier, lower->upper
        // triggers the split same as Latin.
        assert_eq!(
            tokenize_all_case_split("ΜείζωνΤεστ"),
            vec!["μείζων", "τεστ"]
        );
    }

    /// Turkish dotted/dotless I is Unicode's canonical `to_lowercase`
    /// gotcha: `İ` (dotted capital I, U+0130) lowercases to `i̇` (TWO chars:
    /// `i` + combining dot above, U+0069 U+0307) under locale-independent
    /// Unicode case folding -- NOT to plain ASCII `i`. `ı` (dotless lowercase
    /// i, U+0131) has no uppercase Turkish-specific mapping in the default
    /// table and stays as-is. Neither causes a panic; both produce
    /// well-defined (if linguistically Turkish-surprising) output.
    #[test]
    fn turkish_dotted_and_dotless_i_lowercase_without_panic() {
        let dotted_i_result = tokenize_whole_identifier("İstanbul");
        // İ -> i + combining dot above (U+0307, in the SE's table range
        // 0x0300..=0x036F) under default Unicode case folding.
        assert_eq!(dotted_i_result, vec!["i\u{307}stanbul"]);

        let dotless_i_result = tokenize_whole_identifier("KAPICI\u{131}");
        // Dotless ı (U+0131) has no case mapping under default Unicode
        // rules and lowercases to itself; the ASCII "KAPICI" lowercases
        // normally via standard (non-Turkish) case folding.
        assert_eq!(dotless_i_result, vec!["kapici\u{131}"]);
    }

    // -- CJK: confirm documented limitation, not a crash --

    #[test]
    fn mixed_cjk_sentence_with_particles_and_punctuation_is_one_run_per_script_segment() {
        // "日本語のトレース" -- CJK kanji + hiragana particle "の" + katakana
        // "トレース" ("trace"), no ASCII/whitespace boundaries anywhere.
        // Documented v1 limitation: the entire caseless run becomes ONE
        // token (no crash, no truncation, no duplication).
        let input = "日本語のトレース";
        let whole = tokenize_whole_identifier(input);
        let split = tokenize_all_case_split(input);
        assert_eq!(whole, vec![input]);
        assert_eq!(split, vec![input]);
    }

    // -- Compound emoji: ZWJ sequences, skin-tone modifiers, flags --

    #[test]
    fn zwj_family_emoji_sequence_is_all_boundary_chars_no_token_emitted() {
        // Family emoji: man + ZWJ + woman + ZWJ + girl, U+1F468 U+200D
        // U+1F469 U+200D U+1F467. Every constituent codepoint (including
        // ZWJ, U+200D, a format-control char) is non-alphanumeric per
        // `char::is_alphanumeric` and not in the combining-mark table, so
        // classify_char treats each as an independent Boundary -- the whole
        // cluster produces zero tokens, and text around it is unaffected.
        let input = "team\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}rollout";
        assert_eq!(tokenize_whole_identifier(input), vec!["team", "rollout"]);
        assert_eq!(tokenize_all_case_split(input), vec!["team", "rollout"]);
        // Standalone: the whole cluster alone yields no tokens at all.
        let standalone = "\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}";
        assert_eq!(tokenize_whole_identifier(standalone), Vec::<String>::new());
    }

    #[test]
    fn skin_tone_modifier_emoji_is_all_boundary_no_token_emitted() {
        // Thumbs-up + medium skin tone modifier, U+1F44D U+1F3FD.
        let input = "ok\u{1F44D}\u{1F3FD}done";
        assert_eq!(tokenize_whole_identifier(input), vec!["ok", "done"]);
        assert_eq!(tokenize_all_case_split(input), vec!["ok", "done"]);
        let standalone = "\u{1F44D}\u{1F3FD}";
        assert_eq!(tokenize_whole_identifier(standalone), Vec::<String>::new());
    }

    #[test]
    fn regional_indicator_flag_emoji_is_all_boundary_no_token_emitted() {
        // US flag: regional indicator U (U+1F1FA) + regional indicator S
        // (U+1F1F8) -- two symbol codepoints, each independently a
        // boundary.
        let input = "region\u{1F1FA}\u{1F1F8}code";
        assert_eq!(tokenize_whole_identifier(input), vec!["region", "code"]);
        assert_eq!(tokenize_all_case_split(input), vec!["region", "code"]);
        let standalone = "\u{1F1FA}\u{1F1F8}";
        assert_eq!(tokenize_whole_identifier(standalone), Vec::<String>::new());
    }

    // -- Parity beyond the SE's own single parity test --

    #[test]
    fn both_tokenizers_agree_on_content_vs_boundary_for_a_dense_unicode_mix() {
        let input = "Ω_Test日本2\u{1F600}Ω";
        let whole_content: Vec<char> = input
            .chars()
            .filter(|c| {
                matches!(
                    classify_char(*c),
                    CharClass::AlphanumericContent | CharClass::Connector
                )
            })
            .collect();
        let split_content: Vec<char> = input
            .chars()
            .filter(|c| classify_char(*c) == CharClass::AlphanumericContent)
            .collect();
        // Sanity: whole-identifier's content set is a superset (includes
        // `_`) of case-split's content set for this input containing `_`.
        assert!(whole_content.len() >= split_content.len());
        // Neither tokenizer panics or drops non-underscore content chars.
        let _ = tokenize_whole_identifier(input);
        let _ = tokenize_all_case_split(input);
    }

    // -- Determinism / locale-independence --

    #[test]
    fn determinism_holds_across_repeated_calls_for_dense_unicode_input() {
        let input = "José\u{1F468}\u{200D}\u{1F469}getΜείζωνΤεστ日本語_v2Client\u{0951}";
        let a1 = tokenize_whole_identifier(input);
        let a2 = tokenize_whole_identifier(input);
        let b1 = tokenize_all_case_split(input);
        let b2 = tokenize_all_case_split(input);
        assert_eq!(a1, a2);
        assert_eq!(b1, b2);
    }
}
