"""
test_sitemap_parser.py — Unit tests for claude_tooling.sitemap_parser

Run with: python -m unittest discover -s tests -p "test_*.py" -v
or:       python -m pytest tests/ -v

All tests run offline against inline XML fixtures — the network is never
touched (fetch failures are simulated by monkeypatching fetch_sitemap).

Test strategy (mirrors acceptance criteria):
    1. Malformed/garbage XML -> [] with a stderr warning.
    2. Flat <urlset> parses to expected {loc, lastmod} records.
    3. One-level <sitemapindex> recurses into child sitemaps and aggregates.
    4. --since/--until window filter is inclusive at both boundaries and
       drops out-of-window entries.
    5. --prefix does exact path-segment matching: positive /news/<slug>,
       negative /news-and-events/... (the M9 prefix over/under-match risk).
    6. Simulated fetch failure (raised URLError) exits 0 and prints [].
    7. Importability: import the module and call parse_sitemap directly.
"""

import io
import json
import subprocess
import sys
import unittest
from contextlib import redirect_stderr
from datetime import date
from pathlib import Path
from unittest.mock import patch
from urllib.error import URLError

_HERE = Path(__file__).parent
if str(_HERE) not in sys.path:
    sys.path.insert(0, str(_HERE))

from claude_tooling import sitemap_parser as sm


FLAT_URLSET = b"""<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/news/first-story</loc>
    <lastmod>2026-01-10</lastmod>
  </url>
  <url>
    <loc>https://example.com/news/second-story</loc>
    <lastmod>2026-01-15T10:30:00+00:00</lastmod>
  </url>
  <url>
    <loc>https://example.com/news-and-events/gala</loc>
    <lastmod>2026-01-15</lastmod>
  </url>
  <url>
    <loc>https://example.com/blog/unrelated</loc>
    <lastmod>2026-02-01</lastmod>
  </url>
</urlset>
"""

SITEMAPINDEX = b"""<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>https://example.com/sitemap-news.xml</loc>
    <lastmod>2026-01-01</lastmod>
  </sitemap>
  <sitemap>
    <loc>https://example.com/sitemap-blog.xml</loc>
    <lastmod>2026-01-01</lastmod>
  </sitemap>
</sitemapindex>
"""

CHILD_NEWS_URLSET = b"""<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/news/child-story</loc>
    <lastmod>2026-01-12</lastmod>
  </url>
</urlset>
"""

CHILD_BLOG_URLSET = b"""<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/blog/child-post</loc>
    <lastmod>2026-01-13</lastmod>
  </url>
</urlset>
"""

MALFORMED_XML = b"<urlset><url><loc>broken"


class TestMalformedXml(unittest.TestCase):
    def test_malformed_xml_returns_empty_list(self):
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            records = sm.parse_sitemap(MALFORMED_XML)
        self.assertEqual(records, [])
        self.assertIn("WARNING", stderr.getvalue())

    def test_garbage_bytes_returns_empty_list(self):
        records = sm.parse_sitemap(b"not xml at all \x00\x01")
        self.assertEqual(records, [])

    def test_unrecognized_root_returns_empty_list(self):
        records = sm.parse_sitemap(b"<somethingelse><foo/></somethingelse>")
        self.assertEqual(records, [])


class TestFlatUrlset(unittest.TestCase):
    def test_parses_all_entries_with_no_filters(self):
        records = sm.parse_sitemap(FLAT_URLSET)
        self.assertEqual(len(records), 4)
        self.assertEqual(
            records[0],
            {"loc": "https://example.com/news/first-story", "lastmod": "2026-01-10"},
        )
        # full W3C datetime lastmod is preserved verbatim
        self.assertEqual(records[1]["lastmod"], "2026-01-15T10:30:00+00:00")

    def test_importable_as_module_function(self):
        # Directly exercises the importable surface (not the CLI).
        self.assertTrue(callable(sm.parse_sitemap))
        records = sm.parse_sitemap(FLAT_URLSET, prefix="/blog/")
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["loc"], "https://example.com/blog/unrelated")


class TestSitemapIndexRecursion(unittest.TestCase):
    def test_recurses_one_level_and_aggregates(self):
        def fake_fetch(url):
            if url == "https://example.com/sitemap-news.xml":
                return CHILD_NEWS_URLSET
            if url == "https://example.com/sitemap-blog.xml":
                return CHILD_BLOG_URLSET
            return None

        records = sm.parse_sitemap(SITEMAPINDEX, _fetch=fake_fetch)
        locs = sorted(r["loc"] for r in records)
        self.assertEqual(
            locs,
            [
                "https://example.com/blog/child-post",
                "https://example.com/news/child-story",
            ],
        )

    def test_child_fetch_failure_is_skipped_not_fatal(self):
        def fake_fetch(url):
            return None  # simulate every child fetch failing

        stderr = io.StringIO()
        with redirect_stderr(stderr):
            records = sm.parse_sitemap(SITEMAPINDEX, _fetch=fake_fetch)
        self.assertEqual(records, [])

    def test_nested_sitemapindex_beyond_one_level_is_not_followed(self):
        # A child sitemap that is itself a sitemapindex should not be
        # recursed into again — recursion is capped at exactly one level.
        def fake_fetch(url):
            return SITEMAPINDEX  # child claims to be another index

        stderr = io.StringIO()
        with redirect_stderr(stderr):
            records = sm.parse_sitemap(SITEMAPINDEX, _fetch=fake_fetch)
        self.assertEqual(records, [])
        self.assertIn("WARNING", stderr.getvalue())


