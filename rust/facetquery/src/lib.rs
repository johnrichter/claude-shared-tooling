//! `facetquery` — a pure, frontmatter-agnostic boolean facet-query language.
//!
//! # Scope (`facetquery@1`)
//! Parses a query string into the AST defined in [`ast`] via [`parse`], and
//! evaluates a parsed [`Query`] against any [`FacetSource`] via [`evaluate`]
//! (see [`eval`]). **Parse-time vs eval-time**: every error [`parse`] can
//! return is a syntax violation the query string alone determines; every
//! [`EvalDiagnostic`] instead needs the facet source too (a facet's
//! existence, a facet's type) — see [`ParseError`]'s and [`eval`]'s doc
//! comments for that boundary.
//!
//! Normative spec: `schemas/facetquery/facetquery-language.spec.md` +
//! `facetquery.ebnf` (same directory), both in this repo. Any divergence
//! between this crate and those two files is a crate defect — they are the
//! conformance oracle, not this implementation.
//!
//! # Match-all representation
//! An empty or whitespace-only query is valid and matches every document
//! (the spec's own words: "no predicate to fail"). This crate represents
//! that as `Query { expr: Expr::And(vec![]) }` — a zero-conjunct `AND`,
//! vacuously true — rather than a dedicated "match everything" AST node,
//! since the pinned `Expr` enum has none. Keep this representation in mind
//! when writing the evaluator: `Expr::And(items)` must already handle
//! `items.is_empty()` as "true", not as a special case bolted on later.
//!
//! # Determinism
//! [`parse`] is a pure function of its `&str` input: identical input bytes
//! always produce a byte-identical [`Query`] (`Debug`/`PartialEq`-equal),
//! on every platform and call. No filesystem, network, clock, or
//! allocation-order dependency anywhere in this crate.

#![deny(unsafe_code)]

pub mod ast;
mod error;
pub mod eval;
mod parser;

pub use ast::{Bound, CmpOp, Expr, Matcher, Predicate, Query, Seg, SetJoin, Term};
pub use error::{Location, ParseError, ParseErrorKind};
pub use eval::{evaluate, EvalDiagnostic, FacetLookup, FacetSource, FacetType, MatchResult};
pub use parser::parse;

/// Returns this crate's semantic version, read from `Cargo.toml` at compile
/// time via `env!("CARGO_PKG_VERSION")`.
#[must_use]
pub fn version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_matches_cargo_toml() {
        assert_eq!(version(), "0.1.0");
    }
}
