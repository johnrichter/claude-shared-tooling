package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- Sitemap: dedup on canonical URL, never mutable lastmod ----

// TestSitemap_DedupKeepsFirstOccurrenceRegardlessOfLastMod verifies a later
// lastmod for the same URL never displaces the first-seen entry.
func TestSitemap_DedupKeepsFirstOccurrenceRegardlessOfLastMod(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/a</loc><lastmod>2024-01-01</lastmod></url>
<url><loc>https://example.com/a</loc><lastmod>2099-12-31</lastmod></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(res.Entries))
	}
	if res.Entries[0].LastMod != "2024-01-01" {
		t.Fatalf("expected first-seen lastmod retained (2024-01-01), got %q", res.Entries[0].LastMod)
	}
}

// TestSitemap_DedupNormalizesSchemeHostTrailingSlashFragment verifies scheme
// case, host case, trailing slash, and fragment variants of the same URL all
// collapse to one canonical dedup key.
func TestSitemap_DedupNormalizesSchemeHostTrailingSlashFragment(t *testing.T) {
	xml := `<urlset>
<url><loc>HTTPS://Example.COM/a/</loc></url>
<url><loc>https://example.com/a#section</loc></url>
<url><loc>https://example.com/a</loc></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 {
		t.Fatalf("expected all 3 variants to dedup to 1 entry, got %d: %+v", len(res.Entries), res.Entries)
	}
}

// TestSitemap_DedupDoesNotMergeDifferentPaths verifies dedup never
// over-collapses distinct URLs.
func TestSitemap_DedupDoesNotMergeDifferentPaths(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/a</loc></url>
<url><loc>https://example.com/b</loc></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 2 {
		t.Fatalf("expected 2 distinct entries, got %d", len(res.Entries))
	}
}

// ---- Sitemap: fail-open on partial/malformed input ----

// TestSitemap_EmptyInputYieldsEmptyNonError verifies an empty document
// produces an empty, non-error result.
func TestSitemap_EmptyInputYieldsEmptyNonError(t *testing.T) {
	res := ParseSitemap([]byte(""), SitemapWindow{})
	if len(res.Entries) != 0 || res.Skipped != 0 {
		t.Fatalf("expected empty result on empty input, got %+v", res)
	}
}

// TestSitemap_NotASitemapYieldsEmptyNonError verifies a well-formed document
// that simply isn't a sitemap yields no entries, not an error.
func TestSitemap_NotASitemapYieldsEmptyNonError(t *testing.T) {
	res := ParseSitemap([]byte("<html><body>not a sitemap</body></html>"), SitemapWindow{})
	if len(res.Entries) != 0 {
		t.Fatalf("expected 0 entries from non-sitemap document, got %+v", res)
	}
}

// TestSitemap_MalformedMiddleBlockDoesNotAbortLaterEntries verifies one
// broken <url> block only drops that block, never entries after it.
func TestSitemap_MalformedMiddleBlockDoesNotAbortLaterEntries(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/a</loc></url>
<url><loc>https://example.com/b<broken></url>
<url><loc>https://example.com/c</loc></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	got := map[string]bool{}
	for _, e := range res.Entries {
		got[e.URL] = true
	}
	if !got["https://example.com/a"] || !got["https://example.com/c"] {
		t.Fatalf("expected a and c to survive a malformed middle block, got %+v (skipped=%d)", res.Entries, res.Skipped)
	}
}

// TestSitemap_UnclosedLocInBlockIsSkippedNotFatal verifies a block whose
// <loc> is itself unclosed is dropped without aborting the document.
func TestSitemap_UnclosedLocInBlockIsSkippedNotFatal(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/a</url><url><loc>https://example.com/b</loc></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	found := false
	for _, e := range res.Entries {
		if e.URL == "https://example.com/b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected recoverable entry b to survive malformed entry a, got %+v", res.Entries)
	}
}

// TestSitemap_TruncatedTrailingBlockNeverPanics verifies a battery of
// truncated/degenerate inputs never panics the block scanner.
func TestSitemap_TruncatedTrailingBlockNeverPanics(t *testing.T) {
	inputs := []string{
		`<urlset><url>`,
		`<urlset><url><loc>`,
		`<url`,
		``,
		`<url></url><url>`,
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseSitemap panicked on %q: %v", in, r)
				}
			}()
			_ = ParseSitemap([]byte(in), SitemapWindow{})
		}()
	}
}

// TestSitemap_UnparsableLocIsSkippedNotFatal verifies an entry whose <loc>
// fails URL parsing is skipped and counted, while a later valid entry
// survives.
func TestSitemap_UnparsableLocIsSkippedNotFatal(t *testing.T) {
	xml := `<urlset>
<url><loc>://not-a-valid-url</loc></url>
<url><loc>https://example.com/ok</loc></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/ok" {
		t.Fatalf("expected only the valid entry to survive, got %+v", res.Entries)
	}
	if res.Skipped < 1 {
		t.Fatalf("expected skip counted for unparsable loc, got skipped=%d", res.Skipped)
	}
}

