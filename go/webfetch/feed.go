package webfetch

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
)

// FeedEntryKind classifies a FeedEntry by its own content — never by
// guesswork about the feed's overall purpose.
type FeedEntryKind string

const (
	// FeedKindRelease is an entry whose title or category names a version
	// or explicitly calls itself a release/changelog.
	FeedKindRelease FeedEntryKind = "release"
	// FeedKindPost is any entry that doesn't match FeedKindRelease.
	FeedKindPost FeedEntryKind = "post"
)

// FeedEntry is one item/entry from an RSS or Atom feed. URL is the
// canonicalized link and is the entry's dedup key. Fields the source didn't
// carry are left blank and named in Missing rather than guessed.
type FeedEntry struct {
	Title     string
	URL       string
	Published string
	Kind      FeedEntryKind
	Missing   []string
}

var (
	releaseWordPattern = regexp.MustCompile(`(?i)\b(released?|releases|changelog)\b`)
	versionPattern     = regexp.MustCompile(`\bv?\d+\.\d+(\.\d+)?\b`)
)

func classifyFeedEntry(title string, categories []string) FeedEntryKind {
	if releaseWordPattern.MatchString(title) || versionPattern.MatchString(title) {
		return FeedKindRelease
	}
	for _, c := range categories {
		if releaseWordPattern.MatchString(c) || versionPattern.MatchString(c) {
			return FeedKindRelease
		}
	}
	return FeedKindPost
}

type rssDoc struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title      string   `xml:"title"`
	Link       string   `xml:"link"`
	PubDate    string   `xml:"pubDate"`
	Categories []string `xml:"category"`
}

type atomDoc struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Links      []atomLink     `xml:"link"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Categories []atomCategory `xml:"category"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

// ParseFeed parses an RSS 2.0 or Atom document, classifies each entry, and
// dedups on canonical URL — keeping the first (document-order) occurrence
// of a given URL. An entry with no usable link is never deduped away (there
// is no identity to dedup on); it is still returned, with "url" in Missing.
func ParseFeed(data []byte) ([]FeedEntry, error) {
	root, err := feedRootElement(data)
	if err != nil {
		return nil, err
	}
	switch root {
	case "rss":
		return parseRSSFeed(data)
	case "feed":
		return parseAtomFeed(data)
	default:
		return nil, fmt.Errorf("webfetch: unrecognized feed root element %q", root)
	}
}

func feedRootElement(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("webfetch: read feed root element: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local, nil
		}
	}
}

func parseRSSFeed(data []byte) ([]FeedEntry, error) {
	var doc rssDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("webfetch: parse rss feed: %w", err)
	}
	entries := make([]FeedEntry, 0, len(doc.Channel.Items))
	seen := make(map[string]bool, len(doc.Channel.Items))
	for _, item := range doc.Channel.Items {
		e := buildFeedEntry(item.Title, item.Link, item.PubDate, item.Categories)
		entries = appendDeduped(entries, seen, e)
	}
	return entries, nil
}

func parseAtomFeed(data []byte) ([]FeedEntry, error) {
	var doc atomDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("webfetch: parse atom feed: %w", err)
	}
	entries := make([]FeedEntry, 0, len(doc.Entries))
	seen := make(map[string]bool, len(doc.Entries))
	for _, entry := range doc.Entries {
		terms := make([]string, len(entry.Categories))
		for i, c := range entry.Categories {
			terms[i] = c.Term
		}
		e := buildFeedEntry(entry.Title, atomEntryLink(entry.Links), firstNonEmpty(entry.Published, entry.Updated), terms)
		entries = appendDeduped(entries, seen, e)
	}
	return entries, nil
}

// atomEntryLink picks the entry's own page over any alternate-representation
// link (e.g. a comments or edit link): a rel="alternate" link, or the first
// link if none is marked alternate.
func atomEntryLink(links []atomLink) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return l.Href
		}
	}
	if len(links) > 0 {
		return links[0].Href
	}
	return ""
}

func buildFeedEntry(title, link, published string, categories []string) FeedEntry {
	e := FeedEntry{
		Title:     title,
		Published: published,
		Kind:      classifyFeedEntry(title, categories),
	}
	if canon, err := canonicalizeURL(link); err == nil {
		e.URL = canon
	}
	if e.Title == "" {
		e.Missing = append(e.Missing, "title")
	}
	if e.URL == "" {
		e.Missing = append(e.Missing, "url")
	}
	if e.Published == "" {
		e.Missing = append(e.Missing, "published")
	}
	return e
}

func appendDeduped(entries []FeedEntry, seen map[string]bool, e FeedEntry) []FeedEntry {
	if e.URL != "" {
		if seen[e.URL] {
			return entries
		}
		seen[e.URL] = true
	}
	return append(entries, e)
}

// FetchFeed fetches url under ctx and applies ParseFeed to the body.
func (f *Fetcher) FetchFeed(ctx context.Context, url string) ([]FeedEntry, error) {
	body, err := f.fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	return ParseFeed(body)
}
