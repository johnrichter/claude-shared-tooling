// Package webfetch is a deterministic fetch-parse-filter pipeline for three
// web sources — XML sitemaps, per-page HTML metadata, and RSS/Atom feeds —
// with no LLM in the loop. Every parser is fail-open on partial/malformed
// input (it returns whatever it could recover, never silently the zero
// value) and never invents a value: a field the source doesn't carry comes
// back blank and named in the result's Missing list, for the caller to act
// on explicitly rather than trust a fabricated stand-in.
//
// Sitemap dedups entries on their canonical URL — never on lastmod, which a
// publisher can rewrite for the same URL without the identity of the page
// changing. ArticleMeta scrapes exactly what a page's own <title>/<meta>
// tags say, verbatim. Feed classifies each entry (release vs. general post)
// from its own content, not from guesswork about the source.
//
// A Fetcher wraps the HTTP access shared by all three: every request runs
// under a caller-supplied timeout, since an unbounded fetch of a third-party
// URL is not an acceptable failure mode for a pipeline meant to run
// unattended.
package webfetch
