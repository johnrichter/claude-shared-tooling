# Dependency spike — tolerant HTML parser for article metadata scraping

## Decision

**Pin `golang.org/x/net` at `v0.57.0`, using its `html` subpackage.**

```
require golang.org/x/net v0.57.0
```

**Application site:** `go/webfetch` — `ArticleMeta` scrapes `<title>` and `<meta>` tags
(description, author, published time) from live, third-party HTML that is never guaranteed
well-formed.

## Problem

Go's standard library has no HTML parser — `encoding/xml` requires well-formed input and
chokes on the unclosed tags, unescaped entities, and non-XML voids (`<meta ...>` with no
self-closing slash) that real-world HTML routinely contains. Extracting `<title>`/`<meta>`
content with hand-rolled regexes is the well-known fragile alternative: it breaks on
attribute-order variation, nested quotes, and multi-line tags, and silently mis-extracts
rather than failing loud — the opposite of this task's never-fabricate requirement.

## Candidates scored

| Library | Durability | Active maintenance | License fit | Adoption | Verdict |
| --- | --- | --- | --- | --- | --- |
| **`golang.org/x/net/html`** | ✅ implements the WHATWG HTML5 parsing algorithm (tokenizer + tree construction), maintained under the official `golang.org/x` umbrella alongside the standard library itself | ✅ latest release `v0.57.0` (2026-07-08); source repo pushed to within the last week | ✅ BSD-3-Clause — already relied on elsewhere in this repo (`x/sys`, `x/text`) under the same Go-Authors text | ✅ 3,026 GitHub stars; the de facto standard Go HTML parser, imported by most Go web-scraping libraries (e.g. `goquery`) | **Pinned** |
| hand-rolled regex extraction | ❌ no tokenizer, no tag-nesting or entity-decoding awareness — breaks on real-world attribute-order/quoting variation | n/a (not a dependency) | n/a | n/a | Rejected — silently mis-extracts instead of failing loud; violates never-fabricate |
| `PuerkitoBio/goquery` | ✅ mature jQuery-style API, but it is itself built on `x/net/html` — adding it pulls in the same parser plus a heavier selector API this task doesn't need (single-pass tag/attribute lookup, no CSS selectors) | ✅ actively maintained | ✅ BSD-3-Clause | ✅ widely adopted | Rejected — unnecessary layer over the parser this task actually needs |

## Why `x/net/html` over the field

- Only candidate that is a parser (not a wrapper around one) implementing the actual HTML5
  tokenization/tree-construction spec, so malformed real-world markup degrades gracefully
  instead of erroring or mis-matching like a regex would.
- Official `golang.org/x` module — same maintainer and license as `x/sys`/`x/text`, already
  pinned in this repo, so no new license family or maintainer-trust question is introduced.
- `goquery` would add a CSS-selector layer this task doesn't use; walking the token stream
  directly for `<title>`/`<meta name|property>` keeps the dependency surface to the parser
  alone.

## License clearance

- **License:** BSD-3-Clause (Go Authors) — identical text already reproduced in
  `THIRD-PARTY-LICENSES.md` for `x/sys`/`x/text`.
- Obligation: retain copyright/permission notice in any distributed binary. Satisfied by
  extending the existing `x/sys`/`x/text` BSD-3-Clause section to also cover `x/net`.

## Consuming this decision

- `go/webfetch/go.mod` requires `golang.org/x/net v0.57.0`.
- `LICENSE-3rdparty.csv` gets an `x/net` row; `THIRD-PARTY-LICENSES.md`'s shared
  `golang.org/x/sys, golang.org/x/text` BSD-3-Clause section is extended to name `x/net` too,
  since all three share the same copyright holder and license text.