// TestSitemap_RelativeLocWithNoHostIsSkipped verifies a <loc> with no
// scheme/host (no stable identity to dedup on) is dropped and counted.
func TestSitemap_RelativeLocWithNoHostIsSkipped(t *testing.T) {
	xml := `<urlset><url><loc>/relative/path</loc></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 0 {
		t.Fatalf("expected relative loc (no scheme/host) to be dropped, got %+v", res.Entries)
	}
	if res.Skipped != 1 {
		t.Fatalf("expected skip=1, got %d", res.Skipped)
	}
}

// ---- Sitemap: window/prefix filter ----

// TestSitemap_PathPrefixFilter verifies PathPrefix keeps only matching URLs.
func TestSitemap_PathPrefixFilter(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/blog/a</loc></url>
<url><loc>https://example.com/docs/b</loc></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{PathPrefix: "/blog"})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/blog/a" {
		t.Fatalf("expected only /blog prefix match, got %+v", res.Entries)
	}
}

// TestSitemap_TimeWindowFilterSinceUntil verifies Since/Until bound lastmod
// on both ends.
func TestSitemap_TimeWindowFilterSinceUntil(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/a</loc><lastmod>2023-01-01</lastmod></url>
<url><loc>https://example.com/b</loc><lastmod>2024-06-15</lastmod></url>
<url><loc>https://example.com/c</loc><lastmod>2025-01-01</lastmod></url>
</urlset>`
	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	res := ParseSitemap([]byte(xml), SitemapWindow{Since: since, Until: until})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/b" {
		t.Fatalf("expected only b within window, got %+v", res.Entries)
	}
}

// TestSitemap_TimeWindowExcludesEntryWithUnparsableLastMod verifies a window
// filter drops (rather than keeps by default) an entry whose lastmod it
// cannot parse.
func TestSitemap_TimeWindowExcludesEntryWithUnparsableLastMod(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/a</loc><lastmod>not-a-date</lastmod></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{Since: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)})
	if len(res.Entries) != 0 || res.Skipped != 1 {
		t.Fatalf("expected entry with unparsable lastmod dropped under window, got entries=%d skipped=%d", len(res.Entries), res.Skipped)
	}
}

// TestSitemap_ZeroWindowSelectsEverything verifies the zero-value
// SitemapWindow imposes no filtering.
func TestSitemap_ZeroWindowSelectsEverything(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/a</loc></url><url><loc>https://example.com/b</loc></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 2 {
		t.Fatalf("expected zero window to select all entries, got %d", len(res.Entries))
	}
}

