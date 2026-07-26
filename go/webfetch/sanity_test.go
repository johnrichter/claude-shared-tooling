package webfetch

import "testing"

// Sanity coverage only — the adversarial/unit suite proving each acceptance
// criterion is authored separately.

// A stale lastmod on a re-seen URL must not create a second entry — dedup
// keys on the canonical URL alone.
func TestSanity_SitemapDedupsOnCanonicalURLIgnoresLastMod(t *testing.T) {
	xml := `<urlset>
<url><loc>https://Example.com/a/</loc><lastmod>2024-01-01</lastmod></url>
<url><loc>https://example.com/a</loc><lastmod>2024-06-01</lastmod></url>
</urlset>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 {
		t.Fatalf("expected 1 deduped entry, got %d: %+v", len(res.Entries), res.Entries)
	}
}

// A truncated final <url> block must be dropped, not treated as a fatal
// parse error for the whole document.
func TestSanity_SitemapFailsOpenOnTruncatedTrailingEntry(t *testing.T) {
	xml := `<urlset><url><loc>https://example.com/a</loc></url><url><loc>https://example.com/b</loc>`
	res := ParseSitemap([]byte(xml), SitemapWindow{})
	if len(res.Entries) != 1 || res.Skipped != 1 {
		t.Fatalf("expected 1 entry + 1 skip, got entries=%d skipped=%d", len(res.Entries), res.Skipped)
	}
}

// A page carrying only a <title> must yield that title verbatim and flag
// every other field as missing, never a guessed value.
func TestSanity_ArticleMetaBlankAndFlaggedWhenMissing(t *testing.T) {
	m, err := ParseArticleMeta("https://example.com/x", []byte(`<html><head><title>Only Title</title></head></html>`))
	if err != nil {
		t.Fatalf("ParseArticleMeta: %v", err)
	}
	if m.Title != "Only Title" {
		t.Fatalf("expected verbatim title, got %q", m.Title)
	}
	if m.Description != "" || m.Author != "" || m.PublishedAt != "" {
		t.Fatalf("expected blank missing fields, got %+v", m)
	}
	want := map[string]bool{"description": true, "author": true, "published_at": true}
	if len(m.Missing) != len(want) {
		t.Fatalf("expected 3 missing fields, got %v", m.Missing)
	}
	for _, f := range m.Missing {
		if !want[f] {
			t.Fatalf("unexpected missing field %q", f)
		}
	}
}

// A version-bearing title classifies as a release, a plain one as a post,
// and a re-seen canonical URL (trailing slash aside) collapses to one entry.
func TestSanity_FeedClassifiesAndDedupsOnCanonicalURL(t *testing.T) {
	data := `<rss><channel>
<item><title>v2.0.0 released</title><link>https://example.com/p</link><pubDate>2024-01-01</pubDate></item>
<item><title>v2.0.0 released</title><link>https://example.com/p/</link><pubDate>2024-01-02</pubDate></item>
<item><title>Weekly update</title><link>https://example.com/q</link></item>
</channel></rss>`
	entries, err := ParseFeed([]byte(data))
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 deduped entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Kind != FeedKindRelease {
		t.Fatalf("expected release classification, got %v", entries[0].Kind)
	}
	if entries[1].Kind != FeedKindPost {
		t.Fatalf("expected post classification, got %v", entries[1].Kind)
	}
}
