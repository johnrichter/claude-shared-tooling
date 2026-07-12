//! The `facetquery@1` grammar: a hand-written recursive-descent parser
//! matching `facetquery.ebnf` production-for-production, over a plain
//! `&str` cursor threaded as `&mut &str` (winnow's native representation of
//! a borrowed string stream).
//!
//! # Where winnow does the work, and where this file does
//! Every leaf-level token match (`ws0`/`ws1`, keyword/delimiter literals via
//! `eat_str`) is winnow's own `take_while`/`literal`, chosen because both
//! guarantee "no partial consumption on failure" — exactly the primitive
//! this grammar's backtracking needs. What's hand-written is the grammar's
//! *structure*: the ordered-choice productions (`predicate_value`,
//! `primary`, `bound`) dispatch unambiguously on the next character, so
//! they need no backtracking at all — a plain `if`/`else` chain on the
//! leading byte is both simpler and faster than winnow's generic `alt`.
//! The one place real backtracking matters — `and_expr`/`or_expr`'s
//! `{ws1, keyword, ws1, item}` loops — has a commit decision that depends
//! on *which* keyword (if any) matched, not just on one sub-parse's
//! success, which doesn't fit winnow's `opt`/`cut_err` shape without a
//! bespoke combinator; each loop instead saves the cursor before attempting
//! a separator and restores it verbatim if the attempt doesn't pan out —
//! see `and_expr` for the one subtlety this requires (an unmatched `AND`
//! keyword must still fall through to implicit-AND, not just stop the
//! chain — see its comment). The escape-aware bare-word scan (`scan_bare`)
//! is also hand-written: it needs to report both the decoded text *and*
//! whether an escape occurred (to decide the reserved-word bypass), which
//! is one job winnow's single-purpose token combinators don't cover.
//!
//! # Determinism and no panics
//! Every function here is a total, pure function of its `&str` input: no
//! allocation-order-dependent behavior. Parenthesis nesting recurses once
//! per `(` (via `group`), so unbounded nesting would otherwise recurse
//! proportionally to the input's own length and abort the process on stack
//! overflow — [`MAX_GROUP_DEPTH`] caps that recursion and turns it into an
//! ordinary positioned `ParseError` instead. No `unwrap`/`expect`/indexing
//! panic path: every slice taken is either a fixed-width ASCII literal
//! already confirmed present by `starts_with`, or a `char`-boundary-safe
//! length computed from `char::len_utf8`/`str::find`.

use winnow::error::{ContextError, ErrMode};
use winnow::token::{literal, take_while};
use winnow::Parser;

use crate::ast::{Bound, CmpOp, Expr, Matcher, Predicate, Query, Seg, SetJoin, Term};
use crate::error::{Location, ParseError, ParseErrorKind};

/// A parse failure at a specific point in the original input. `at` is
/// always an exact suffix of the input `parse` was called with (every
/// function here only ever narrows its cursor via slicing, never builds a
/// new `String`), so [`Location::at`] recovers its byte offset with a
/// simple length subtraction once, at the top level — see `parse` below.
struct Fail<'a> {
    at: &'a str,
    kind: ParseErrorKind,
}

type PResult<'a, T> = Result<T, Fail<'a>>;

fn fail<T>(at: &str, kind: ParseErrorKind) -> PResult<'_, T> {
    Err(Fail { at, kind })
}

const RESERVED_WORDS: [&str; 4] = ["AND", "OR", "NOT", "TO"];

fn is_ws(c: char) -> bool {
    matches!(c, ' ' | '\t' | '\n' | '\r')
}

fn is_reserved_punct(c: char) -> bool {
    matches!(
        c,
        '"' | '\\' | ':' | '(' | ')' | '[' | ']' | '*' | '?' | '<' | '>'
    )
}

fn is_boundary(c: char) -> bool {
    is_ws(c) || is_reserved_punct(c)
}