// TestSitemap_CleanFilterExclusionsDoNotInflateSkipped verifies a well-formed
// entry excluded by a prefix or a parseable out-of-window lastmod is NOT
// counted as Skipped — Skipped signals data loss, not deliberate filtering.
func TestSitemap_CleanFilterExclusionsDoNotInflateSkipped(t *testing.T) {
	xml := `<urlset>
<url><loc>https://example.com/blog/a</loc><lastmod>2024-06-15</lastmod></url>
<url><loc>https://example.com/docs/b</loc><lastmod>2024-06-15</lastmod></url>
<url><loc>https://example.com/blog/c</loc><lastmod>2020-01-01</lastmod></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{
		PathPrefix: "/blog",
		Since:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if len(res.Entries) != 1 || res.Entries[0].URL != "https://example.com/blog/a" {
		t.Fatalf("expected only in-window /blog entry, got %+v", res.Entries)
	}
	if res.Skipped != 0 {
		t.Fatalf("clean filter exclusions must not inflate Skipped, got skipped=%d", res.Skipped)
	}
}

// ---- ArticleMeta: verbatim, never fabricate ----

// TestArticleMeta_OGPreferredOverGenericTags verifies og:title/og:description
// take precedence over generic <title>/<meta name=description> when both
// are present.
func TestArticleMeta_OGPreferredOverGenericTags(t *testing.T) {
	body := `<html><head>
<title>Generic Title</title>
<meta property="og:title" content="OG Title">
<meta name="description" content="Generic Desc">
<meta property="og:description" content="OG Desc">
</head></html>`
	m, err := ParseArticleMeta("https://example.com/x", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "OG Title" {
		t.Fatalf("expected og:title preferred, got %q", m.Title)
	}
	if m.Description != "OG Desc" {
		t.Fatalf("expected og:description preferred, got %q", m.Description)
	}
}

// TestArticleMeta_AllFieldsMissingOnEmptyDoc verifies a document with no
// usable tags leaves every field blank and flags all four as missing.
func TestArticleMeta_AllFieldsMissingOnEmptyDoc(t *testing.T) {
	m, err := ParseArticleMeta("https://example.com/empty", []byte(`<html><head></head><body></body></html>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "" || m.Description != "" || m.Author != "" || m.PublishedAt != "" {
		t.Fatalf("expected all fields blank, got %+v", m)
	}
	if len(m.Missing) != 4 {
		t.Fatalf("expected 4 missing fields, got %v", m.Missing)
	}
}

