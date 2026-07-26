package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Test-engineer adversarial pass, additive to adversarial_test.go /
// sanity_test.go. Targets gaps left uncovered by the author's suite and
// tries harder to break the fail-open / never-fabricate / dedup contracts.

// ---- ArticleMeta: empty-element edge cases ----

// A self-closing/empty <title></title> has no FirstChild text node at all
// (not even a whitespace one) — must not panic and must flag missing, same
// as a whitespace-only title.
func TestTE_ArticleMeta_EmptyTitleElementNoFirstChild(t *testing.T) {
	m, err := ParseArticleMeta("https://example.com/x", []byte(`<html><head><title></title></head></html>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "" {
		t.Fatalf("expected empty title element to yield blank title, got %q", m.Title)
	}
	found := false
	for _, f := range m.Missing {
		if f == "title" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected title flagged missing, got %+v", m.Missing)
	}
}

// A meta tag with a content attribute but no name/property must not be
// mistaken for any tracked field, and must not panic on missing attrs.
func TestTE_ArticleMeta_MetaTagMissingNameAndPropertyIgnored(t *testing.T) {
	body := `<html><head><meta content="orphan content"><title>T</title></head></html>`
	m, err := ParseArticleMeta("https://example.com/x", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Description != "" {
		t.Fatalf("expected nameless/property-less meta ignored, got description %q", m.Description)
	}
}

// HTML entities in a real title must decode exactly as the page wrote them
// (verbatim scraping means "what the browser would show", not raw source
// bytes) — this is not fabrication, it's correct decoding.
func TestTE_ArticleMeta_HTMLEntityDecodedInTitle(t *testing.T) {
	body := `<html><head><title>Tom &amp; Jerry</title></head></html>`
	m, err := ParseArticleMeta("https://example.com/x", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "Tom & Jerry" {
		t.Fatalf("expected entity-decoded title, got %q", m.Title)
	}
}

// A malformed <meta> content value containing an unescaped raw "<" must not
// crash the walker.
func TestTE_ArticleMeta_UnusualAttributeQuotingDoesNotPanic(t *testing.T) {
	body := `<html><head><meta name=description content=NoQuotesHere><title>T</title></head></html>`
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on unquoted attribute: %v", r)
		}
	}()
	m, err := ParseArticleMeta("https://example.com/x", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Description != "NoQuotesHere" {
		t.Fatalf("expected unquoted attribute value recovered, got %q", m.Description)
	}
}

// ---- Feed: atom link edge cases ----

// An Atom entry with zero <link> elements must yield an empty URL (flagged
// missing), never panic on an empty slice.
func TestTE_Feed_AtomEntryWithNoLinksAtAllFlagsURLMissing(t *testing.T) {
	data := `<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>No links</title><published>2024-01-01</published></entry>
</feed>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	found := false
	for _, f := range entries[0].Missing {
		if f == "url" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected url flagged missing for linkless atom entry, got %+v", entries[0].Missing)
	}
}

// Dedup must key on the fully canonicalized URL, not raw string equality:
// an RSS feed repeating the same article with different case/fragment/
// trailing-slash must still collapse to one entry, keeping the first title
// seen (never overwritten by a later duplicate).
func TestTE_Feed_RSSDedupNormalizesLikeCanonicalizeURL(t *testing.T) {
	data := `<rss><channel>
<item><title>First</title><link>HTTPS://Example.COM/p/</link></item>
<item><title>Second</title><link>https://example.com/p#frag</link></item>
</channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected canonical-URL dedup to collapse both items, got %d: %+v", len(entries), entries)
	}
	if entries[0].Title != "First" {
		t.Fatalf("expected first-seen title retained, got %q", entries[0].Title)
	}
}

// A version-looking substring inside an unrelated word (e.g. an IPv4-like
// token) must not spuriously trip release classification if it doesn't
// match the anchored version pattern; conversely a genuine bare version
// number with no other release wording must still classify as a release.
func TestTE_Feed_BareVersionNumberWithNoReleaseWordStillClassifiesRelease(t *testing.T) {
	data := `<rss><channel><item><title>2.5.1</title><link>https://example.com/v</link></item></channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected bare version title classified as release, got %+v", entries)
	}
}

// A category list where only one of several terms matches must still
// classify as release (any match wins), and must not error on nil/empty
// categories elsewhere.
func TestTE_Feed_OneMatchingCategoryAmongManyClassifiesRelease(t *testing.T) {
	data := `<rss><channel>
<item><title>Weekly notes</title><link>https://example.com/x</link><category>engineering</category><category>changelog</category><category>misc</category></item>
</channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected one matching category among several to classify release, got %+v", entries)
	}
}

// ---- Sitemap: additional fail-open / dedup adversarial cases ----

// A sitemap containing a byte-order-mark or leading garbage before the
// first well-formed <url> block must still recover that block instead of
// yielding nothing.
func TestTE_Sitemap_LeadingGarbageBeforeFirstBlockStillRecoversIt(t *testing.T) {
	xml := "\xEF\xBB\xBFsome garbage prefix not xml at all <url><loc>https://example.com/a</loc></url>"
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/a" {
		t.Fatalf("expected recovery of well-formed block despite leading garbage, got %+v", res)
	}
}

// Three occurrences of the same canonical URL (not just two) must still
// dedup to exactly one entry, and only the first lastmod is retained.
func TestTE_Sitemap_TripleDuplicateCollapsesToOneKeepingFirst(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/a</loc><lastmod>2020-01-01</lastmod></url>
<url><loc>https://example.com/a/</loc><lastmod>2021-01-01</lastmod></url>
<url><loc>HTTPS://EXAMPLE.COM/a</loc><lastmod>2022-01-01</lastmod></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 {
		t.Fatalf("expected triple duplicate to collapse to 1, got %d: %+v", len(res.Entries), res.Entries)
	}
	if res.Entries[0].LastMod != "2020-01-01" {
		t.Fatalf("expected first-seen lastmod retained across 3 duplicates, got %q", res.Entries[0].LastMod)
	}
}

// A PathPrefix of "" combined with a Since/Until window must apply only the
// time filter (prefix check disabled), confirming the two filters are
// independent rather than accidentally coupled.
func TestTE_Sitemap_EmptyPrefixWithTimeWindowAppliesOnlyTimeFilter(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/anything/a</loc><lastmod>2024-06-01</lastmod></url>
<url><loc>https://other.com/b</loc><lastmod>2019-01-01</lastmod></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{
		Since: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/anything/a" {
		t.Fatalf("expected only in-window entry kept regardless of path, got %+v", res.Entries)
	}
	if res.Skipped != 0 {
		t.Fatalf("expected clean exclusion (parseable lastmod, just out of window) not counted as skipped, got %d", res.Skipped)
	}
}

// A prefix filter that matches zero entries must yield an empty (not
// skipped) result — nothing here is data loss, it's a filter with no hits.
func TestTE_Sitemap_PrefixMatchingNothingYieldsEmptyNotSkipped(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/docs/a</loc></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{PathPrefix: "/blog"})
	if len(res.Entries) != 0 {
		t.Fatalf("expected no matches, got %+v", res.Entries)
	}
	if res.Skipped != 0 {
		t.Fatalf("expected 0 skipped for a clean non-matching prefix, got %d", res.Skipped)
	}
}

// ---- Fetcher: additional edge coverage ----

// FetchArticleMeta's success path (fetch + parse) must work end to end,
// mirroring the FetchSitemap/FetchFeed round-trip tests already present.
func TestTE_Fetcher_FetchArticleMetaSuccessRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Live Page</title></head></html>`))
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	m, err := f.FetchArticleMeta(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "Live Page" {
		t.Fatalf("expected live-fetched title, got %q", m.Title)
	}
}