/// Consumes leading whitespace; always succeeds (`ws` in the EBNF — zero or
/// more).
fn ws0(input: &mut &str) {
    let _: Result<&str, ErrMode<ContextError>> = take_while(0.., is_ws).parse_next(input);
}

/// Consumes leading whitespace; requires at least one (`ws1` — mandatory
/// separator). `take_while(1.., ..)` never consumes on failure (winnow's
/// `take_till1`, which it dispatches to, only calls `next_slice` once it
/// has confirmed a nonzero match), so `start` below is always exactly where
/// `input` still sits when this returns `Err`.
fn ws1<'a>(input: &mut &'a str) -> PResult<'a, ()> {
    let start = *input;
    let matched: Result<&str, ErrMode<ContextError>> = take_while(1.., is_ws).parse_next(input);
    matched.map(|_| ()).map_err(|_| Fail {
        at: start,
        kind: ParseErrorKind::Unexpected {
            expected: "whitespace",
        },
    })
}

/// Consumes `lit` if `input` starts with it; leaves `input` untouched
/// otherwise (winnow's `literal` never partially matches).
fn eat_str(input: &mut &str, lit: &str) -> bool {
    let result: Result<&str, ErrMode<ContextError>> = literal(lit).parse_next(input);
    result.is_ok()
}

/// Scans the longest possible run of `bare_char` (the EBNF's
/// `reserved_free_run`) starting at `s`, without consuming anything —
/// callers decide whether to commit. Returns the decoded (escape-resolved)
/// text, how many bytes of `s` it spans, and whether any escape occurred —
/// the last one is what lets `\AND` bypass the reserved-word exclusion
/// while a bare `AND` doesn't. A trailing lone `\` with nothing after it to
/// escape simply ends the run there (it stays in the unconsumed remainder,
/// where whatever comes next — closing delimiter, whitespace, `eoi` —
/// reports it as a stray token; never a panic on truncated input).
fn scan_bare(s: &str) -> (String, usize, bool) {
    let mut decoded = String::new();
    let mut consumed = 0usize;
    let mut had_escape = false;
    loop {
        let rest = &s[consumed..];
        match rest.chars().next() {
            None => break,
            Some('\\') => {
                let after = &rest[1..];
                match after.chars().next() {
                    Some(c2) => {
                        decoded.push(c2);
                        had_escape = true;
                        consumed += 1 + c2.len_utf8();
                    }
                    None => break,
                }
            }
            Some(c) if is_boundary(c) => break,
            Some(c) => {
                decoded.push(c);
                consumed += c.len_utf8();
            }
        }
    }
    (decoded, consumed, had_escape)
}

fn is_reserved_word(s: &str) -> bool {
    RESERVED_WORDS.contains(&s)
}

/// Non-consuming lookahead: is the bare-char run starting at `s` exactly
/// one of the reserved words, unescaped? Used only to pick a more specific
/// [`ParseErrorKind::ReservedWord`] over a generic "expected a term" when a
/// term production comes up empty.
fn detect_reserved(s: &str) -> bool {
    let (decoded, consumed, had_escape) = scan_bare(s);
    consumed > 0 && !had_escape && is_reserved_word(&decoded)
}

/// Consumes one `literal_run` (`reserved_free_run - reserved_word`) if
/// present; leaves `input` untouched (returns `None`) if the run would be
/// empty, or if it's exactly an unescaped reserved word.
fn take_literal_run(input: &mut &str) -> Option<String> {
    let (decoded, consumed, had_escape) = scan_bare(input);
    if consumed == 0 || (!had_escape && is_reserved_word(&decoded)) {
        return None;
    }
    *input = &input[consumed..];
    Some(decoded)
}

/// `facet_key = key_head, { key_tail }` — ASCII-only per the EBNF (`letter`
/// there is `'A'..'Z' | 'a'..'z'`). Returns the run's byte length, `0` if
/// the input doesn't even start with a `key_head` char.
fn scan_facet_key(input: &str) -> usize {
    let bytes = input.as_bytes();
    let is_head = |b: u8| b.is_ascii_alphanumeric() || b == b'_';
    let is_tail = |b: u8| is_head(b) || b == b'-' || b == b'.';
    match bytes.first() {
        Some(&b) if is_head(b) => {}
        _ => return 0,
    }
    let mut end = 1;
    while end < bytes.len() && is_tail(bytes[end]) {
        end += 1;
    }
    end
}

