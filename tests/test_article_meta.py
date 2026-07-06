#!/usr/bin/env python3
"""Offline unit tests for article_meta.py — no network. Verifies deterministic, verbatim
extraction and null-safe degradation (the anti-hallucination contract)."""

import unittest

from ai_shared_lib_public import article_meta


class ExtractMetaTests(unittest.TestCase):
    def test_prefers_og_over_title_tag(self):
        html = """
        <html><head>
        <title>Fallback Title | Anthropic</title>
        <meta property="og:title" content="Introducing Claude">
        <meta property="og:description" content="A short factual excerpt from the page.">
        <meta property="article:published_time" content="2026-07-01T10:00:00Z">
        </head><body>Body text.</body></html>
        """
        r = article_meta.extract_meta(html)
        self.assertEqual(r["title"], "Introducing Claude")
        self.assertEqual(r["published"], "2026-07-01T10:00:00Z")
        self.assertEqual(r["excerpt"], "A short factual excerpt from the page.")

    def test_falls_back_to_title_tag_and_time_element(self):
        html = """
        <html><head><title>Only Title Here</title></head>
        <body><time datetime="2026-06-30">June 30</time></body></html>
        """
        r = article_meta.extract_meta(html)
        self.assertEqual(r["title"], "Only Title Here")
        self.assertEqual(r["published"], "2026-06-30")
        self.assertIsNone(r["excerpt"])  # no description tag -> null, never invented

    def test_name_and_property_both_supported(self):
        html = """
        <html><head>
        <meta name="description" content="Name-based description.">
        <meta name="date" content="2026-05-01">
        </head><body></body></html>
        """
        r = article_meta.extract_meta(html)
        self.assertEqual(r["excerpt"], "Name-based description.")
        self.assertEqual(r["published"], "2026-05-01")

    def test_html_entities_unescaped(self):
        html = ('<html><head>'
                '<meta property="og:title" content="Claude &amp; Tools: &quot;news&quot;">'
                '<meta property="og:description" content="A &lt;tag&gt; &amp; more.">'
                '</head></html>')
        r = article_meta.extract_meta(html)
        self.assertEqual(r["title"], 'Claude & Tools: "news"')
        self.assertEqual(r["excerpt"], "A <tag> & more.")

    def test_all_absent_yields_all_null(self):
        r = article_meta.extract_meta("<html><head></head><body>no meta</body></html>")
        self.assertIsNone(r["title"])
        self.assertIsNone(r["published"])
        self.assertIsNone(r["excerpt"])

    def test_first_occurrence_wins(self):
        html = ('<html><head>'
                '<meta property="og:description" content="First.">'
                '<meta property="og:description" content="Second.">'
                '</head></html>')
        self.assertEqual(article_meta.extract_meta(html)["excerpt"], "First.")

    def test_malformed_html_degrades_not_raises(self):
        # Unclosed tags / garbage must not raise — extractor returns what it parsed.
        r = article_meta.extract_meta("<html><head><meta property='og:title' content='X'><body><p>oops")
        self.assertEqual(r["title"], "X")

    def test_published_priority_published_time_over_modified(self):
        html = ('<html><head>'
                '<meta property="article:modified_time" content="2026-07-02T00:00:00Z">'
                '<meta property="article:published_time" content="2026-07-01T00:00:00Z">'
                '</head></html>')
        # published_time has higher priority than modified_time regardless of document order
        self.assertEqual(article_meta.extract_meta(html)["published"], "2026-07-01T00:00:00Z")


class ArticleMetaFailureTests(unittest.TestCase):
    def test_fetch_failure_returns_full_shape_all_null(self):
        # Point at an unroutable URL; fetch_html catches and returns None -> all fields null, url preserved.
        r = article_meta.article_meta("http://127.0.0.1:9/definitely-not-listening")
        self.assertEqual(r["url"], "http://127.0.0.1:9/definitely-not-listening")
        self.assertIsNone(r["title"])
        self.assertIsNone(r["published"])
        self.assertIsNone(r["excerpt"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