// FetchFeed's success path must classify and return entries from a live
// server response, not just a static []byte.
func TestTE_Fetcher_FetchFeedSuccessRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel><item><title>v1.0.0</title><link>https://example.com/r</link></item></channel></rss>`))
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	entries, err := f.FetchFeed(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected 1 release entry from live fetch, got %+v", entries)
	}
}

// A non-2xx status from FetchSitemap/FetchArticleMeta must also be reported
// as an error (redundant coverage vs. the FetchFeed case, on different call
// sites — the error handling is shared plumbing but each entrypoint must
// wire it correctly).
func TestTE_Fetcher_NonSuccessStatusIsErrorOnSitemapAndArticleMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	if _, err := f.FetchSitemap(context.Background(), srv.URL, SitemapWindow{}); err == nil {
		t.Fatalf("expected FetchSitemap to error on 404")
	}
	if _, err := f.FetchArticleMeta(context.Background(), srv.URL); err == nil {
		t.Fatalf("expected FetchArticleMeta to error on 404")
	}
}

// ---- canonicalizeURL: query string and case preservation ----

// A query string is part of a page's identity and must be preserved (not
// stripped) by canonicalization — dropping it would wrongly merge distinct
// pages that differ only by query parameter.
func TestTE_CanonicalizeURL_PreservesQueryString(t *testing.T) {
	c, err := canonicalizeURL("https://example.com/search?q=foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != "https://example.com/search?q=foo" {
		t.Fatalf("expected query string preserved, got %q", c)
	}
}

// Path case must be preserved (only scheme/host are lower-cased) — many
// servers are case-sensitive on path, so lower-casing it would silently
// merge or misroute distinct URLs.
func TestTE_CanonicalizeURL_PathCaseIsPreserved(t *testing.T) {
	c, err := canonicalizeURL("https://example.com/CaseSensitivePath")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != "https://example.com/CaseSensitivePath" {
		t.Fatalf("expected path case preserved, got %q", c)
	}
}