/// `quoted_phrase = '"', { phrase_char }, '"'` — the whole decoded content
/// becomes one `Seg::Literal`; quoting disables wildcard meaning entirely,
/// so no `*`/`?` segment is ever produced here.
fn quoted_phrase<'a>(input: &mut &'a str) -> PResult<'a, Term> {
    let start = *input;
    let mut rest = &input[1..];
    let mut decoded = String::new();
    loop {
        match rest.chars().next() {
            None => return fail(start, ParseErrorKind::UnterminatedPhrase),
            Some('"') => {
                *input = &rest[1..];
                return Ok(Term {
                    raw: decoded.clone(),
                    segments: vec![Seg::Literal(decoded)],
                });
            }
            Some('\\') => {
                let after = &rest[1..];
                match after.chars().next() {
                    Some(c2) => {
                        decoded.push(c2);
                        rest = &after[c2.len_utf8()..];
                    }
                    None => return fail(start, ParseErrorKind::UnterminatedPhrase),
                }
            }
            Some(c) => {
                decoded.push(c);
                rest = &rest[c.len_utf8()..];
            }
        }
    }
}

/// `wildcard_term = wild_segment, { wild_segment }` — interleaves literal
/// runs with `*`/`?` markers. Fails (never partially consumes) if not even
/// one `wild_segment` is present.
fn wildcard_term<'a>(input: &mut &'a str) -> PResult<'a, Term> {
    let start = *input;
    let mut raw = String::new();
    let mut segments = Vec::new();
    loop {
        match input.chars().next() {
            Some('*') => {
                *input = &input[1..];
                raw.push('*');
                segments.push(Seg::StarWild);
            }
            Some('?') => {
                *input = &input[1..];
                raw.push('?');
                segments.push(Seg::QuestionWild);
            }
            _ => match take_literal_run(input) {
                Some(text) => {
                    raw.push_str(&text);
                    segments.push(Seg::Literal(text));
                }
                None => break,
            },
        }
    }
    if segments.is_empty() {
        let kind = if detect_reserved(start) {
            ParseErrorKind::ReservedWord
        } else {
            ParseErrorKind::Unexpected { expected: "a term" }
        };
        return fail(start, kind);
    }
    Ok(Term { raw, segments })
}

/// `term = quoted_phrase | wildcard_term`.
fn term<'a>(input: &mut &'a str) -> PResult<'a, Term> {
    if input.starts_with('"') {
        quoted_phrase(input)
    } else {
        wildcard_term(input)
    }
}

/// `scalar = quoted_phrase | literal_run` — a range/comparison operand:
/// exact text, never wildcarded, always exactly one `Seg::Literal`.
fn scalar<'a>(input: &mut &'a str) -> PResult<'a, Term> {
    if input.starts_with('"') {
        return quoted_phrase(input);
    }
    let start = *input;
    if let Some(text) = take_literal_run(input) {
        Ok(Term {
            raw: text.clone(),
            segments: vec![Seg::Literal(text)],
        })
    } else {
        let kind = if detect_reserved(start) {
            ParseErrorKind::ReservedWord
        } else {
            ParseErrorKind::Unexpected {
                expected: "a value",
            }
        };
        fail(start, kind)
    }
}

/// `bound = "*" | scalar` — `*` here is the single unbounded marker
/// character, never a wildcard pattern (a `scalar` can never start with an
/// unescaped `*` anyway, since `*` is excluded from `bare_char`), so no
/// ambiguity between the two alternatives is possible.
fn bound<'a>(input: &mut &'a str) -> PResult<'a, Bound> {
    if input.starts_with('*') {
        *input = &input[1..];
        return Ok(Bound::Unbounded);
    }
    Ok(Bound::Value(scalar(input)?))
}

