---
name: "facetquery Language Specification (facetquery@1)"
description: "facetquery@1 grammar spec: boolean facet-query language syntax, operators, precedence, wildcards, ranges, escaping, exists/not-exists, parse-time vs eval-time diagnostics, full edge-case table -- conformance oracle for the facetquery crate parser"
id: "schema:facetquery:language-spec"
tags:
  - type:knowledge
  - topic:tooling
  - status:complete
  - privacy:public
  - owner:public
links: []
updated: 2026-07-12T00:00:00Z
---

# facetquery — boolean facet-query language (`facetquery@1`)

Canonical, versioned specification of the `facetquery` language: a small boolean query language over
named facets (`facet:value`) plus full-text terms. Frontmatter-agnostic — it defines syntax and
evaluation semantics against a generic facet source, not against any one document schema.

Normative together with `facetquery.ebnf` (same directory): the EBNF is the formal grammar, this
document is the prose companion (operators, precedence, escaping, type-gating, every edge case).
Read both — a case not covered by one is covered by the other.

**Reference implementation:** `rust/facetquery` (winnow-based parser + generic evaluator). Its test
suite includes a grammar-conformance test that asserts the crate's accepted/rejected inputs and
diagnostics agree with this spec and grammar. Any crate/spec disagreement found there is a **crate
defect**, not spec ambiguity — this document is the oracle.

## The parse-time / eval-time split

Every failure mode in the language is one of two classes, split by what's needed to detect it:

| Class | Detected from | Result | Query runs? |
|---|---|---|---|
| **Parse-time** | The query string alone (grammar) | Positioned `ParseError` — `{line, column, expected}` | No |
| **Eval-time** | Query string **+** the facet source (a facet's existence, a facet's type) | `EvalDiagnostic` (warning), attached to the result | Yes — offending predicate is a no-match, the rest of the query still evaluates |

A syntactically valid query with an eval-time problem (unknown facet, range on a string-typed facet)
still runs and returns a result; it never becomes a `ParseError`. A syntax violation always blocks
the run — it is never silently degraded to a warning.

## Query atoms

| Atom | Syntax | Meaning |
|---|---|---|
| Predicate | `facet:value` | Matches when `facet`'s value(s) satisfy `value`'s matcher (equality, wildcard, set, range, comparison, or exists) |
| Bareword | `value` (no `facet:` prefix) | Full-text term — matches the source's text field(s), not any one facet |
| Phrase | `"multi word value"` | Quoted — usable as a bareword full-text phrase, or as a facet's value when it contains spaces/reserved characters |

## Boolean operators

| Operator | Token(s) | Notes |
|---|---|---|
| AND | `AND` keyword, **or** implicit by whitespace adjacency | `facet:a AND facet:b` ≡ `facet:a facet:b` — same AST node either way |
| OR | `OR` keyword only | No implicit-OR — adjacency always means AND, never OR |
| NOT | `NOT` keyword (**canonical**), or `-` prefix (**documented alias**) | Prefix, unary; attaches to the predicate/term or the parenthesized group that follows. One negation node in the AST regardless of which token was written |
| Grouping | `( )` | Overrides precedence |

`AND`, `OR`, `NOT`, `TO` are reserved case-sensitively (exact uppercase) whenever they appear
unescaped and unquoted outside a phrase. To search for one of these words as literal text, quote it
(`"AND"`) or escape at least one of its characters (`\AND`) — either bypasses the reservation.

**`NOT` vs `-` — the one difference that matters:** `NOT` is a word operator and requires whitespace
before its operand (`NOT facet:x`); `-` attaches with no whitespace (`-facet:x`, `-(a OR b)`). Both
produce the identical negation node — pick either consistently, or mix them; the language does not
distinguish.

## Precedence (published)

**`NOT` > `AND` > `OR`, left-to-right; parentheses override.** Stated explicitly and unambiguously —
undocumented operator precedence in many facet-query implementations is exactly the gap this
publishes a fix for.

