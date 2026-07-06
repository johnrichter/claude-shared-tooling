#!/usr/bin/env python3
"""
sitemap_parser.py — Fetch, parse, window-filter, and prefix-filter any XML sitemap.

Usage:
    python3 sitemap_parser.py --url URL [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--prefix /path/]

Output: compact JSON array of {loc, lastmod} records to stdout, one per matching
        <url> entry. Empty array [] on no matches. On ANY network or parse
        failure: exits 0, prints [] with a WARNING to stderr — never blocks a caller.

Handles both a flat <urlset> and a one-level <sitemapindex> (standard
http://www.sitemaps.org/schemas/sitemap/0.9 namespace, though the namespace
URI is not required — tag matching is namespace-agnostic). A <sitemapindex>'s
child sitemaps are fetched and recursed into exactly one level deep; nested
sitemapindexes beyond that are not followed, to bound the number of fetches.

--prefix matches an exact leading PATH SEGMENT, not a bare substring: a prefix
of /news/ matches /news/<slug> but never /news-and-events/... .

Both a CLI entrypoint (`main`) and importable functions (`parse_sitemap`,
`parse_sitemap_url`, `fetch_sitemap`) are provided.

Depends only on the Python stdlib (xml.etree, urllib, argparse, json, datetime).
"""

import argparse
import json
import sys
import xml.etree.ElementTree as ET
from datetime import date
from urllib.error import URLError
from urllib.parse import urlparse
from urllib.request import Request, urlopen

USER_AGENT = "jr-shared-sitemap-parser/1.0"
FETCH_TIMEOUT = 15  # seconds
MAX_SITEMAPINDEX_DEPTH = 1  # recurse exactly one level into child sitemaps


def _local(tag: str) -> str:
    """Return the local (namespace-stripped) name of an XML tag."""
    return tag.rsplit("}", 1)[-1] if "}" in tag else tag


def _find_local(elem: ET.Element, name: str) -> ET.Element | None:
    """Return the first direct child of `elem` whose local tag name is `name`."""
    for child in elem:
        if _local(child.tag) == name:
            return child
    return None


def _path_segments(path: str) -> list[str]:
    """Split a URL path into non-empty segments."""
    return [seg for seg in path.split("/") if seg]


def _prefix_matches(url_path: str, prefix: str) -> bool:
    """True if `url_path`'s leading segments exactly match `prefix`'s segments.

    Anchored to a path-segment boundary so a prefix like /news/ matches
    /news/<slug> but never falsely matches /news-and-events/... — a bare
    substring match would false-positive on that case.
    """
    prefix_segs = _path_segments(prefix)
    if not prefix_segs:
        return True
    url_segs = _path_segments(url_path)
    return url_segs[: len(prefix_segs)] == prefix_segs


def _extract_date(lastmod: str) -> date | None:
    """Best-effort extraction of the calendar date from a <lastmod> value.

    Sitemap <lastmod> may be a bare date (YYYY-MM-DD) or a full W3C
    datetime (YYYY-MM-DDTHH:MM:SS+00:00) — only the date portion matters
    for window filtering. Returns None on unparseable input.
    """
    if not lastmod:
        return None
    try:
        return date.fromisoformat(lastmod[:10])
    except ValueError:
        return None


def fetch_sitemap(url: str) -> bytes | None:
    """Fetch sitemap bytes for `url`; return None on any network error (warns to stderr)."""
    try:
        req = Request(url, headers={"User-Agent": USER_AGENT})
        with urlopen(req, timeout=FETCH_TIMEOUT) as resp:
            return resp.read()
    except URLError as exc:
        print(f"[sitemap-parser] WARNING: sitemap unreachable ({exc}) — returning empty result", file=sys.stderr)
        return None
    except Exception as exc:
        print(f"[sitemap-parser] WARNING: unexpected fetch error ({exc}) — returning empty result", file=sys.stderr)
        return None


def _parse_urlset(root: ET.Element, since: date | None, until: date | None, prefix: str | None) -> list[dict]:
    """Extract {loc, lastmod} records from a <urlset> root, applying filters."""
    records = []
    for url_elem in root:
        if _local(url_elem.tag) != "url":
            continue

        loc_elem = _find_local(url_elem, "loc")
        loc = (loc_elem.text or "").strip() if loc_elem is not None else ""
        if not loc:
            continue

        lastmod_elem = _find_local(url_elem, "lastmod")
        lastmod = (lastmod_elem.text or "").strip() if lastmod_elem is not None else ""

        if since is not None or until is not None:
            entry_date = _extract_date(lastmod)
            if entry_date is None:
                continue  # can't window-filter without a parseable date
            if since is not None and entry_date < since:
                continue
            if until is not None and entry_date > until:
                continue

        if prefix:
            if not _prefix_matches(urlparse(loc).path, prefix):
                continue

        records.append({"loc": loc, "lastmod": lastmod})
    return records