// TestArticleMeta_NeverFabricatesFromURLOrSurroundingContent verifies a page
// with rich body text but no title/meta tags never has a title/description
// synthesized from that content.
func TestArticleMeta_NeverFabricatesFromURLOrSurroundingContent(t *testing.T) {
	body := `<html><body><h1>Big Visible Headline</h1><p>Lots of readable prose here about the article subject matter.</p></body></html>`
	m, err := ParseArticleMeta("https://example.com/article-about-something", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "" {
		t.Fatalf("must not fabricate title from h1/body content, got %q", m.Title)
	}
	if m.Description != "" {
		t.Fatalf("must not fabricate description from body text, got %q", m.Description)
	}
}

// TestArticleMeta_MalformedHTMLStillTolerated verifies a page with a
// non-self-closing meta void tag and no closing body/html tags still yields
// its real title and description.
func TestArticleMeta_MalformedHTMLStillTolerated(t *testing.T) {
	body := `<html><head><title>Real Title</title><meta name="description" content="Desc here"><body><p>text`
	m, err := ParseArticleMeta("https://example.com/malformed", []byte(body))
	if err != nil {
		t.Fatalf("expected html.Parse to tolerate malformed markup, got error: %v", err)
	}
	if m.Title != "Real Title" {
		t.Fatalf("expected title recovered despite malformed markup, got %q", m.Title)
	}
	if m.Description != "Desc here" {
		t.Fatalf("expected description recovered despite malformed markup, got %q", m.Description)
	}
}

// TestArticleMeta_UnclosedTitleSwallowsFollowingMarkupAsText verifies
// ArticleMeta preserves HTML5's RCDATA rule for <title>: an unclosed title
// swallows the rest of the document as literal text (exactly as a browser
// would render it), rather than trying to still recover a meta tag buried
// inside it.
func TestArticleMeta_UnclosedTitleSwallowsFollowingMarkupAsText(t *testing.T) {
	body := `<html><head><title>Unclosed Title<meta name="description" content="Desc here"><body><p>text`
	m, err := ParseArticleMeta("https://example.com/unclosed-title", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(m.Title, "Unclosed Title") {
		t.Fatalf("expected RCDATA text content in title, got %q", m.Title)
	}
	if m.Description != "" {
		t.Fatalf("expected description NOT extracted (swallowed into title RCDATA per HTML5), got %q", m.Description)
	}
}

// TestArticleMeta_FirstOccurrenceWinsOnDuplicateTags verifies a duplicate
// <title> never overwrites the first document-order occurrence.
func TestArticleMeta_FirstOccurrenceWinsOnDuplicateTags(t *testing.T) {
	body := `<html><head>
<title>First Title</title>
<title>Second Title</title>
</head></html>`
	m, err := ParseArticleMeta("https://example.com/dup", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "First Title" {
		t.Fatalf("expected first document-order title kept, got %q", m.Title)
	}
}

// TestArticleMeta_WhitespaceOnlyTitleTreatedAsMissingAfterTrim verifies a
// whitespace-only <title> trims to blank and is flagged missing, not kept as
// a non-empty-but-meaningless value.
func TestArticleMeta_WhitespaceOnlyTitleTreatedAsMissingAfterTrim(t *testing.T) {
	body := `<html><head><title>   </title></head></html>`
	m, err := ParseArticleMeta("https://example.com/blank-title", []byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Title != "" {
		t.Fatalf("expected whitespace-only title trimmed to blank, got %q", m.Title)
	}
	found := false
	for _, f := range m.Missing {
		if f == "title" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected title flagged missing when only whitespace present, got %+v", m.Missing)
	}
}

// ---- Feed: classify + dedup + blank/flag ----

// TestFeed_AtomEntriesClassifyAndDedup verifies Atom parsing classifies a
// version-bearing title as a release and a plain one as a post, and dedups
// on canonical URL (trailing-slash variant included).
func TestFeed_AtomEntriesClassifyAndDedup(t *testing.T) {
	data := `<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>v1.2.3</title><link rel="alternate" href="https://example.com/r"/><published>2024-01-01</published></entry>
<entry><title>v1.2.3</title><link rel="alternate" href="https://example.com/r/"/><published>2024-01-02</published></entry>
<entry><title>My thoughts on Go</title><link href="https://example.com/blog"/></entry>
</feed>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 deduped entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected version title classified as release, got %v", entries[0].Kind)
	}
	if entries[1].Kind != FeedKindPost {
		t.Fatalf("expected plain title classified as post, got %v", entries[1].Kind)
	}
}

// TestFeed_ChangelogCategoryClassifiesAsRelease verifies classification also
// looks at category terms, not title alone.
func TestFeed_ChangelogCategoryClassifiesAsRelease(t *testing.T) {
	data := `<rss><channel>
<item><title>Update notes</title><link>https://example.com/x</link><category>Changelog</category></item>
</channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected changelog category to classify as release, got %+v", entries)
	}
}

// TestFeed_EntryWithNoLinkFlagsURLMissingButNotDeduped verifies an entry
// with no link has no identity to dedup on, so it is retained (not silently
// collapsed with another linkless entry) and flags "url" missing.
func TestFeed_EntryWithNoLinkFlagsURLMissingButNotDeduped(t *testing.T) {
	data := `<rss><channel>
<item><title>No link 1</title></item>
<item><title>No link 2</title></item>
</channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected both linkless entries retained (no identity to dedup on), got %d", len(entries))
	}
	for _, e := range entries {
		found := false
		for _, f := range e.Missing {
			if f == "url" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected url flagged missing, got %+v", e.Missing)
		}
	}
}

// TestFeed_MissingTitleAndPublishedFlagged verifies an item lacking title
// and pubDate flags exactly those two fields missing.
func TestFeed_MissingTitleAndPublishedFlagged(t *testing.T) {
	data := `<rss><channel><item><link>https://example.com/x</link></item></channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	want := map[string]bool{"title": true, "published": true}
	if len(entries[0].Missing) != len(want) {
		t.Fatalf("expected title+published flagged missing, got %+v", entries[0].Missing)
	}
	for _, f := range entries[0].Missing {
		if !want[f] {
			t.Fatalf("unexpected missing field %q", f)
		}
	}
}

// TestFeed_UnrecognizedRootElementErrors verifies a document that is neither
// rss nor Atom feed errors rather than silently returning nothing.
func TestFeed_UnrecognizedRootElementErrors(t *testing.T) {
	_, err := ParseFeed([]byte(`<somethingelse><foo/></somethingelse>`))
	if err == nil {
		t.Fatalf("expected error for unrecognized feed root element")
	}
}

// TestFeed_TrulyMalformedXMLErrors verifies feed parsing is not fail-open
// like Sitemap: a feed decodes as one XML tree, so genuinely malformed XML
// must error rather than silently return a partial/wrong result.
func TestFeed_TrulyMalformedXMLErrors(t *testing.T) {
	_, err := ParseFeed([]byte(`<rss><channel><item><title>Unclosed`))
	if err == nil {
		t.Fatalf("expected error for malformed feed XML")
	}
}

// TestFeed_EmptyInputErrors verifies an empty document errors (there is no
// root element to classify as rss/Atom).
func TestFeed_EmptyInputErrors(t *testing.T) {
	_, err := ParseFeed([]byte(``))
	if err == nil {
		t.Fatalf("expected error on empty feed input")
	}
}

// TestFeed_AtomLinkPrefersAlternateOverEditRel verifies atomEntryLink picks
// the entry's own page over a non-alternate (e.g. edit) link.
func TestFeed_AtomLinkPrefersAlternateOverEditRel(t *testing.T) {
	data := `<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>x</title><link rel="edit" href="https://example.com/edit"/><link rel="alternate" href="https://example.com/page"/></entry>
</feed>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != "https://example.com/page" {
		t.Fatalf("expected alternate link chosen over edit link, got %+v", entries)
	}
}

// ---- Fetcher: HTTP timeout ----

// TestFetcher_ZeroOrNegativeTimeoutFallsBackToDefault verifies NewFetcher
// never constructs an unbounded client.
func TestFetcher_ZeroOrNegativeTimeoutFallsBackToDefault(t *testing.T) {
	f := NewFetcher(0)
	if f.Client.Timeout != DefaultTimeout {
		t.Fatalf("expected zero timeout to fall back to DefaultTimeout, got %v", f.Client.Timeout)
	}
	f2 := NewFetcher(-5 * time.Second)
	if f2.Client.Timeout != DefaultTimeout {
		t.Fatalf("expected negative timeout to fall back to DefaultTimeout, got %v", f2.Client.Timeout)
	}
}

// TestFetcher_TimesOutOnSlowServer verifies a request against a server
// slower than the configured timeout errors instead of blocking.
func TestFetcher_TimesOutOnSlowServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("too slow"))
	}))
	defer srv.Close()

	f := NewFetcher(50 * time.Millisecond)
	_, err := f.FetchSitemap(context.Background(), srv.URL, SitemapWindow{})
	if err == nil {
		t.Fatalf("expected timeout error from slow server, got nil")
	}
}

// TestFetcher_NonSuccessStatusIsError verifies a non-2xx response is
// returned as an error, not an empty body to parse.
func TestFetcher_NonSuccessStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	_, err := f.FetchFeed(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error mentioning 500 status, got %v", err)
	}
}

// TestFetcher_ContextCancellationAborts verifies a caller-supplied context
// deadline aborts the request independent of the Fetcher's own timeout.
func TestFetcher_ContextCancellationAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	f := NewFetcher(5 * time.Second)
	_, err := f.FetchArticleMeta(ctx, srv.URL)
	if err == nil {
		t.Fatalf("expected context cancellation to produce an error")
	}
}

// TestFetcher_SuccessfulRoundTripEndToEnd verifies the fetch-then-parse path
// works end to end against a live HTTP server.
func TestFetcher_SuccessfulRoundTripEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<urlset><url><loc>https://example.com/a</loc></url></urlset>`))
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	res, err := f.FetchSitemap(context.Background(), srv.URL, SitemapWindow{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("expected 1 entry from live fetch, got %+v", res)
	}
}

// ---- canonicalizeURL edge cases ----

// TestCanonicalizeURL_RootPathKeepsSingleSlash verifies the bare root path
// is the one case where a trailing slash is preserved, not stripped.
func TestCanonicalizeURL_RootPathKeepsSingleSlash(t *testing.T) {
	c, err := canonicalizeURL("https://example.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != "https://example.com/" {
		t.Fatalf("expected root path to keep trailing slash, got %q", c)
	}
}

// TestCanonicalizeURL_RejectsEmptyAndWhitespace verifies empty/whitespace
// input is rejected rather than canonicalized into something misleading.
func TestCanonicalizeURL_RejectsEmptyAndWhitespace(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if _, err := canonicalizeURL(in); err == nil {
			t.Fatalf("expected error for input %q", in)
		}
	}
}

// TestFeed_AtomLinkFallsBackToFirstLinkWhenNoneMarkedAlternate verifies
// atomEntryLink still picks a usable link when every link carries a
// non-alternate rel (e.g. all "related").
func TestFeed_AtomLinkFallsBackToFirstLinkWhenNoneMarkedAlternate(t *testing.T) {
	data := `<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>x</title><link rel="related" href="https://example.com/first"/><link rel="via" href="https://example.com/second"/></entry>
</feed>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != "https://example.com/first" {
		t.Fatalf("expected fallback to first link, got %+v", entries)
	}
}

// TestSitemap_LastModAcceptsFullRFC3339Timestamp verifies parseSitemapTime's
// RFC3339 branch (not just the bare-date branch) is honored by the window
// filter.
func TestSitemap_LastModAcceptsFullRFC3339Timestamp(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/a</loc><lastmod>2024-06-15T10:00:00Z</lastmod></url></urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{
		Since: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	})
	if len(res.Entries) != 1 {
		t.Fatalf("expected RFC3339 lastmod to pass window, got %+v", res)
	}
}

// TestFetcher_BuildRequestErrorIsReturned verifies an unbuildable request
// (invalid URL) surfaces as an error, not a panic.
func TestFetcher_BuildRequestErrorIsReturned(t *testing.T) {
	f := NewFetcher(5 * time.Second)
	_, err := f.FetchFeed(context.Background(), "http://[::1]:namedport")
	if err == nil {
		t.Fatalf("expected error building request for invalid URL")
	}
}

// TestCanonicalizeURL_RejectsSchemeOnlyOrHostOnlyMissingCounterpart verifies
// a scheme with no host (mailto:) and a host with no scheme
// (protocol-relative) both lack the stable identity dedup requires.
func TestCanonicalizeURL_RejectsSchemeOnlyOrHostOnlyMissingCounterpart(t *testing.T) {
	if _, err := canonicalizeURL("mailto:foo@example.com"); err == nil {
		t.Fatalf("expected error for scheme with no host")
	}
	if _, err := canonicalizeURL("//example.com/a"); err == nil {
		t.Fatalf("expected error for protocol-relative url with no scheme")
	}
}