/// `range_value = "[", ws, bound, ws1, "TO", ws1, bound, ws, "]"`.
fn range_value<'a>(input: &mut &'a str) -> PResult<'a, Matcher> {
    let open = *input;
    *input = &input[1..];
    ws0(input);
    let lo = bound(input)?;
    if ws1(input).is_err() {
        return fail(input, ParseErrorKind::MalformedRange);
    }
    if !eat_str(input, "TO") {
        return fail(input, ParseErrorKind::MalformedRange);
    }
    if ws1(input).is_err() {
        return fail(input, ParseErrorKind::MalformedRange);
    }
    let hi = bound(input)?;
    ws0(input);
    if !eat_str(input, "]") {
        return fail(open, ParseErrorKind::UnclosedRange);
    }
    Ok(Matcher::Range { lo, hi })
}

/// `set_value = "(", ws, term, { ws1, set_op, ws1, term }, ws, ")"` — every
/// `set_op` in one group must be the same keyword.
fn set_value<'a>(input: &mut &'a str) -> PResult<'a, Matcher> {
    let open = *input;
    *input = &input[1..];
    ws0(input);
    let first = term(input)?;
    let mut terms = vec![first];
    let mut join: Option<SetJoin> = None;
    loop {
        let checkpoint = *input;
        if ws1(input).is_err() {
            break;
        }
        let candidate = if eat_str(input, "OR") {
            Some(SetJoin::Or)
        } else if eat_str(input, "AND") {
            Some(SetJoin::And)
        } else {
            None
        };
        let Some(op) = candidate else {
            *input = checkpoint;
            break;
        };
        if ws1(input).is_err() {
            // Keyword matched but no mandatory trailing whitespace -- not a
            // real set_op occurrence (a set has no implicit continuation to
            // fall back to, unlike and_expr's implicit-AND); the whole
            // separator attempt is abandoned.
            *input = checkpoint;
            break;
        }
        match join {
            None => join = Some(op),
            Some(existing) if existing == op => {}
            Some(_) => return fail(input, ParseErrorKind::MixedSetJoin),
        }
        terms.push(term(input)?);
    }
    ws0(input);
    if !eat_str(input, ")") {
        return fail(open, ParseErrorKind::UnclosedParen);
    }
    Ok(Matcher::Set {
        terms,
        join: join.unwrap_or(SetJoin::Or),
    })
}

/// `predicate_value = set_value | range_value | cmp_value | term` — each
/// alternative starts on a distinct leading character, so dispatch never
/// backtracks.
fn predicate_value<'a>(input: &mut &'a str) -> PResult<'a, Matcher> {
    if input.starts_with('(') {
        return set_value(input);
    }
    if input.starts_with('[') {
        return range_value(input);
    }
    if eat_str(input, ">=") {
        return Ok(Matcher::Cmp {
            op: CmpOp::Ge,
            term: scalar(input)?,
        });
    }
    if eat_str(input, ">") {
        return Ok(Matcher::Cmp {
            op: CmpOp::Gt,
            term: scalar(input)?,
        });
    }
    if eat_str(input, "<=") {
        return Ok(Matcher::Cmp {
            op: CmpOp::Le,
            term: scalar(input)?,
        });
    }
    if eat_str(input, "<") {
        return Ok(Matcher::Cmp {
            op: CmpOp::Lt,
            term: scalar(input)?,
        });
    }
    Ok(Matcher::Term(term(input)?))
}

/// Reclassifies a `facet:*` predicate's `Matcher::Term` into
/// [`Matcher::Exists`] — see that variant's doc comment for exactly which
/// shape qualifies, and why a bareword `*` does not go through this path
/// at all (it's built directly as `Matcher::Term` in `primary`, never
/// passed through here).
fn reclassify_exists(matcher: Matcher) -> Matcher {
    if let Matcher::Term(term) = &matcher {
        if let [Seg::StarWild] = term.segments.as_slice() {
            return Matcher::Exists;
        }
    }
    matcher
}

