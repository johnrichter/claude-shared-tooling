# codegov-lint — SC-CODEGOV code-authoring gate

Mechanically enforces the org's code-comment policy (`governance-code-authoring`) across
every authored-source language: a doc comment on every exported/public symbol, and no
comment carrying cross-language-port archaeology, an embedded milestone/phase/task/spec id,
or a narrated line of commented-out code.

## Two engines, one report

- **`codegov_lint`** (this package) extracts every comment/docstring span per file (line
  comments, block comments, and — for Python — triple-quoted docstring bodies) and runs it
  against the three banned-content rules, plus a presence-only doc-comment check for Go/Rust
  exported declarations. Stdlib-only.
- **ruff** (pinned in `requirements.txt`), pydocstyle rules (`D`) in Google convention, is
  the API-doc-comment check for Python — the trusted, actively-maintained implementation of
  that rule rather than a hand-rolled docstring parser. Install into an isolated venv, never
  the system interpreter:

  ```sh
  python3 -m venv .venv && .venv/bin/pip install -r tooling/codegov-lint/requirements.txt
  ```

Go's and Rust's own per-language doc gates (`cargo doc -D warnings` + clippy, `golangci-lint`)
already enforce godoc/rustdoc format and completeness in this repo's CI; this tool's Go/Rust
check is a presence-only backstop for the same rule, not a duplicate of those gates' style
checks.

## Usage

```sh
python3 tooling/codegov-lint/check.py --diff <ref>   # files changed since <ref> — CI mode
python3 tooling/codegov-lint/check.py --files a.py b.go
python3 tooling/codegov-lint/check.py --all           # every tracked source file — audit only
```

Exit codes: `0` no violations; `1` one or more violations found (each printed as
`path:line: RULE: detail`); `2` the harness itself could not run.

## Why `--diff`, not a full-tree scan, gates CI

This tree predates the code-authoring policy: it already carries thousands of pre-existing
Python docstring gaps and, since `go/build-helpers` domain-models plan/task ids as data,
legitimate `M<n>.P<n>.T<n>`-shaped literals throughout its comments and tests. A full-tree
scan run as a required check would fail on that backlog on day one, for work no current task
touches. `--diff` scopes enforcement to what a task actually changes — new code stops adding
violations immediately, without an unbounded retroactive remediation project blocking
unrelated work. `--all` stays available for periodic audits of the backlog itself.

## Known heuristic limits

- Quote-stripping (to avoid mistaking a `#`/`//` inside a string literal for a comment
  marker) is single-line and regex-based, not a real tokenizer — an edge case (a marker
  character inside a multi-line string) can still slip through as a false comment.
- The milestone/phase/task-id and dead-code rules are pattern-based, not semantic: a
  standalone identifier that happens to match `M\d+` shape, or a comment sentence that
  happens to read like a code statement, can trip a rule despite having nothing to do with
  process provenance or disabled code. In `--diff` mode (the wired CI path) this is a
  reasonable trade against missing a real violation in new work.
- The Go/Rust doc-presence check only looks at the line immediately above a declaration
  (skipping Rust attribute lines) — a doc comment separated by a blank line, or a grouped
  Go `const ( ... )`/`var ( ... )` block, is out of scope.

## CI wiring

`.github/workflows/codegov.yml`'s `codegov` job runs `check.py --diff` against the PR's
base ref (or the previous commit, on a direct push to `main`) — a required check standing
alongside `ci.yml`'s job graph. It's a separate workflow file (not a job inside `ci.yml`)
so it has its own stable check name; since it can't join `ci.yml`'s `guardrail`-gated
`needs:` chain across files, it re-runs the same secret/binary guardrail scan as its first
two steps before installing anything or linting.