| Written | Parses as |
|---|---|
| `a OR b AND c` | `a OR (b AND c)` |
| `NOT a AND b` | `(NOT a) AND b` |
| `a b OR c` (implicit AND) | `(a AND b) OR c` |
| `a AND b OR c AND d` | `(a AND b) OR (c AND d)` |
| `-a OR b` | `(-a) OR b` |

## Set shorthand

`facet:(v1 OR v2)` and `facet:(v1 AND v2)` — a parenthesized value list scoped to one facet.

- One join per group: every join inside a single `facet:( ... )` must be the same keyword (all `OR`
  or all `AND`) — a set carries exactly one join, never a mix.
- To mix `AND`/`OR` across values on the same facet, nest ordinary predicates instead of a set group:
  `(facet:v1 OR facet:v2) AND facet:v3`.
- `facet:(v1 OR v2)` is equivalent in effect to `facet:v1 OR facet:v2`; the shorthand exists for
  readability, not for new semantics.

## Wildcards

`*` matches zero or more of any character; `?` matches exactly one of any character.

- Active **only outside double quotes**. Inside a quoted phrase, `*` and `?` are literal characters
  — quoting disables wildcard meaning entirely.
- Freely mixable with literal text in the same token: `ser*ce`, `?erver`, `a*b?c`.
- A bareword or predicate value that resolves to the single segment `*` (nothing else) is the
  exists/not-exists shape — see below.

## Escaping / quoting

| Context | Rule |
|---|---|
| Phrase | `"..."` — spaces and every otherwise-reserved character are literal inside; only `"` and `\` ever need `\` to appear literally inside a phrase |
| Escape character | `\` — makes the next character literal, identically inside and outside a phrase |
| Reserved outside a phrase | Must be quoted or escaped to use literally: whitespace, `"` `\` `:` `(` `)` `[` `]` `*` `?` `<` `>`, and — only as the leading character of a predicate/term/group — `-` |
| Reserved words | `AND` `OR` `NOT` `TO` (exact case, whole token) — quote the word or escape any one of its characters to search it literally |

A hyphen that is **not** the first character of a predicate/term/group is always literal — no
escaping needed (`facet:abc-123`). Only a **leading** unescaped `-` is the negation operator.

## Ranges and comparisons — type-gated

- Range: `facet:[min TO max]` — inclusive; either bound may be `*` for unbounded (`facet:[* TO 100]`,
  `facet:[2026-01-01 TO *]`).
- Comparison: `facet:>v`, `facet:>=v`, `facet:<v`, `facet:<=v`.
- Range/comparison operands are exact tokens (a quoted phrase or a bareword) — wildcards are not
  meaningful in a bound or comparison value; `*` in that position always means "unbounded", never
  a wildcard pattern.

**Eligibility is an eval-time property of the facet's TYPE, not a grammar rule.** The type comes from
the evaluator's facet source (e.g., a frontmatter adapter reading the merged schema profile's
per-namespace `type`) — the grammar accepts `facet:[..]`/`facet:>..` on any facet name; whether it's
*meaningful* is decided at evaluation:

| Facet type | Range / comparison eligible? |
|---|---|
| `date`, `numeric`, `date-interval` (ordered types) | Yes |
| `string` (default) | No — equality and wildcard matching only |

A range or comparison predicate targeting a `string`-typed facet is a `RangeOnNonOrdered` eval-time
diagnostic (warning); that predicate is a no-match, and the query still runs. `date-interval` is
ordered and never raises this diagnostic — see "Date intervals" below for its own matching rules.

## Date intervals

`date-interval` is an ordered facet type (additive as of this section — `facetquery@1` is unchanged;
no grammar or version bump). A stored value is a closed date range:

```
YYYY-MM-DD/YYYY-MM-DD
```