/// Maximum `(`-nesting depth `group` will descend before failing with
/// [`ParseErrorKind::TooDeeplyNested`] instead of recursing further —
/// without this, pathological input (thousands of nested `(`) recurses the
/// call stack proportionally and aborts the process on overflow, which
/// would violate this crate's no-panic guarantee just as much as an actual
/// panic. Generous for any realistic query, small enough to never come
/// close to exhausting a real thread's stack.
const MAX_GROUP_DEPTH: usize = 128;

/// `group = "(", ws, expr, ws, ")"`. `depth` counts enclosing `group`s —
/// see [`MAX_GROUP_DEPTH`].
fn group<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    let open = *input;
    if depth >= MAX_GROUP_DEPTH {
        return fail(open, ParseErrorKind::TooDeeplyNested);
    }
    *input = &input[1..];
    ws0(input);
    let e = expr(input, depth + 1)?;
    ws0(input);
    if !eat_str(input, ")") {
        return fail(open, ParseErrorKind::UnclosedParen);
    }
    Ok(e)
}

/// `primary = group | predicate | term` — dispatches on `(` for `group`;
/// otherwise looks ahead for a `facet_key ":"` to decide predicate vs.
/// bareword `term`. The facet-key lookahead is non-consuming (`scan_facet_key`
/// never mutates `input`), so falling through to `term` on a "not actually
/// a predicate" run (e.g. `hello` — matches `key_head`/`key_tail` but isn't
/// followed by `:`) re-parses cleanly from the original position, using
/// `term`'s own (wider) character rules rather than `facet_key`'s.
fn primary<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    if input.starts_with('(') {
        return Ok(Expr::Group(Box::new(group(input, depth)?)));
    }
    let key_len = scan_facet_key(input);
    if key_len > 0 && input.as_bytes().get(key_len) == Some(&b':') {
        let facet = input[..key_len].to_string();
        *input = &input[key_len + 1..];
        let matcher = reclassify_exists(predicate_value(input)?);
        return Ok(Expr::Pred(Predicate {
            facet: Some(facet),
            matcher,
        }));
    }
    Ok(Expr::Pred(Predicate {
        facet: None,
        matcher: Matcher::Term(term(input)?),
    }))
}

/// `not_expr = [ negation ], primary`; `negation = ("NOT", ws1) | "-"`.
/// `NOT` and `-` produce the identical `Expr::Not` node.
fn not_expr<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    let checkpoint = *input;
    let mut negated = false;
    if eat_str(input, "NOT") {
        if ws1(input).is_ok() {
            negated = true;
        } else {
            *input = checkpoint;
        }
    }
    if !negated && input.starts_with('-') {
        *input = &input[1..];
        negated = true;
    }
    let p = primary(input, depth)?;
    Ok(if negated { Expr::Not(Box::new(p)) } else { p })
}

/// `and_expr = not_expr, { and_op, not_expr }`; `and_op = ws1, ["AND", ws1]`.
///
/// A single-item chain collapses to that item directly rather than
/// `Expr::And(vec![item])` (see [`Expr::And`]'s doc comment).
///
/// The loop's one subtlety: `AND`'s keyword form requires whitespace on
/// *both* sides. If the mandatory whitespace matches but the word that
/// follows isn't `AND` (or is `AND` with no trailing whitespace, e.g. the
/// bareword `ANDfoo`), that is NOT a failed `and_op` — `and_op`'s `["AND",
/// ws1]` part is optional, so its absence just means this iteration is
/// plain implicit-AND, and the next `not_expr` is tried anyway, from right
/// after the whitespace (undoing only the keyword-length peek, not the
/// whitespace). Only once `AND` is captured *with* its trailing whitespace
/// does a subsequent `not_expr` failure become a hard error instead of a
/// reason to stop the chain and hand the whitespace back to the caller
/// (`or_expr`'s own separator check, or the top-level trailing-content
/// check).
fn and_expr<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    let first = not_expr(input, depth)?;
    let mut items = vec![first];
    loop {
        let checkpoint = *input;
        if ws1(input).is_err() {
            break;
        }
        let after_ws1 = *input;
        let mut explicit = false;
        if eat_str(input, "AND") {
            if ws1(input).is_ok() {
                explicit = true;
            } else {
                *input = after_ws1;
            }
        }
        match not_expr(input, depth) {
            Ok(e) => items.push(e),
            Err(e) => {
                if explicit {
                    return Err(e);
                }
                *input = checkpoint;
                break;
            }
        }
    }
    Ok(if items.len() == 1 {
        items.remove(0)
    } else {
        Expr::And(items)
    })
}

