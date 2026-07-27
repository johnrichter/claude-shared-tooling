package webfetch

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/url"
	"strings"
	"time"
)

// SitemapEntry is one <url> record recovered from a sitemap. URL is the
// canonicalized <loc> and is the entry's identity for dedup purposes.
// LastMod is carried verbatim for display/filtering only — a publisher can
// rewrite it for the same URL without the page's identity changing, so it
// is never part of the dedup key.
type SitemapEntry struct {
	URL     string
	LastMod string
}

// SitemapWindow narrows ParseSitemap's output. A zero SitemapWindow selects
// every recoverable entry.
type SitemapWindow struct {
	PathPrefix string    // keep only URLs whose path starts with this prefix; "" keeps all
	Since      time.Time // keep only entries whose lastmod is >= Since; zero disables the bound
	Until      time.Time // keep only entries whose lastmod is <= Until; zero disables the bound
}

// SitemapResult is ParseSitemap's fail-open output: whatever entries it
// could recover, plus a count of <url> blocks it found but could not use
// (malformed XML inside the block, an unparseable <loc>, or — when Since/
// Until narrows the window — a lastmod it couldn't parse to test against
// the window). ParseSitemap never errors on bad input; Skipped is how a
// caller observes data loss instead.
type SitemapResult struct {
	Entries []SitemapEntry
	Skipped int
}

type sitemapURLBlock struct {
	XMLName xml.Name `xml:"url"`
	Loc     string   `xml:"loc"`
	LastMod string   `xml:"lastmod"`
}

var (
	sitemapOpenTag  = []byte("<url>")
	sitemapCloseTag = []byte("</url>")
)

// ParseSitemap recovers <url> entries from a sitemap document, applying
// window's prefix/time filters and deduping on each entry's canonical URL.
// It fails open: rather than parse the document as one XML tree (where one
// malformed or truncated <url> block would abort everything after it), it
// scans for individually well-formed <url>...</url> blocks and decodes each
// on its own, dropping only the block that's actually broken. A document
// that is not a sitemap at all (or is truncated before any complete block)
// yields an empty, non-error result.
func ParseSitemap(data []byte, window SitemapWindow) SitemapResult {
	var result SitemapResult
	seen := make(map[string]bool)

	pos := 0
	for {
		start := bytes.Index(data[pos:], sitemapOpenTag)
		if start < 0 {
			break
		}
		start += pos
		endRel := bytes.Index(data[start:], sitemapCloseTag)
		if endRel < 0 {
			// Trailing block has no closing tag — a truncated fetch or
			// stream cutoff. Drop it and stop; there's nothing complete
			// left to recover past this point.
			result.Skipped++
			break
		}
		end := start + endRel + len(sitemapCloseTag)

		var block sitemapURLBlock
		if err := xml.Unmarshal(data[start:end], &block); err != nil {
			result.Skipped++
			pos = end
			continue
		}

		canon, err := canonicalizeURL(block.Loc)
		if err != nil {
			result.Skipped++
			pos = end
			continue
		}

		switch classifyWindow(canon, block.LastMod, window) {
		case windowUnevaluable:
			// A window bound is set but this block's lastmod can't be
			// parsed to test against it — genuine data loss, so count it.
			result.Skipped++
			pos = end
			continue
		case windowFilteredOut:
			// Well-formed but deliberately outside the requested window;
			// not data loss, so it is excluded without inflating Skipped.
			pos = end
			continue
		}

		if seen[canon] {
			pos = end
			continue
		}
		seen[canon] = true
		result.Entries = append(result.Entries, SitemapEntry{URL: canon, LastMod: block.LastMod})
		pos = end
	}

	return result
}

// windowVerdict distinguishes a clean filter exclusion from a block whose
// lastmod can't be evaluated against a requested time window — only the
// latter is data loss the caller should see via SitemapResult.Skipped.
type windowVerdict int

const (
	windowKeep        windowVerdict = iota // inside the window; keep
	windowFilteredOut                      // well-formed, deliberately excluded
	windowUnevaluable                      // a window bound is set but lastmod won't parse
)

func classifyWindow(canonURL, lastMod string, window SitemapWindow) windowVerdict {
	if window.PathPrefix != "" {
		u, err := url.Parse(canonURL)
		if err != nil || !strings.HasPrefix(u.Path, window.PathPrefix) {
			return windowFilteredOut
		}
	}
	if !window.Since.IsZero() || !window.Until.IsZero() {
		t, err := parseSitemapTime(lastMod)
		if err != nil {
			return windowUnevaluable
		}
		if !window.Since.IsZero() && t.Before(window.Since) {
			return windowFilteredOut
		}
		if !window.Until.IsZero() && t.After(window.Until) {
			return windowFilteredOut
		}
	}
	return windowKeep
}

// parseSitemapTime accepts the two lastmod shapes the sitemap protocol
// permits: a full timestamp (W3C datetime, which RFC 3339 is a subset of)
// or a bare date.
func parseSitemapTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// FetchSitemap fetches url under ctx and applies ParseSitemap to the body.
func (f *Fetcher) FetchSitemap(ctx context.Context, url string, window SitemapWindow) (SitemapResult, error) {
	body, err := f.fetch(ctx, url)
	if err != nil {
		return SitemapResult{}, err
	}
	return ParseSitemap(body, window), nil
}
