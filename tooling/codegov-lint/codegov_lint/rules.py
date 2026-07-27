"""The code-authoring policy's banned-content and doc-presence rules, as compiled patterns.

Each rule below targets exactly the phrasing the comment-authoring policy bans, scoped
narrowly enough to leave ordinary engineering prose alone: a rule fires on an explicit
citation shape (a named source language, a plan/task id, a file:line pointer, a full
code-shaped statement sitting alone in a comment) rather than on any word that could
plausibly appear in one.
"""
from __future__ import annotations

import re
from dataclasses import dataclass

_LANG = (
    r"(?:python|golang|go|rust|javascript|typescript|java|ruby|c\+\+|csharp|c#|"
    r"kotlin|swift|php|scala)"
)

PORT_ARCHAEOLOGY = [
    re.compile(rf"\b(?:ported|porting|adapted|translated)\s+(?:from|to)\s+(?:the\s+)?{_LANG}\b", re.I),
    re.compile(rf"\bwas\s+(?:originally\s+|previously\s+)?(?:written|implemented)\s+in\s+(?:the\s+)?{_LANG}\b", re.I),
    re.compile(rf"\bmirrors\s+the\s+{_LANG}\b.{{0,40}}\b(?:version|implementation|source)\b", re.I),
    re.compile(r"\b[\w-]+\.(?:py|go|rs|ts|tsx|js|jsx|rb|java|kt|kts|swift|cs|php|scala):\d+\b"),
]

MILESTONE_ID = [
    re.compile(r"\bM\d+(?:\.P\d+)?(?:\.T\d+)?\b"),
    re.compile(r"\b(?:Milestone|Phase|Task)\s+\d+\b", re.I),
    re.compile(r"\bSC-[A-Z][A-Z0-9]{2,}\b"),
]

# Every pattern below requires an actual code-shaped terminator or grammar (a statement
# punctuation mark, a dotted import path, a call's parentheses) alongside its leading
# keyword — a bare keyword prefix is not enough, since ordinary prose routinely opens a
# sentence with "if", "return", "use", "for", and the like ("If unset, defaults to ten").
_DEAD_CODE_BLOCK = re.compile(
    r"^(?:def|class|function|func|public|private|protected|static|"
    r"if|elif|else|for|while|switch|try|except|finally|catch|with|"
    r"return|package|var|let|const|pub\s+fn|pub\s+struct|pub\s+enum)\b.*[:{};]\s*$"
)
_DEAD_CODE_CLOSE_BRACE = re.compile(r"^\}\s*(?:else\b.*)?\{?\s*;?\s*$")
_DEAD_CODE_STATEMENT = re.compile(r"^[\w.\[\]]+\s*=\s*\S.*;\s*$")
_DEAD_CODE_BARE_CALL = re.compile(r"^\w+\([^)]*\)\s*;?\s*$")
_DEAD_CODE_IMPORT = re.compile(
    r"^from\s+[\w]+(?:\.[\w]+)*\s+import\s+[\w*]+(?:\s*,\s*[\w]+)*\s*$"
    r"|^import\s+[\w]+(?:\.[\w]+)*(?:\s+as\s+\w+)?(?:\s*,\s*[\w]+(?:\.[\w]+)*)*\s*$"
)
_DEAD_CODE_USE = re.compile(r"^(?:pub\s+)?(?:use|mod)\s+[\w:{}, *]+;\s*$")
_DEAD_CODE_PACKAGE = re.compile(r"^package\s+[\w.]+;?\s*$")
_DEAD_CODE_INCLUDE = re.compile(r'^#include\s*[<"][\w./]+[>"]\s*$')
_DEAD_CODE_PRINT_CALL = re.compile(
    r"^(?:println!|console\.log|System\.out\.println)\s*\([^)]*\)\s*;?\s*$"
)

DEAD_CODE = [
    _DEAD_CODE_BLOCK,
    _DEAD_CODE_CLOSE_BRACE,
    _DEAD_CODE_STATEMENT,
    _DEAD_CODE_BARE_CALL,
    _DEAD_CODE_IMPORT,
    _DEAD_CODE_USE,
    _DEAD_CODE_PACKAGE,
    _DEAD_CODE_INCLUDE,
    _DEAD_CODE_PRINT_CALL,
]


@dataclass(frozen=True)
class Violation:
    """One rule hit: which file/line, which rule fired, and the offending text."""

    path: str
    line: int
    rule: str
    detail: str


def scan_comment(path: str, line: int, text: str) -> list[Violation]:
    """Run every banned-content rule against one extracted comment's text."""
    violations = []
    for pattern in PORT_ARCHAEOLOGY:
        if pattern.search(text):
            violations.append(Violation(path, line, "PORT-ARCHAEOLOGY", text))
            break
    for pattern in MILESTONE_ID:
        if pattern.search(text):
            violations.append(Violation(path, line, "MILESTONE-ID", text))
            break
    for i, comment_line in enumerate(text.splitlines() or [text]):
        stripped = comment_line.strip()
        if not stripped:
            continue
        if any(p.match(stripped) for p in DEAD_CODE):
            violations.append(Violation(path, line + i, "DEAD-CODE", stripped))
    return violations


_GO_EXPORTED = re.compile(r"^(func|type|const|var)\s+([A-Z]\w*)")
_RUST_PUB = re.compile(r"^\s*pub\s+(fn|struct|enum|trait|type|const|static|mod)\s+(\w+)")


def scan_doc_presence(path: str, extension: str, lines: list[str]) -> list[Violation]:
    """Flag an exported Go/Rust declaration with no doc comment on the line directly above.

    Presence-only: it checks a doc comment exists, not its content or format — Rust's own
    rustdoc gate and Go's godoc convention govern format, this only backstops a class those
    per-language gates already require but a task could still land without.
    """
    if extension == "go":
        return _scan_doc_presence(path, lines, _GO_EXPORTED, "//")
    if extension == "rs":
        return _scan_doc_presence(path, lines, _RUST_PUB, ("///", "//!"), attr_prefix="#[")
    return []


def _scan_doc_presence(
    path: str,
    lines: list[str],
    declaration: re.Pattern,
    doc_markers,
    attr_prefix: str | None = None,
) -> list[Violation]:
    if isinstance(doc_markers, str):
        doc_markers = (doc_markers,)
    violations = []
    for i, line in enumerate(lines):
        if not declaration.match(line):
            continue
        j = i - 1
        while attr_prefix and j >= 0 and lines[j].strip().startswith(attr_prefix):
            j -= 1
        has_doc = j >= 0 and lines[j].strip().startswith(doc_markers)
        if not has_doc:
            violations.append(Violation(path, i + 1, "MISSING-API-DOC", line.strip()))
    return violations
