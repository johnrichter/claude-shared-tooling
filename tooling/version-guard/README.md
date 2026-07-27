# version-guard — SC-VERSIONING enforcement

Stdlib-only, no install: run it from a checkout. Enforces two rules from this repo's
versioning convention (README's "Versioning" section):

1. **Tag-prefix/module-path parity.** A module tag is `<module-path>/[v]X.Y.Z`, or bare
   `[v]X.Y.Z` for the top-level Python package. The prefix must name a real module path in
   the tree — the `go/<name>` convention documented in the top-level README generalizes to
   every module kind (`rust/<crate>`, `schemas/<name>`, and any future language directory).
2. **Rust cross-module dependencies are git-tag dependencies, never `path = ...`.** A path
   dependency only resolves inside the checkout that wrote it; it can't survive a module
   being tagged and consumed independently of its neighbor.

## Commands

| Command | Does |
|---|---|
| `check-tag <tag>` | Rejects a tag whose path prefix isn't a real module path. |
| `check-deps` | Rejects any Rust `path = ...` dependency found in any `Cargo.toml` in the repo. |
| `commands --module <path> --version <X.Y.Z>` | Prints the exact tag-and-release command set for that module cut (empty `--module` for the top-level package). |

```sh
python3 tooling/version-guard/check.py check-tag go/git/v1.2.0
python3 tooling/version-guard/check.py check-deps
python3 tooling/version-guard/check.py commands --module go/git --version 1.2.0
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | tag/dependency set conforms, or `commands` printed |
| 1 | a violation was found (bad tag prefix, or a path dependency), or a manifest could not be read (never a silent pass) |
| 2 | usage error — bad arguments |

## Wiring it into CI

```yaml
- name: Tag conforms to SC-VERSIONING
  if: startsWith(github.ref, 'refs/tags/')
  run: python3 tooling/version-guard/check.py check-tag "${GITHUB_REF_NAME}"

- name: No Rust path/relative dependencies
  run: python3 tooling/version-guard/check.py check-deps
```

## Layout

| Path | Role |
|---|---|
| `check.py` | Command line. Running it puts this directory on the import path, so the package below resolves with no install. |
| `version_guard/tag.py` | Parses a tag, and checks its prefix against the real module tree. |
| `version_guard/deps.py` | Scans every `Cargo.toml` in the repo for `path = ...` dependencies. |
| `version_guard/commands.py` | The canonical tag-name and tag-and-release command rendering. |

## Notes for anyone extending it

- **`check-deps` is a line-oriented scan, not a TOML parser.** It tracks the current
  `[section]` and flags a `path = "..."` key inside a dependency-shaped section
  (`dependencies`, `dev-dependencies`, `build-dependencies`, their `target.*` and
  workspace-scoped forms, and `[dependencies.<name>]` dotted subtables), including the
  single-line inline-table form (`name = { path = "...", ... }`). A `path` dependency
  spread across a multi-line inline table is not caught — none exist in this repo today;
  keep dependency inline tables on one line so the scan stays exact.
- **A module is recognized by a language manifest** (`go.mod`, `Cargo.toml`,
  `pyproject.toml`) directly inside it, or — for schema modules, which carry no manifest —
  by being an immediate child of `schemas/`. The top-level module is the repo root itself,
  recognized by its `pyproject.toml`.
- **`rust/frontmatter`'s dependency on `facetquery`** is a git-tag dependency
  (`rust/facetquery/v0.1.0`), not a `path = ...` dependency — both crates are workspace
  members released together, so `rust/Cargo.toml` carries a `[patch]` entry redirecting
  that git source back to the in-tree member for local builds and CI, while the manifest
  itself stays git-tag-only per this rule.
