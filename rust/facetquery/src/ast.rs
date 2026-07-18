//! The public `facetquery@1` AST — [`parse`][crate::parse]'s output type and
//! the pinned contract every downstream consumer (the eval-time evaluator,
//! any caller inspecting a parsed query) builds against.
//!
//! Every type here derives `Debug, Clone, PartialEq, Eq`, so two parses of
//! the same input are byte-identical under both — see the crate's
//! determinism guarantee on [`crate::parse`]. `Matcher` and `Seg` are
//! `#[non_exhaustive]`: both are likely to grow a variant in a future
//! `facetquery` minor version without that being a breaking grammar change
//! (e.g. a new matcher shape), so callers must not exhaustively match them
//! without a wildcard arm.

/// A fully parsed `facetquery@1` query — the root of the AST.
///
/// An empty or whitespace-only input parses to `Query { expr: Expr::And(vec![]) }`
/// — the language's canonical match-all representation (see the crate-level
/// docs). There is no dedicated "match everything" AST node; a zero-conjunct
/// `AND` is vacuously true under conjunction, which is exactly the semantics
/// evaluation needs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Query {
    /// The query's top-level boolean expression.
    pub expr: Expr,
}

/// A boolean expression node.
///
/// `And`/`Or` hold every operand at that precedence level as a flat `Vec`
/// (not a binary tree) — `a AND b AND c` is one `And(vec![a, b, c])`, not
/// nested pairwise `And`s. A single-operand chain (e.g. a lone `NOT a` with
/// no surrounding `AND`/`OR`) collapses to that operand directly rather than
/// wrapping it in a one-element `And`/`Or` — this keeps the AST minimal and
/// matches the precedence table in the language spec exactly (each row's
/// "parses as" column is this collapsing rule applied literally).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Expr {
    /// `a AND b [AND c ...]`, or implicit-AND-by-adjacency (`a b`) — same
    /// node either way; the grammar does not distinguish which was written.
    And(Vec<Expr>),
    /// `a OR b [OR c ...]`. There is no implicit OR.
    Or(Vec<Expr>),
    /// `NOT x` or `-x` — same node regardless of which token was written.
    Not(Box<Expr>),
    /// An explicitly parenthesized `( expr )`. Kept distinct from its inner
    /// expression (rather than being unwrapped away) so a query's written
    /// grouping is recoverable from the AST; evaluation treats `Group`
    /// transparently (it changes precedence, never meaning).
    Group(Box<Expr>),
    /// A leaf: one predicate (`facet:value`) or bareword/phrase term.
    Pred(Predicate),
}

/// One leaf predicate: an optional facet scope plus how its value matches.
///
/// `facet: None` is a bareword/phrase full-text term (`value` with no
/// `facet:` prefix); `facet: Some(name)` is `name:value`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Predicate {
    /// The facet name, or `None` for a bareword/phrase full-text term.
    pub facet: Option<String>,
    /// How this predicate's value is matched.
    pub matcher: Matcher,
}

/// How a predicate's value is matched against a facet (or full text).
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum Matcher {
    /// A single term: exact/wildcard equality, or full-text search.
    Term(Term),
    /// `facet:(v1 OR v2)` / `facet:(v1 AND v2)` — one join for the whole
    /// group (mixing `AND`/`OR` in one set is a parse-time error; see
    /// [`crate::ParseError`]).
    Set {
        /// The set's member terms, in source order.
        terms: Vec<Term>,
        /// The single join every member is combined with.
        join: SetJoin,
    },
    /// `facet:[lo TO hi]` — inclusive range, either bound may be unbounded.
    /// Range eligibility by facet type is an eval-time concern, not
    /// enforced by the parser (see the language spec's type-gating
    /// section) — the parser accepts this shape for any facet name.
    Range {
        /// The range's lower bound.
        lo: Bound,
        /// The range's upper bound.
        hi: Bound,
    },
    /// `facet:>v`, `facet:>=v`, `facet:<v`, `facet:<=v`.
    Cmp {
        /// Which comparison operator was written.
        op: CmpOp,
        /// The operand compared against.
        term: Term,
    },
    /// `facet:*` (or its negation, `NOT facet:*` / `-facet:*`, via the
    /// surrounding `Expr::Not`) — matches when the facet has at least one
    /// value on the document (a schema-known facet the document simply
    /// doesn't set is `Present` with empty `values`, per
    /// [`crate::eval::FacetLookup::Present`]'s doc, and does NOT satisfy
    /// this). Not a distinct grammar production: a term
    /// of exactly one segment, `Seg::StarWild`, written as a facet's value,
    /// is reclassified from `Matcher::Term` to this variant by the parser.
    /// A bareword (no `facet:` prefix) that is the single wildcard segment
    /// `*` stays `Matcher::Term` — it means "match all text", not "exists"
    /// (see the language spec's edge-case table entry for a bare `*`).
    Exists,
}

/// The join keyword shared by every member of one `facet:( ... )` set.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SetJoin {
    /// `facet:(v1 AND v2 ...)`.
    And,
    /// `facet:(v1 OR v2 ...)`.
    Or,
}

/// A range/comparison bound.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Bound {
    /// A concrete bound value.
    Value(Term),
    /// `*` on that side — that side is unbounded.
    Unbounded,
}

/// A comparison operator (`facet:>v`, etc).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CmpOp {
    /// `>`
    Gt,
    /// `>=`
    Ge,
    /// `<`
    Lt,
    /// `<=`
    Le,
}

/// One term: a bareword, a facet value, a quoted phrase, or a range/
/// comparison operand — anywhere the grammar accepts a single value.
///
/// `raw` is the term's fully unescaped text (quotes stripped, `\x` resolved
/// to `x`, wildcard characters kept as `*`/`?`) — the same text `segments`
/// decomposes structurally. `segments` is what a matcher actually walks: a
/// quoted phrase always yields exactly one `Seg::Literal` (quoting disables
/// wildcard meaning entirely — see the language spec's escaping table), an
/// unquoted term interleaves `Literal` runs with `StarWild`/`QuestionWild`
/// wherever an unescaped `*`/`?` appeared, and a range/comparison operand
/// (a `scalar`, never wildcarded) also always yields one `Seg::Literal`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Term {
    /// The term's fully unescaped text.
    pub raw: String,
    /// The term decomposed into literal runs and wildcard markers.
    pub segments: Vec<Seg>,
}

/// One piece of a [`Term`]'s decomposition.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum Seg {
    /// A run of literal (non-wildcard) text.
    Literal(String),
    /// An unescaped `*` outside a quoted phrase — matches zero or more of
    /// any character.
    StarWild,
    /// An unescaped `?` outside a quoted phrase — matches exactly one of
    /// any character.
    QuestionWild,
}
