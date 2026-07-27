package webfetch

import (
	"bytes"
	"context"
	"strings"

	"golang.org/x/net/html"
)

// ArticleMeta is what a single page's own <title>/<meta> tags say about
// itself, taken verbatim — nothing here is inferred or synthesized. A field
// the page doesn't carry is left blank and its name added to Missing, so a
// caller can distinguish "the page said nothing" from "the page said this".
type ArticleMeta struct {
	URL         string
	Title       string
	Description string
	Author      string
	PublishedAt string
	Missing     []string
}

// rawTags collects every candidate source for each ArticleMeta field found
// while walking the document, keeping only the first (document-order)
// occurrence of each — later duplicate tags never overwrite an earlier one.
type rawTags struct {
	title         string
	ogTitle       string
	description   string
	ogDescription string
	author        string
	articleAuthor string
	publishedTime string
	dateMeta      string
}

// ParseArticleMeta scrapes ArticleMeta from an HTML page body. Parsing goes
// through golang.org/x/net/html, which implements the HTML5 parsing
// algorithm and so tolerates the unclosed tags and non-XML voids that real
// pages carry — it never fails on malformed markup, only on a genuine read
// error from body's underlying reader.
func ParseArticleMeta(sourceURL string, body []byte) (ArticleMeta, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ArticleMeta{}, err
	}

	var raw rawTags
	collectMetaTags(doc, &raw)

	m := ArticleMeta{
		URL:         sourceURL,
		Title:       firstNonEmpty(raw.ogTitle, raw.title),
		Description: firstNonEmpty(raw.ogDescription, raw.description),
		Author:      firstNonEmpty(raw.articleAuthor, raw.author),
		PublishedAt: firstNonEmpty(raw.publishedTime, raw.dateMeta),
	}
	if m.Title == "" {
		m.Missing = append(m.Missing, "title")
	}
	if m.Description == "" {
		m.Missing = append(m.Missing, "description")
	}
	if m.Author == "" {
		m.Missing = append(m.Missing, "author")
	}
	if m.PublishedAt == "" {
		m.Missing = append(m.Missing, "published_at")
	}
	return m, nil
}

func collectMetaTags(n *html.Node, raw *rawTags) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "title":
			if raw.title == "" && n.FirstChild != nil {
				raw.title = strings.TrimSpace(n.FirstChild.Data)
			}
		case "meta":
			name, property, content := metaAttrs(n)
			switch {
			case property == "og:title" && raw.ogTitle == "":
				raw.ogTitle = content
			case name == "description" && raw.description == "":
				raw.description = content
			case property == "og:description" && raw.ogDescription == "":
				raw.ogDescription = content
			case name == "author" && raw.author == "":
				raw.author = content
			case property == "article:author" && raw.articleAuthor == "":
				raw.articleAuthor = content
			case property == "article:published_time" && raw.publishedTime == "":
				raw.publishedTime = content
			case name == "date" && raw.dateMeta == "":
				raw.dateMeta = content
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectMetaTags(c, raw)
	}
}

func metaAttrs(n *html.Node) (name, property, content string) {
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "name":
			name = strings.ToLower(strings.TrimSpace(a.Val))
		case "property":
			property = strings.ToLower(strings.TrimSpace(a.Val))
		case "content":
			content = strings.TrimSpace(a.Val)
		}
	}
	return name, property, content
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// FetchArticleMeta fetches sourceURL under ctx and applies ParseArticleMeta
// to the body.
func (f *Fetcher) FetchArticleMeta(ctx context.Context, sourceURL string) (ArticleMeta, error) {
	body, err := f.fetch(ctx, sourceURL)
	if err != nil {
		return ArticleMeta{}, err
	}
	return ParseArticleMeta(sourceURL, body)
}
