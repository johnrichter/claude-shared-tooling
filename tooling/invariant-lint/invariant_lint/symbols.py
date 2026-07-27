"""Rung-1 resolution: does a declared `file:symbol` name a real definition?

Each source language gets a definition-anchored resolver rather than a substring search,
so a symbol that appears only in a comment or a string never counts as resolved. Python
is resolved through its own AST, which also lets a dotted symbol (`Class.method`) navigate
into a class body. Go and Rust are resolved with regexes anchored to the start of a
declaration line, which a `//` or `#` comment line can never match since the anchor
requires the declaring keyword (or, for a grouped var/const block, the symbol itself) to be
the first token. Any other extension falls back to a comment-stripped whole-word search.
"""
from __future__ import annotations

import ast
import re
from pathlib import Path

_COMMENT_PREFIXES = ("#", "//", "--", ";", "%")


def _strip_comment_lines(text: str) -> list[str]:
    """Lines of `text` with a leading full-line comment (any common syntax) blanked out."""
    kept = []
    for line in text.splitlines():
        if line.strip().startswith(_COMMENT_PREFIXES):
            kept.append("")
        else:
            kept.append(line)
    return kept


def _resolve_go(text: str, symbol: str) -> bool:
    """A Go func/method, type, or top-level/grouped var or const named `symbol`."""
    name = re.escape(symbol)
    patterns = [
        rf"^\s*func\s*(?:\([^)]*\)\s*)?{name}\s*(?:\[[^\]]*\])?\s*\(",
        rf"^\s*type\s+{name}\b",
        rf"^\s*(?:var|const)\s+{name}\b",
        # A grouped `var ( ... )` / `const ( ... )` block names each entry on its own line
        # with no leading keyword — the symbol itself is the first token.
        rf"^\s*{name}\s+\S",
        rf"^\s*{name}\s*=",
    ]
    combined = re.compile("|".join(patterns), re.MULTILINE)
    return combined.search("\n".join(_strip_comment_lines(text))) is not None


def _resolve_rust(text: str, symbol: str) -> bool:
    """A Rust fn, struct, enum, trait, const, static, or type item named `symbol`."""
    name = re.escape(symbol)
    modifiers = r'(?:pub(?:\([^)]*\))?\s+)?(?:default\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]*"\s+)?'
    patterns = [
        rf"^\s*{modifiers}fn\s+{name}\s*[<(]",
        rf"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|const|static(?:\s+mut)?|type)\s+{name}\b",
    ]
    combined = re.compile("|".join(patterns), re.MULTILINE)
    return combined.search("\n".join(_strip_comment_lines(text))) is not None


def _resolve_python(text: str, symbol: str) -> bool:
    """A module-level def/class, or a `Class.method` path navigated through the AST."""
    try:
        tree = ast.parse(text)
    except SyntaxError:
        return False

    def_types = (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)

    def find(body: list[ast.stmt], name: str) -> ast.stmt | None:
        for node in body:
            if isinstance(node, def_types) and node.name == name:
                return node
        return None

    parts = symbol.split(".")
    scope = tree.body
    node: ast.stmt | None = None
    for part in parts:
        node = find(scope, part)
        if node is None:
            return False
        scope = getattr(node, "body", [])
    return True


def _resolve_generic(text: str, symbol: str) -> bool:
    """Fallback for a language with no dedicated resolver: a whole-word match outside comments."""
    pattern = re.compile(rf"\b{re.escape(symbol)}\b")
    return any(pattern.search(line) for line in _strip_comment_lines(text))


_RESOLVERS = {
    "go": _resolve_go,
    "rs": _resolve_rust,
    "py": _resolve_python,
}


def resolve_symbol(path: Path, symbol: str) -> tuple[bool, str]:
    """Resolve `symbol` in the file at `path`. Returns (resolved, reason-if-not).

    A missing file is reported the same way a renamed-away symbol is: both mean the
    fail-fast this entry names is no longer where the registry says it is.
    """
    if not path.is_file():
        return False, f"{path} does not exist"
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError as exc:
        return False, f"{path}: {exc}"

    ext = path.suffix.lstrip(".").lower()
    resolver = _RESOLVERS.get(ext, _resolve_generic)
    if resolver(text, symbol):
        return True, ""
    return False, f"{symbol!r} does not resolve in {path} (renamed, moved, or deleted)"
