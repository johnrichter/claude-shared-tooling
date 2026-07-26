"""Python API-doc-comment enforcement.

Delegated to ruff's pydocstyle rules (D) in Google convention — the trusted,
actively-maintained implementation of that check, rather than a hand-rolled docstring
parser. Requires `ruff` on PATH, installed from this package's `requirements.txt` into an
isolated environment, never the system/global interpreter's package set.
"""
from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

from .rules import Violation

_CONFIG = """
[lint]
select = ["D"]

[lint.pydocstyle]
convention = "google"
"""


def _display_path(path: Path, repo_root: Path) -> Path:
    """Repo-relative path, or the resolved path itself if outside `repo_root`."""
    try:
        return path.relative_to(repo_root)
    except ValueError:
        return path


def ruff_available() -> bool:
    """True if a `ruff` executable is on PATH."""
    return shutil.which("ruff") is not None


def scan_python_docstrings(paths: list[Path], repo_root: Path, config_path: Path) -> list[Violation]:
    """Run ruff's Google-convention docstring rules over `paths`, return violations."""
    py_paths = [p for p in paths if p.suffix == ".py"]
    if not py_paths:
        return []
    config_path.write_text(_CONFIG, encoding="utf-8")
    result = subprocess.run(
        ["ruff", "check", "--config", str(config_path), "--output-format", "json", *map(str, py_paths)],
        cwd=repo_root,
        capture_output=True,
        text=True,
    )
    if not result.stdout.strip():
        return []
    diagnostics = json.loads(result.stdout)
    violations = [
        Violation(
            path=str(_display_path(Path(d["filename"]).resolve(), repo_root)),
            line=d["location"]["row"],
            rule="MISSING-API-DOC",
            detail=f"{d['code']} {d['message']}",
        )
        for d in diagnostics
    ]
    violations.sort(key=lambda v: (v.path, v.line, v.rule))
    return violations