def parse_sitemap(
    xml_bytes: bytes,
    since: date | None = None,
    until: date | None = None,
    prefix: str | None = None,
    _fetch=fetch_sitemap,
    _depth: int = 0,
) -> list[dict]:
    """Parse sitemap XML bytes into filtered {loc, lastmod} records.

    Handles both a flat <urlset> and a one-level <sitemapindex> (child
    sitemaps referenced by a <sitemapindex> are fetched via `_fetch` and
    recursed into exactly once — never further, to bound network calls).
    Any parse failure returns [] with a stderr WARNING.
    """
    try:
        root = ET.fromstring(xml_bytes)
    except ET.ParseError as exc:
        print(f"[sitemap-parser] WARNING: XML parse error ({exc}) — returning empty result", file=sys.stderr)
        return []
    except Exception as exc:
        print(f"[sitemap-parser] WARNING: unexpected parse error ({exc}) — returning empty result", file=sys.stderr)
        return []

    root_name = _local(root.tag)

    if root_name == "urlset":
        return _parse_urlset(root, since, until, prefix)

    if root_name == "sitemapindex":
        if _depth >= MAX_SITEMAPINDEX_DEPTH:
            print(
                "[sitemap-parser] WARNING: sitemapindex nesting exceeds one level — skipping deeper levels",
                file=sys.stderr,
            )
            return []
        records = []
        for sitemap_elem in root:
            if _local(sitemap_elem.tag) != "sitemap":
                continue
            loc_elem = _find_local(sitemap_elem, "loc")
            child_url = (loc_elem.text or "").strip() if loc_elem is not None else ""
            if not child_url:
                continue
            child_bytes = _fetch(child_url)
            if child_bytes is None:
                continue  # fetch failure already warned by _fetch
            records.extend(
                parse_sitemap(child_bytes, since=since, until=until, prefix=prefix, _fetch=_fetch, _depth=_depth + 1)
            )
        return records

    print(f"[sitemap-parser] WARNING: unrecognized root element <{root_name}> — returning empty result", file=sys.stderr)
    return []


def parse_sitemap_url(
    url: str,
    since: date | None = None,
    until: date | None = None,
    prefix: str | None = None,
) -> list[dict]:
    """Fetch `url` and return its filtered {loc, lastmod} records; [] on any failure."""
    xml_bytes = fetch_sitemap(url)
    if xml_bytes is None:
        return []
    return parse_sitemap(xml_bytes, since=since, until=until, prefix=prefix, _fetch=fetch_sitemap)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Fetch, parse, window-filter, and prefix-filter an XML sitemap."
    )
    parser.add_argument("--url", required=True, metavar="URL", help="Sitemap URL to fetch (urlset or sitemapindex).")
    parser.add_argument("--since", metavar="YYYY-MM-DD", help="Inclusive start of the <lastmod> date window.")
    parser.add_argument("--until", metavar="YYYY-MM-DD", help="Inclusive end of the <lastmod> date window.")
    parser.add_argument("--prefix", metavar="/path/", help="Exact leading path-segment filter, e.g. /news/.")
    args = parser.parse_args()

    try:
        since = date.fromisoformat(args.since) if args.since else None
        until = date.fromisoformat(args.until) if args.until else None
    except ValueError as exc:
        print(f"[sitemap-parser] ERROR: invalid date argument — {exc}", file=sys.stderr)
        sys.exit(1)

    if since is not None and until is not None and since > until:
        print(f"[sitemap-parser] ERROR: --since {since} is after --until {until}", file=sys.stderr)
        sys.exit(1)

    try:
        records = parse_sitemap_url(args.url, since=since, until=until, prefix=args.prefix)
    except Exception as exc:
        # Belt-and-suspenders: never let an unexpected error block a caller.
        print(f"[sitemap-parser] WARNING: unexpected error ({exc}) — returning empty result", file=sys.stderr)
        records = []

    print(json.dumps(records, ensure_ascii=False, separators=(",", ":")))


if __name__ == "__main__":
    main()