Single `/` separator, both endpoints inclusive, `start <= end`. Shape-checked only (reused from
`date`'s `YYYY-MM-DD` check — no calendar validation), compared lexically per endpoint (fixed-width
dates compare calendar-correctly lexically, exactly like the point `date` type). `/` is not reserved
punctuation in the grammar, so `start/end` parses as one ordinary literal token — no grammar change
is needed to write it as a facet value, range bound, or comparison operand.

A file matches a `date-interval` predicate iff **any** of its interval values matches:

| Matcher | Query example | Condition (stored interval `[s0, s1]`) |
|---|---|---|
| `Range{lo,hi}` | `period:[2026-04-01 TO 2026-06-30]` | Overlap: `s0 <= hi and lo <= s1` (`*` on either side is open/unbounded; `[* TO *]` matches every well-formed stored value) |
| `Term` = single date `D` | `period:2026-05-15` | `s0 <= D <= s1` |
| `Term` = interval `A/B` | `period:2026-04-01/2026-06-30` | Overlap against `[A, B]` |
| `Cmp >D` | `period:>2026-01-01` | `s1 > D` |
| `Cmp >=D` | `period:>=2026-01-01` | `s1 >= D` |
| `Cmp <D` | `period:<2026-01-01` | `s0 < D` |
| `Cmp <=D` | `period:<=2026-01-01` | `s0 <= D` |
| `Set{terms,join}` | `period:(2026-01-15 OR 2026-08-01)` | Per-member overlap, combined by `join` |
| `Exists` | `period:*` | Facet has >= 1 value (unchanged from every other type) |

Worked example: `period:>2026-01-01` matches a stored value `2025-12-01/2026-02-01` — the interval
started before the operand, but its end (`2026-02-01`) is past it, which is what `Cmp` against the
far endpoint captures.

**A `Term` on a `date-interval` facet is date/interval-parsed, not glob-matched** — deliberately
different from the point `date` type, whose `Term` matcher glob-matches the raw stored text. A
wildcard term (`period:2026-*`) is neither a bare date nor an `A/B` interval, so it fails to parse
into a window and is a no-match, never a partial/prefix hit.

**Malformed input is always a silent no-match, never a diagnostic or panic:**

- A malformed **stored** value — not `date/date` shaped, missing or extra `/`-separated parts,
  a non-date-shaped endpoint, or `start > end` — is a no-match for that value; no `EvalDiagnostic`
  is raised (same treatment as an unparseable point-`date` value today).
- A malformed **query operand** — a `Cmp`/`Range`-bound scalar that isn't a shape-valid date, or a
  `Term`/`Set` member that's neither a bare date nor an `A/B` interval — is a no-match; no new
  `ParseError` and no `EvalDiagnostic`.

## Exists / not-exists

- `facet:*` — **exists**: matches when the facet has at least one value on the document (any
  value, whatever it is).
