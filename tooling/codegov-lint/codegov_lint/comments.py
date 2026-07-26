"""Per-language comment extraction.

Groups every tracked extension into a comment-syntax family and pulls the text a human
reads as narration out of source: line comments and block comments. Extraction is
line-oriented and quote-stripping is a single-pass heuristic, not a real tokenizer — good
enough for scanning comment prose for banned phrasing, not a substitute for a language
parser. Python is the one exception: its docstrings are pulled via the standard library's
`ast` module instead of a quote-matching regex, since a regex has no reliable way to tell a
literal triple-quote sequence sitting inside an ordinary string (for example, one written
into a regex pattern) from an actual docstring delimiter. Python's `#` line comments are
still scanned the same line-oriented way as every other hash-comment language, alongside
its docstrings.
"""
from __future__ import annotations

import ast
import re
from dataclasses import dataclass

_HASH = frozenset(
    {
        "sh", "bash", "zsh", "ksh", "csh", "fish", "py", "pyi", "pyw", "rb", "pl", "pm",
        "tcl", "r", "jl", "nim", "cr", "ex", "exs", "elm", "ps1", "psd1", "psm1",
    }
)
_SLASH = frozenset(
    {
        "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx", "cs", "d", "dart", "go", "groovy",
        "gvy", "java", "js", "jsx", "cjs", "mjs", "cts", "mts", "kt", "kts", "m", "mm",
        "php", "rs", "scala", "swift", "ts", "tsx", "v", "zig", "fs", "fsi", "fsx",
        "svelte", "vue",
    }
)
_DASHDASH = frozenset({"sql", "hs", "lhs", "lua"})
_PERCENT = frozenset({"erl", "hrl"})
_SEMICOLON = frozenset({"clj", "cljc", "cljs", "asm", "s"})

FAMILY_BY_EXTENSION: dict[str, str] = {}
for _ext in _HASH:
    FAMILY_BY_EXTENSION[_ext] = "hash"
for _ext in _SLASH:
    FAMILY_BY_EXTENSION[_ext] = "slash"
for _ext in _DASHDASH:
    FAMILY_BY_EXTENSION[_ext] = "dashdash"
for _ext in _PERCENT:
    FAMILY_BY_EXTENSION[_ext] = "percent"
for _ext in _SEMICOLON:
    FAMILY_BY_EXTENSION[_ext] = "semicolon"

_LINE_MARKER = {"hash": "#", "slash": "//", "dashdash": "--", "percent": "%", "semicolon": ";"}

_QUOTE_RE = re.compile(r"""("([^"\\]|\\.)*"|'([^'\\]|\\.)*'|`([^`\\]|\\.)*`)""")
_BLOCK_COMMENT_RE = re.compile(r"/\*(.*?)\*/", re.DOTALL)
_PYTHON_EXTENSIONS = frozenset({"py", "pyi", "pyw"})


@dataclass(frozen=True)
class Comment:
    """One extracted comment (or docstring) span, 1-indexed at its first line."""

    line: int
    text: str


def _blank_quotes(line: str) -> str:
    """Replace quoted spans with equal-length filler.

    So a marker char inside a string literal (e.g. a `#` in a shell command embedded in a
    Python string) is never mistaken for the start of a comment.
    """
    return _QUOTE_RE.sub(lambda m: " " * len(m.group(0)), line)


def extract(text: str, extension: str) -> list[Comment]:
    """Return every comment/docstring span in `text`, given its file extension.

    An extension outside the families this module knows yields no comments (nothing to
    scan, not an error) — narrower coverage here only shrinks what gets checked, it never
    misattributes ordinary code as a comment.
    """
    if extension in _PYTHON_EXTENSIONS:
        lines = text.splitlines()
        return _python_docstrings(text) + list(_line_comments(lines, "#"))
    family = FAMILY_BY_EXTENSION.get(extension)
    if family is None:
        return []
    lines = text.splitlines()
    comments = list(_line_comments(lines, _LINE_MARKER[family]))
    if family == "slash":
        blanked = "\n".join(_blank_quotes(line) for line in lines)
        comments.extend(_block_comments(text, blanked, _BLOCK_COMMENT_RE))
    return comments


def _line_comments(lines: list[str], marker: str) -> list[Comment]:
    found = []
    for i, raw in enumerate(lines, start=1):
        stripped = _blank_quotes(raw)
        idx = stripped.find(marker)
        if idx == -1:
            continue
        found.append(Comment(line=i, text=raw[idx + len(marker) :].strip()))
    return found


def _block_comments(text: str, blanked: str, pattern: re.Pattern) -> list[Comment]:
    """Match `pattern` against `blanked` to find comment spans, report content from `text`.

    `blanked` has quoted spans neutralized but is the same length/offsets as `text`, so a
    match's span locates the real content to report.
    """
    found = []
    for match in pattern.finditer(blanked):
        start_line = text.count("\n", 0, match.start()) + 1
        found.append(Comment(line=start_line, text=text[match.start(1) : match.end(1)].strip()))
    return found


def _python_docstrings(text: str) -> list[Comment]:
    try:
        tree = ast.parse(text)
    except SyntaxError:
        return []
    nodes = [tree] + [
        n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))
    ]
    found = []
    for node in nodes:
        doc = ast.get_docstring(node, clean=False)
        if doc is None or not node.body:
            continue
        found.append(Comment(line=node.body[0].lineno, text=doc.strip()))
    return found