/// `or_expr = and_expr, { ws1, "OR", ws1, and_expr }`.
///
/// Unlike `and_expr`, there is no implicit-OR to fall back to, so this
/// loop is simpler: if the `ws1, "OR", ws1` separator doesn't match in
/// full, the whitespace is handed back (restored) and the loop just stops
/// — by then, `and_expr` has already greedily consumed everything it
/// validly could via implicit-AND, so any leftover whitespace here is
/// either a genuine `OR` or belongs to an enclosing production (a group's
/// closing `)`, or the top-level trailing-content check).
fn or_expr<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    let first = and_expr(input, depth)?;
    let mut items = vec![first];
    loop {
        let checkpoint = *input;
        if ws1(input).is_err() {
            break;
        }
        let after_ws1 = *input;
        let matched = eat_str(input, "OR") && ws1(input).is_ok();
        if !matched {
            *input = checkpoint;
            break;
        }
        let _ = after_ws1;
        items.push(and_expr(input, depth)?);
    }
    Ok(if items.len() == 1 {
        items.remove(0)
    } else {
        Expr::Or(items)
    })
}

/// `expr = or_expr` — the published precedence (`NOT` > `AND` > `OR`,
/// left-to-right) falls directly out of this call chain's nesting:
/// `or_expr` calls `and_expr` calls `not_expr` calls `primary`. `depth`
/// counts enclosing `group`s (see [`MAX_GROUP_DEPTH`]); every function in
/// this chain just threads it through unchanged except `group` itself.
fn expr<'a>(input: &mut &'a str, depth: usize) -> PResult<'a, Expr> {
    or_expr(input, depth)
}

/// `query = ws, [ expr ], ws, eoi`. An absent `expr` (empty or
/// whitespace-only input) is this crate's match-all representation — see
/// [`Query`]'s doc comment.
fn query_body<'a>(input: &mut &'a str) -> PResult<'a, Expr> {
    ws0(input);
    if input.is_empty() {
        return Ok(Expr::And(Vec::new()));
    }
    let e = expr(input, 0)?;
    ws0(input);
    Ok(e)
}

fn locate(source: &str, at: &str) -> Location {
    Location::at(source, source.len() - at.len())
}

/// Parses a `facetquery@1` query string into its AST.
///
/// Deterministic: identical input bytes always produce a byte-identical
/// [`Query`] (`Debug`/`PartialEq`-equal on every call, on every platform —
/// there is no allocation-order, hashing, or environment dependency
/// anywhere in this grammar). Never panics, on any input — malformed,
/// adversarial, arbitrarily large, or non-ASCII input is always a
/// well-formed `Err`, never a panic, index-out-of-bounds, or hang (the only
/// recursion is `(`-group nesting, which a fixed depth cap turns into a
/// `TooDeeplyNested` error rather than a stack overflow — `NOT`/`-` negation
/// is handled iteratively and adds no stack depth of its own).
///
/// This function is parse-time only: every error it can return is a syntax
/// violation the query string alone determines (see
/// [`ParseErrorKind`][crate::ParseErrorKind]). Eval-time concerns —
/// whether a facet exists, whether a range is meaningful for a facet's
/// type — depend on a facet source this crate never sees, and are never
/// raised as a [`ParseError`]; a syntactically valid query always parses to
/// a [`Query`] here, however it fares once evaluated.
///
/// # Errors
/// Returns [`ParseError`] for any syntax violation the grammar defines —
/// unbalanced parentheses, a trailing/dangling operator, an unterminated
/// phrase, a malformed range, a mixed-join set, a reserved word standing
/// alone, or any other stray/unexpected token. Always positioned (see
/// [`ParseError::at`]).
pub fn parse(source: &str) -> Result<Query, ParseError> {
    let mut rest = source;
    match query_body(&mut rest) {
        Ok(expr) if rest.is_empty() => Ok(Query { expr }),
        Ok(_) => Err(ParseError {
            at: locate(source, rest),
            kind: ParseErrorKind::Unexpected {
                expected: "end of input",
            },
        }),
        Err(Fail { at, kind }) => Err(ParseError {
            at: locate(source, at),
            kind,
        }),
    }
}

