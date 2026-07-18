//! [`crate::parse`]'s error type — every parse-time failure the language
//! spec defines, always positioned.
//!
//! Parsing never panics on any input (malformed, adversarial, huge, or
//! non-ASCII) — every rejection surfaces here, never as a panic or hang.
//!
//! `parser.rs` drives the grammar with winnow's token-level primitives
//! (`take_while`, `literal`, and friends) over a plain `&str` stream, but
//! resolves the grammar's own commit-vs-backtrack decisions by hand — which
//! branch of `AND`/`OR`/`NOT` a run of whitespace belongs to depends on
//! which keyword (if any) follows it, not on any one sub-parse's
//! success/failure, so it doesn't fit winnow's generic `alt`/`cut_err`
//! shape cleanly. Every failure site instead records the exact remaining
//! input slice at the point of failure; because that slice is always a
//! suffix of the original input (never reallocated), [`Location::at`]
//! recovers its byte offset with a plain length subtraction — no pointer
//! arithmetic, no risk of a stale offset from a slice that moved.

use std::fmt;

/// Why [`crate::parse`] could not produce a [`crate::Query`].
///
/// One variant per parse-time failure class the language spec's edge-case
/// table defines (unbalanced parens, trailing operator, stray token,
/// unterminated phrase, malformed range, mixed set join, lone reserved
/// word) plus a catch-all [`Self::Unexpected`] for any other grammar
/// mismatch — every one carries a [`Location`]. There is deliberately no
/// eval-time variant here (`UnknownFacet`, `RangeOnNonOrdered`) — those are
/// the evaluator's `EvalDiagnostic`, a separate, later concern; a
/// syntactically valid query is never rejected by this type regardless of
/// what a facet source later makes of it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParseError {
    /// Where in the input the failure was detected.
    pub at: Location,
    /// What the parser expected to find at [`Self::at`], and did not.
    pub kind: ParseErrorKind,
}

/// The specific grammar expectation a [`ParseError`] failed to meet.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum ParseErrorKind {
    /// A `(` (boolean group or set shorthand) is never closed by a matching
    /// `)`.
    UnclosedParen,
    /// A `[` range is never closed by a matching `]`.
    UnclosedRange,
    /// A quoted phrase's opening `"` is never closed by a matching `"`.
    UnterminatedPhrase,
    /// A range is missing its `TO` keyword, or `TO` appears without a
    /// well-formed bound on one or both sides.
    MalformedRange,
    /// A `facet:( ... )` set mixes `AND` and `OR` joins in one group —
    /// exactly one join is allowed per group.
    MixedSetJoin,
    /// `AND`, `OR`, `NOT`, or `TO` appeared unescaped and unquoted where a
    /// term/bareword was expected — a reserved word standing alone.
    ReservedWord,
    /// `(`-nesting exceeds the parser's recursion-depth limit. Not part of
    /// the language grammar — a defensive bound so pathological input
    /// (thousands of nested groups) is rejected deterministically instead
    /// of recursing until the process aborts.
    TooDeeplyNested,
    /// A stray or unexpected token — a closing `)` with no matching open,
    /// two operators back to back, adjacent atoms with no separating
    /// whitespace, or any other grammar mismatch not covered by a more
    /// specific variant above.
    Unexpected {
        /// A short, human-readable description of what was expected.
        expected: &'static str,
    },
}

/// A position in the original query string, both as a byte offset (for
/// tooling that wants to slice the input) and as 1-based line/column (for a
/// human-readable message) — computed by scanning the input up to that
/// offset, counting `\n` bytes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Location {
    /// 1-based line number.
    pub line: usize,
    /// 1-based column number, counted in `char`s (not bytes) since the
    /// last newline.
    pub column: usize,
    /// 0-based byte offset into the original input.
    pub offset: usize,
}

impl Location {
    pub(crate) fn at(input: &str, offset: usize) -> Self {
        let consumed = &input[..offset];
        let line = consumed.bytes().filter(|&b| b == b'\n').count() + 1;
        let column = match consumed.rfind('\n') {
            Some(nl) => consumed[nl + 1..].chars().count() + 1,
            None => consumed.chars().count() + 1,
        };
        Self {
            line,
            column,
            offset,
        }
    }
}

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "facetquery parse error at line {}, column {}: {}",
            self.at.line, self.at.column, self.kind
        )
    }
}

impl fmt::Display for ParseErrorKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::UnclosedParen => write!(f, "unbalanced parentheses — '(' is never closed"),
            Self::UnclosedRange => write!(f, "'[' range is never closed with ']'"),
            Self::UnterminatedPhrase => write!(f, "quoted phrase is never closed with '\"'"),
            Self::MalformedRange => {
                write!(
                    f,
                    "malformed range — expected 'TO' and a bound on each side"
                )
            }
            Self::MixedSetJoin => write!(
                f,
                "a facet:( ... ) set mixes AND and OR — exactly one join is allowed per group"
            ),
            Self::ReservedWord => write!(
                f,
                "AND/OR/NOT/TO is reserved here — quote or escape it to search the literal word"
            ),
            Self::TooDeeplyNested => write!(f, "too many nested '(' groups"),
            Self::Unexpected { expected } => write!(f, "unexpected token, expected {expected}"),
        }
    }
}

impl std::error::Error for ParseError {}
