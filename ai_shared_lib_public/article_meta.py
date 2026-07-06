#!/usr/bin/env python3
"""
article_meta.py — Deterministically extract an article's title, published date, and a
short factual excerpt from a web page's structured metadata. NO language model is involved,
so there is nothing to hallucinate: every field is copied verbatim from the page's own
HTML `<title>`, Open Graph / article meta tags, or `<time>` element.

Usage:
    python3 article_meta.py --url https://www.anthropic.com/news/<slug>

Output: a single compact JSON object to stdout:
    {"url": "...", "title": "<or null>", "published": "<or null>", "excerpt": "<or null>"}

Extraction (all verbatim from the page — never synthesized):
    title     <- og:title, else twitter:title, else <title>
    published <- meta article:published_time, else article:modified_time, else
                 meta[name=date]/pubdate, else og:updated_time, else <time datetime="...">
    excerpt   <- og:description, else twitter:description, else meta[name=description]

A field with no corresponding tag is emitted as null (the caller renders "not stated" /
URL-only) — the script NEVER invents a value. On any network or parse failure it exits 0
with all fields null plus a stderr warning, so it can never block a caller (mirrors
sitemap_parser.py's fail-open contract).

Depends only on the Python stdlib (urllib, html.parser, html, json, argparse).
"""

import argparse
import html
import json
import sys
from html.parser import HTMLParser
from urllib.error import URLError
from urllib.request import Request, urlopen

FETCH_TIMEOUT = 15  # seconds
USER_AGENT = "jr-shared-article-meta/1.0"

# meta tags whose content is a candidate for each field, in priority order.
_TITLE_META = ("og:title", "twitter:title")
_PUBLISHED_META = ("article:published_time", "article:modified_time", "date", "pubdate", "og:updated_time")
_EXCERPT_META = ("og:description", "twitter:description", "description")


class _MetaExtractor(HTMLParser):
    """Collect the specific meta/title/time values we need — verbatim, no transformation."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.metas: dict[str, str] = {}      # normalized meta key -> first-seen content
        self._in_title = False
        self.title_text: str | None = None
        self.time_datetime: str | None = None

    def handle_starttag(self, tag: str, attrs: list) -> None:
        a = {k.lower(): (v or "") for k, v in attrs}
        if tag == "title":
            self._in_title = True
        elif tag == "meta":
            # A meta tag identifies its field via either `name=` or `property=` (OG uses property).
            key = (a.get("property") or a.get("name") or "").strip().lower()
            content = a.get("content")
            if key and content is not None and key not in self.metas:
                self.metas[key] = content  # first occurrence wins; store raw
        elif tag == "time" and self.time_datetime is None:
            dt = a.get("datetime")
            if dt:
                self.time_datetime = dt

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self._in_title = False

    def handle_data(self, data: str) -> None:
        if self._in_title and self.title_text is None:
            text = data.strip()
            if text:
                self.title_text = text


def _first(metas: dict, keys: tuple) -> str | None:
    """Return the first present, non-empty meta value among keys, unescaped."""
    for k in keys:
        v = metas.get(k)
        if v is not None and v.strip():
            return html.unescape(v.strip())
    return None


def fetch_html(url: str) -> str | None:
    """Fetch page HTML as text; return None on any error (warn to stderr)."""
    try:
        req = Request(url, headers={"User-Agent": USER_AGENT})
        with urlopen(req, timeout=FETCH_TIMEOUT) as resp:
            raw = resp.read()
        charset = resp.headers.get_content_charset() or "utf-8"
        return raw.decode(charset, errors="replace")
    except URLError as exc:
        print(f"[article-meta] WARNING: page unreachable ({exc}) — returning null fields", file=sys.stderr)
        return None
    except Exception as exc:  # noqa: BLE001 — never let a fetch error escape
        print(f"[article-meta] WARNING: unexpected fetch error ({exc}) — returning null fields", file=sys.stderr)
        return None


def extract_meta(html_text: str) -> dict:
    """Parse HTML and return {title, published, excerpt}, each verbatim-or-None."""
    parser = _MetaExtractor()
    try:
        parser.feed(html_text)
    except Exception as exc:  # noqa: BLE001 — malformed HTML must degrade, not raise
        print(f"[article-meta] WARNING: HTML parse error ({exc}) — returning what was parsed", file=sys.stderr)

    title = _first(parser.metas, _TITLE_META)
    if title is None and parser.title_text:
        title = html.unescape(parser.title_text)

    published = _first(parser.metas, _PUBLISHED_META)
    if published is None and parser.time_datetime:
        published = html.unescape(parser.time_datetime.strip())

    excerpt = _first(parser.metas, _EXCERPT_META)

    return {"title": title, "published": published, "excerpt": excerpt}


def article_meta(url: str) -> dict:
    """Fetch + extract; always returns the full shape, fields null on any failure."""
    html_text = fetch_html(url)
    if html_text is None:
        return {"url": url, "title": None, "published": None, "excerpt": None}
    fields = extract_meta(html_text)
    return {"url": url, **fields}


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Deterministically extract {title, published, excerpt} from a page's metadata (no LLM)."
    )
    parser.add_argument("--url", required=True, help="Article URL to extract metadata from.")
    args = parser.parse_args()

    try:
        result = article_meta(args.url)
    except Exception as exc:  # noqa: BLE001 — belt-and-suspenders: never crash a caller
        print(f"[article-meta] WARNING: unexpected error ({exc}) — emitting null fields", file=sys.stderr)
        result = {"url": args.url, "title": None, "published": None, "excerpt": None}

    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
