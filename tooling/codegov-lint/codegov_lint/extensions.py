"""The file extensions this scanner treats as authored source.

Mirrors the org's canonical code-source-extensions set (shared by the write-locus gate and
the comment gate in the governance plugins) as a local copy, since this repo does not
depend on the plugin repo directly. A path with no dot, or an extension not listed here, is
not source under this scanner.
"""
from __future__ import annotations

EXTENSIONS = frozenset(
    {
        "asm", "bash", "c", "cc", "cjs", "clj", "cljc", "cljs", "cpp", "cr", "cs", "csh",
        "cts", "cxx", "d", "dart", "elm", "erl", "ex", "exs", "fish", "fs", "fsi", "fsx",
        "go", "groovy", "gvy", "h", "hh", "hpp", "hrl", "hs", "hxx", "java", "jl", "js",
        "jsx", "ksh", "kt", "kts", "lhs", "lua", "m", "mjs", "mm", "mts",
        "nim", "php", "pl", "pm", "ps1", "psd1", "psm1", "py", "pyi", "pyw", "r", "rb",
        "rs", "s", "scala", "sh", "sql", "svelte", "swift", "tcl", "ts", "tsx", "v", "vue",
        "zig", "zsh",
    }
)


def extension_of(path: str) -> str:
    """Return the lowercased final dot-segment of `path`, or "" if it has none."""
    name = path.rsplit("/", 1)[-1]
    if "." not in name:
        return ""
    return name.rsplit(".", 1)[-1].lower()


def is_source(path: str) -> bool:
    """True if `path`'s extension is in the tracked source-extension set."""
    return extension_of(path) in EXTENSIONS