class TestWindowFilter(unittest.TestCase):
    def test_inclusive_lower_boundary(self):
        records = sm.parse_sitemap(FLAT_URLSET, since=date(2026, 1, 10), until=date(2026, 1, 10))
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["loc"], "https://example.com/news/first-story")

    def test_inclusive_upper_boundary(self):
        records = sm.parse_sitemap(FLAT_URLSET, since=date(2026, 1, 15), until=date(2026, 1, 15))
        locs = sorted(r["loc"] for r in records)
        self.assertEqual(
            locs,
            [
                "https://example.com/news-and-events/gala",
                "https://example.com/news/second-story",
            ],
        )

    def test_drops_out_of_window_entries(self):
        records = sm.parse_sitemap(FLAT_URLSET, since=date(2026, 1, 1), until=date(2026, 1, 1))
        self.assertEqual(records, [])

    def test_full_window_returns_all(self):
        records = sm.parse_sitemap(FLAT_URLSET, since=date(2026, 1, 1), until=date(2026, 2, 28))
        self.assertEqual(len(records), 4)


class TestPrefixFilter(unittest.TestCase):
    def test_positive_match_segment(self):
        records = sm.parse_sitemap(FLAT_URLSET, prefix="/news/")
        locs = sorted(r["loc"] for r in records)
        self.assertEqual(
            locs,
            [
                "https://example.com/news/first-story",
                "https://example.com/news/second-story",
            ],
        )

    def test_negative_no_bare_substring_match(self):
        # /news/ must NOT match /news-and-events/... — exact segment only.
        records = sm.parse_sitemap(FLAT_URLSET, prefix="/news/")
        locs = [r["loc"] for r in records]
        self.assertNotIn("https://example.com/news-and-events/gala", locs)

    def test_prefix_without_trailing_slash_also_segment_anchored(self):
        records = sm.parse_sitemap(FLAT_URLSET, prefix="/news")
        locs = [r["loc"] for r in records]
        self.assertNotIn("https://example.com/news-and-events/gala", locs)
        self.assertIn("https://example.com/news/first-story", locs)

    def test_combined_prefix_and_window_filter(self):
        records = sm.parse_sitemap(FLAT_URLSET, since=date(2026, 1, 15), until=date(2026, 1, 15), prefix="/news/")
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["loc"], "https://example.com/news/second-story")


class TestFetchFailure(unittest.TestCase):
    def test_urlerror_returns_none_and_warns(self):
        with patch("claude_tooling.sitemap_parser.urlopen", side_effect=URLError("simulated unreachable")):
            stderr = io.StringIO()
            with redirect_stderr(stderr):
                result = sm.fetch_sitemap("https://unreachable.example.com/sitemap.xml")
        self.assertIsNone(result)
        self.assertIn("WARNING", stderr.getvalue())

    def test_parse_sitemap_url_returns_empty_list_on_fetch_failure(self):
        with patch("claude_tooling.sitemap_parser.urlopen", side_effect=URLError("simulated unreachable")):
            stderr = io.StringIO()
            with redirect_stderr(stderr):
                records = sm.parse_sitemap_url("https://unreachable.example.com/sitemap.xml")
        self.assertEqual(records, [])

    def test_cli_exits_0_and_prints_empty_array_on_fetch_failure(self):
        proc = subprocess.run(
            [sys.executable, "-m", "claude_tooling.sitemap_parser", "--url", "http://127.0.0.1:1/does-not-exist.xml"],
            capture_output=True,
            text=True,
            timeout=20,
        )
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(json.loads(proc.stdout), [])
        self.assertIn("WARNING", proc.stderr)


class TestCliOutput(unittest.TestCase):
    def test_cli_emits_compact_json_array(self):
        # Exercise the CLI end-to-end against a local file:// URL fixture.
        import tempfile

        with tempfile.NamedTemporaryFile(suffix=".xml", delete=False) as tmp:
            tmp.write(FLAT_URLSET)
            tmp_path = tmp.name
        try:
            file_url = Path(tmp_path).as_uri()
            proc = subprocess.run(
                [sys.executable, "-m", "claude_tooling.sitemap_parser", "--url", file_url, "--prefix", "/news/"],
                capture_output=True,
                text=True,
                timeout=20,
            )
            self.assertEqual(proc.returncode, 0)
            payload = json.loads(proc.stdout)
            self.assertEqual(len(payload), 2)
            self.assertNotIn(" ", proc.stdout.strip().replace('", "', '","'))  # sanity: no pretty-printed spacing
        finally:
            Path(tmp_path).unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
