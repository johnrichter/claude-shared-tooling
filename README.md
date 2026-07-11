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

Stdlib stays correct for what it covers well — a one-line string op, a built-in already fit for purpose. For a **non-trivial common capability** (CLI/argument parsing, YAML, JSON, regex, globbing, math, HTTP, date/time, and the like), **prefer a well-maintained, robust, trusted library over hand-rolling it** — never reinvent the wheel when a trusted, actively-maintained library exists. This reverses the earlier stdlib-preferred posture: fewer dependencies is not a goal in itself, robustness and correctness are.

Every material dependency choice runs a **robustness-first vetting** (maintenance cadence, adoption, security-advisory history, license fit) before adoption, clears **OSS-license** per the org policy, is **pinned** to an exact version, and is **recorded in `LICENSE-3rdparty.csv`** in the same change. Among viable candidates, prefer **official / first-party / actively-maintained** libraries; flag supply-chain risk and get sign-off before introducing untrusted code.

Hand-rolling a common capability is the narrow exception — only when no trusted library fits (vetting bar, license, or a hard build-fit constraint) — and requires flagging the rejected candidates, stating the rationale, and getting sign-off.

Every module here is stdlib-only in fact today; `dependencies` stays empty until a vetted dependency actually lands.

## Privacy guardrail

`scripts/check_privacy.py` fails on any private-owner/privacy marker or secret leaking into this public repo, and requires any doc frontmatter to carry `privacy:public` + `owner:public`. Two layers:

- **Local pre-commit hook** — `git config core.hooksPath .githooks` (convenience; bypassable).
- **CI required check** — `.gitlab-ci.yml` runs the guardrail + the test suite (the authority).

## Versioning

Semver git tags (`vX.Y.Z`); consumers pin a tag. Bump on every release so updates propagate.

### Changelog

- **Unreleased** — Dependency policy reversed from stdlib-preferred to prefer-a-trusted-library-for-non-trivial-common-capabilities (CLI/YAML/JSON/regex/glob/math, etc.), vetted robustness-first, license-cleared, pinned, and recorded in `LICENSE-3rdparty.csv`. Docs-only: no code change, `dependencies` stays empty, every module remains stdlib-only in fact.
- **v0.2.0** — Dependency policy reframed from "dependency-free / Tier 0" to **stdlib-preferred** (justified vendored deps permitted, subject to OSS-license clearance). No code change: `dependencies` stays empty, every module remains stdlib-only in fact. Docs-only contract clarification.
- **v0.1.0** — Initial release: `resign_commits`, `sitemap_parser`, `article_meta` tools + schema home + privacy guardrail.