- `NOT facet:*` / `-facet:*` — **not-exists**: matches when the facet has zero values — either
  the source's schema has no such facet at all, or the schema knows the facet but this document
  doesn't set it. Both are "no values", so both fail exists the same way; the eval-time
  distinction between them is `UnknownFacet` (schema doesn't know the name) vs. no diagnostic at
  all (schema knows it, this document just has none) — see `FacetLookup::Present`'s doc.

Not separate syntax. `facet:*` parses as an ordinary predicate whose value is the single wildcard
segment `*` — a ordinary `term`, matching any value the facet has. The reference AST recognizes a
term of exactly this shape (one segment, `*`, nothing else) and classifies it as `Matcher::Exists`
rather than a general `Matcher::Term`; `NOT`/`-` negate the same shape for not-exists. No grammar
production is dedicated to exists — see `facetquery.ebnf`'s note on `predicate_value`.

## Edge cases (every case defined)

The key gap this language closes versus a typical undocumented facet-query implementation: every
one of the following is a **defined**, specified outcome — never an implementation-dependent guess.

| Case | Class | Behavior |
|---|---|---|
| Empty query, or whitespace-only query | — | Valid. Matches all documents (no predicate to fail). |
| Bare `*` as the entire query | — | Valid. A wildcard term matching any text — matches all documents. |
| Negation-only query (`NOT facet:x`, `-facet:x`, `-(a OR b)` alone) | — | Valid. Matches every document that does **not** satisfy the negated predicate/group. |
| Unbalanced parentheses | Parse-time | `ParseError`, positioned (line/column) + expected token. Query does not run. |
| Trailing operator (query ends in `AND`, `OR`, `NOT`, or an incomplete `TO`) | Parse-time | `ParseError`, positioned. Query does not run. |
| Unexpected/stray token (stray `)`, two operators back to back, adjacent atoms with no separating whitespace, e.g. `facet:a(b)`) | Parse-time | `ParseError`, positioned. Query does not run. |
| Unterminated quoted phrase | Parse-time | `ParseError`, positioned. Query does not run. |
| Malformed range (missing `TO`, missing `[`/`]`) | Parse-time | `ParseError`, positioned. Query does not run. |
| `facet:*` | — | **Exists** — matches when the facet has at least one value (any value). |
| `NOT facet:*` / `-facet:*` | — | **Not-exists** — matches when the facet has zero values (schema-unknown, or schema-known but unset on this document). |
| Wildcard (`*`/`?`) inside a quoted phrase | — | Literal character — no wildcard expansion. |
| Unescaped leading `-` before a value (e.g. searching for the literal text `-5`) | — | Parses as **negation** of the term that follows, not a literal leading dash. Quote (`"-5"`) or escape (`\-5`) to get the literal value. |
| Mixed `AND`/`OR` inside one `facet:( ... )` set group | Parse-time | `ParseError` — a set group carries exactly one join; nest predicates to mix joins. |
| Range bound `*` on one or both sides (`facet:[* TO 100]`) | — | Valid — that side is unbounded. |
| Reference to a facet the evaluator's facet source doesn't know | Eval-time | `UnknownFacet` diagnostic (warning). That predicate is a no-match; query still runs. |
| Range or comparison against a `string`-typed facet | Eval-time | `RangeOnNonOrdered` diagnostic (warning). That predicate is a no-match; query still runs. |
| Reserved word (`AND`/`OR`/`NOT`/`TO`) written unescaped, unquoted, standing alone where a term/bareword is expected | Parse-time | Not treated as a bareword — `ParseError` (unexpected token / incomplete operator). Quote or escape to search the literal word. |
| Malformed `date-interval` stored value (not `date/date` shaped, wrong endpoint count, `start > end`, non-date-shaped endpoint) | Eval-time | Silent no-match for that value — no diagnostic (same treatment as an unparseable point-`date` value). |
| Malformed `date-interval` query operand (a `Cmp`/`Range`-bound scalar or `Term`/`Set` member that isn't a shape-valid date or `A/B` interval, e.g. a wildcard) | Eval-time | No-match — no new `ParseError`, no diagnostic. |

## Conformance

- `facetquery.ebnf` (this directory) is the normative grammar for `facetquery@1`; this document is
  its normative prose companion. Together they fully define the language — syntax, precedence,
  escaping, type-gating, and every edge case above.
- `rust/facetquery` is the reference implementation: a winnow-based parser producing the public AST
  (`Query`/`Expr`/`Predicate`/`Matcher`) and a generic evaluator over any `FacetSource`. Its
  conformance test suite asserts crate ⟺ spec/grammar agreement — treat a divergence as a crate bug.
- **Versioning:** any change that alters accepted syntax or a defined behavior above bumps the
  version tag (`facetquery@2`, ...). This document and the paired grammar describe `facetquery@1`
  only, throughout. `date-interval` (see "Date intervals" above) is additive under `facetquery@1`:
  it introduces no new syntax (`/` was already unreserved punctuation) and changes no existing
  type's behavior, so it does not bump the version tag.
