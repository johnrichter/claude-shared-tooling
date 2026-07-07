# ai-shared-lib-public

Sensitivity: **public** · Owner: **public** · Kind: **shared library**

Public shared code **and schema/definition specs** for AI-agent workspaces. Everything here is built only from publicly-accessible resources, embeds no private knowledge, and reaches no private data at runtime. Consumed by sibling repos via path dependency in dev and git-tag dependency when shipped.

## What lives here

- **`ai_shared_lib_public/`** — Python tools, stdlib-only in fact today (see [Dependency policy](#dependency-policy)).
- **`schemas/`** — generalized, non-proprietary schema and format definitions others can adopt (see `schemas/README.md`).

**Routing rule (which shared lib?):** Org-specific or otherwise proprietary → the private shared lib. Personal → not a shared lib at all. Everything else non-proprietary and non-personal → here.

## Tools

| Command | Module | Purpose |
| --- | --- | --- |
| `jr-resign-commits` | `resign_commits` | Conflict-proof re-signing of unsigned commits on a git ref via `git commit-tree` (reuses each exact tree; preserves merge topology; dry-run by default). |
| `jr-sitemap-parser` | `sitemap_parser` | Fetch, parse, window-filter, and prefix-filter any XML sitemap (`urlset` or one-level `sitemapindex`); fail-open to `[]`. |
| `jr-article-meta` | `article_meta` | Deterministically extract `{title, published, excerpt}` from a page's structured metadata (no LLM; verbatim-or-null). |

Each module is both an importable API and a CLI (`python -m ai_shared_lib_public.<module> …`).

## Install (dev)

```
uv venv && uv pip install -e .
python -m unittest discover -s tests -p "test_*.py"
```

Requires Python ≥ 3.10. Stdlib-only in fact today — no install of third-party packages needed.

## Dependency policy

Stdlib is **preferred** for portability (no network install to import or run), but it is a preference, not a hard bar. A third-party dependency is acceptable when it is **justified** — it avoids reinventing the wheel or is the best way to get the functionality — and **vendored via the standard mechanism** (Python venv), subject to **OSS-license clearance** per the org policy. Every module here is stdlib-only in fact today; `dependencies` stays empty until a justified dependency actually lands.

## Privacy guardrail

`scripts/check_privacy.py` fails on any private-owner/privacy marker or secret leaking into this public repo, and requires any doc frontmatter to carry `privacy:public` + `owner:public`. Two layers:

- **Local pre-commit hook** — `git config core.hooksPath .githooks` (convenience; bypassable).
- **CI required check** — `.gitlab-ci.yml` runs the guardrail + the test suite (the authority).

## Versioning

Semver git tags (`vX.Y.Z`); consumers pin a tag. Bump on every release so updates propagate.

### Changelog

- **v0.2.0** — Dependency policy reframed from "dependency-free / Tier 0" to **stdlib-preferred** (justified vendored deps permitted, subject to OSS-license clearance). No code change: `dependencies` stays empty, every module remains stdlib-only in fact. Docs-only contract clarification.
- **v0.1.0** — Initial release: `resign_commits`, `sitemap_parser`, `article_meta` tools + schema home + privacy guardrail.