// Quick sanity check of the grammar's main shapes and a couple of error
// paths — the exhaustive per-construct/per-edge-case conformance suite and
// the no-panic property test are the test engineer's stage, not this file's.
#[cfg(test)]
mod tests {
    use super::*;

    fn term(raw: &str) -> Term {
        Term {
            raw: raw.to_string(),
            segments: vec![Seg::Literal(raw.to_string())],
        }
    }

    fn bareword(raw: &str) -> Expr {
        Expr::Pred(Predicate {
            facet: None,
            matcher: Matcher::Term(term(raw)),
        })
    }

    #[test]
    fn empty_and_whitespace_are_match_all() {
        assert_eq!(parse("").unwrap().expr, Expr::And(Vec::new()));
        assert_eq!(parse("   \t\n ").unwrap().expr, Expr::And(Vec::new()));
    }

    #[test]
    fn bareword_and_phrase() {
        assert_eq!(parse("hello").unwrap().expr, bareword("hello"));
        assert_eq!(
            parse("\"hello world\"").unwrap().expr,
            bareword("hello world")
        );
    }

    #[test]
    fn bare_star_is_wildcard_term_not_exists() {
        let expr = parse("*").unwrap().expr;
        assert_eq!(
            expr,
            Expr::Pred(Predicate {
                facet: None,
                matcher: Matcher::Term(Term {
                    raw: "*".to_string(),
                    segments: vec![Seg::StarWild],
                }),
            })
        );
    }

    #[test]
    fn facet_predicate_and_wildcard_segments() {
        let expr = parse("status:ser*ce").unwrap().expr;
        assert_eq!(
            expr,
            Expr::Pred(Predicate {
                facet: Some("status".to_string()),
                matcher: Matcher::Term(Term {
                    raw: "ser*ce".to_string(),
                    segments: vec![
                        Seg::Literal("ser".to_string()),
                        Seg::StarWild,
                        Seg::Literal("ce".to_string()),
                    ],
                }),
            })
        );
    }

    #[test]
    fn exists_and_not_exists() {
        let exists = parse("status:*").unwrap().expr;
        assert_eq!(
            exists,
            Expr::Pred(Predicate {
                facet: Some("status".to_string()),
                matcher: Matcher::Exists,
            })
        );
        let not_exists = parse("-status:*").unwrap().expr;
        assert_eq!(not_exists, Expr::Not(Box::new(exists)));
    }

    #[test]
    fn not_and_dash_alias_produce_identical_node() {
        assert_eq!(
            parse("NOT status:x").unwrap().expr,
            parse("-status:x").unwrap().expr
        );
    }

    #[test]
    fn implicit_and_matches_explicit_and() {
        assert_eq!(parse("a b").unwrap().expr, parse("a AND b").unwrap().expr);
    }

    #[test]
    fn precedence_and_binds_tighter_than_or() {
        // a OR b AND c => a OR (b AND c)
        assert_eq!(
            parse("a OR b AND c").unwrap().expr,
            Expr::Or(vec![
                bareword("a"),
                Expr::And(vec![bareword("b"), bareword("c")]),
            ])
        );
    }

    #[test]
    fn not_binds_tighter_than_and() {
        // NOT a AND b => (NOT a) AND b
        assert_eq!(
            parse("NOT a AND b").unwrap().expr,
            Expr::And(vec![Expr::Not(Box::new(bareword("a"))), bareword("b")])
        );
    }

    #[test]
    fn parens_override_precedence() {
        assert_eq!(
            parse("(a OR b) AND c").unwrap().expr,
            Expr::And(vec![
                Expr::Group(Box::new(Expr::Or(vec![bareword("a"), bareword("b")]))),
                bareword("c"),
            ])
        );
    }

    #[test]
    fn set_shorthand() {
        let expr = parse("status:(open OR closed)").unwrap().expr;
        assert_eq!(
            expr,
            Expr::Pred(Predicate {
                facet: Some("status".to_string()),
                matcher: Matcher::Set {
                    terms: vec![term("open"), term("closed")],
                    join: SetJoin::Or,
                },
            })
        );
    }

    #[test]
    fn mixed_set_join_is_a_parse_error() {
        let err = parse("status:(open OR closed AND foo)").unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::MixedSetJoin);
    }

    #[test]
    fn range_and_unbounded_bound() {
        let expr = parse("created:[* TO 100]").unwrap().expr;
        assert_eq!(
            expr,
            Expr::Pred(Predicate {
                facet: Some("created".to_string()),
                matcher: Matcher::Range {
                    lo: Bound::Unbounded,
                    hi: Bound::Value(term("100")),
                },
            })
        );
    }

    #[test]
    fn comparison_operators() {
        let expr = parse("created:>=100").unwrap().expr;
        assert_eq!(
            expr,
            Expr::Pred(Predicate {
                facet: Some("created".to_string()),
                matcher: Matcher::Cmp {
                    op: CmpOp::Ge,
                    term: term("100"),
                },
            })
        );
    }

    #[test]
    fn escaping_bypasses_reserved_word() {
        assert_eq!(parse(r"\AND").unwrap().expr, bareword("AND"));
        assert_eq!(parse("\"AND\"").unwrap().expr, bareword("AND"));
    }

    #[test]
    fn wildcards_are_literal_inside_quotes() {
        assert_eq!(parse("\"a*b?c\"").unwrap().expr, bareword("a*b?c"));
    }

    #[test]
    fn unclosed_paren_is_positioned_parse_error() {
        let err = parse("(a OR b").unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::UnclosedParen);
        assert_eq!(err.at.offset, 0);
    }

    #[test]
    fn unterminated_phrase_is_positioned_parse_error() {
        let err = parse("\"unterminated").unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::UnterminatedPhrase);
        assert_eq!(err.at.offset, 0);
    }

    #[test]
    fn trailing_operator_is_a_parse_error() {
        assert!(parse("a AND").is_err());
        assert!(parse("a OR").is_err());
    }

    #[test]
    fn reserved_word_alone_is_a_parse_error() {
        let err = parse("status:AND").unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::ReservedWord);
    }

    #[test]
    fn malformed_range_is_a_parse_error() {
        assert!(parse("created:[100 200]").is_err());
        assert!(parse("created:[100 TO").is_err());
    }

    #[test]
    fn excessive_group_nesting_is_a_parse_error_not_a_crash() {
        let err = parse(&"(".repeat(MAX_GROUP_DEPTH + 1)).unwrap_err();
        assert_eq!(err.kind, ParseErrorKind::TooDeeplyNested);
    }

    #[test]
    fn no_panic_on_adversarial_input() {
        let inputs = [
            "(((((((((((",
            ")))))))))))",
            "\"\\",
            "AND OR NOT TO",
            "-",
            "NOT",
            "facet:",
            "facet:[",
            "facet:(",
            "\u{0}\u{1}\u{2}",
            "🦀🦀🦀:🦀",
            &"(".repeat(2000),
            &"a ".repeat(2000),
        ];
        for input in inputs {
            let _ = parse(input);
        }
    }
}
