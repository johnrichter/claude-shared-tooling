# go — README-freshness manifest routine

Sensitivity: **public** · Owner: **public** · Kind: **shared Go module**

The platform's first Go module in this repo. Isolated under `go/` so it never entangles the
Python package (`ai_shared_lib_public/`) that lives at repo root — separate `go.mod`, separate
build/test lifecycle.

**Module path:** `gitlab.com/john-richter/ai/shared-tooling/go` — derived from this repo's git
remote (`git@gitlab.com:john-richter/ai/shared-tooling.git`) plus a `/go` suffix, matching the
module's `go/` subdirectory in the repo.

**Pinning by git tag (source importers):** because this module lives in a subdirectory, Go
requires its version tags to be **prefixed with that subdirectory** — tag releases `go/vX.Y.Z`
(e.g. `go/v0.1.0`), and consume with `go get gitlab.com/john-richter/ai/shared-tooling/go@go/vX.Y.Z`.
This is distinct from the repo-root `vX.Y.Z` tags the Python package uses (`v0.2.1`, …); a plain
`vX.Y.Z` tag is **not** resolvable for this Go module and `go get …@v0.2.1` will fail. (At `v2+`
the module path also gains a `/v2` major-version suffix, per Go's module rules — not yet relevant.)

## What lives here

- **`manifest/`** — the `manifest` package: the single source of the README-freshness manifest
  algorithm. A navigator README generator and each knowledge-base repo's freshness check both
  call this package (or the CLI below) rather than reimplement the hash — that's the whole point
  of putting it here first.
- **`cmd/jr-readme-manifest/`** — the CLI over that package.

## The manifest contract (drift-critical — implement exactly this)

A folder's manifest is a `sha256` over its canonically-sorted **source-input set**:

**Input set** — every **direct child file** of the folder (non-recursive). Excludes:
- the folder's own `README.md`
- dotfiles (names starting `.`)
- subdirectories (and everything under them)

This means editing a README's human-authored prose, or that of a file in a subdirectory, never
changes the folder's own manifest — only the folder's direct-child files' frontmatter does.

**Per child file** — extract frontmatter `name` and `description` by a line-scan of the leading
`---` … `---` block (the same convention `scripts/check_privacy.py` uses elsewhere in this repo:
simple line matching, not a full YAML parser). The opening `---` must have a **matching closing
`---`** before EOF for the block to count as frontmatter at all — an opening fence with no closer
(e.g. a lone `---` used as a horizontal rule) is NOT a frontmatter block; the file contributes
empty name/description, exactly as if it had no fence at all. (An unterminated fence must never
line-scan the rest of the file's body for `key: value`-shaped lines — that would let unrelated
prose forge a field.) Each value is a single-line scalar per the workspace frontmatter schema:
- strip surrounding whitespace
- if the value is wrapped in double quotes, strip them and unescape `\"` to `"`
- a file with no frontmatter block, or a field absent from the block, contributes `""` for that
  field — never an error

**Preimage** — sort child filenames in **byte order** (not locale-aware). For each entry, in
sorted order, feed three fields into the running sha256 hash — the child's **filename**, its
frontmatter **name**, its frontmatter **description**, in that order — each field written as an
**8-byte big-endian length prefix followed by the field's raw UTF-8 bytes** (no delimiter byte
between fields or between entries):

```
entry := len(filename) || filename || len(name) || name || len(description) || description
preimage := entry(sorted[0]) || entry(sorted[1]) || ...
manifest := lowercase_hex(sha256(preimage))
```

where `len(x)` is `x`'s byte length as an 8-byte big-endian `uint64`, and `||` is byte
concatenation. The manifest is the **lowercase hex sha256** of the full preimage, presented as
`sha256:<hex>`.

**Why length-prefixing, not a delimiter byte:** an earlier version of this contract used a raw
`\x1f` (unit separator) as an unescaped field delimiter — `<filename>\x1f<name>\x1f<description>\n`.
That encoding is **not injective**: a literal `\x1f` byte inside a frontmatter scalar is
indistinguishable from a delimiter boundary, so two different `(name, description)` tuples can
serialize to byte-identical preimage lines and collide on the same digest, defeating the hash's
purpose. Length-prefixing removes the ambiguity structurally — a reader always knows a field's
exact byte length before reading it, so no byte value the field's content might contain (`\x1f`,
`\n`, or anything else) can ever be mistaken for a boundary. Two distinct field sequences can
never produce the same length-prefixed byte string.

## CLI

```
jr-readme-manifest manifest <dir>   # print the folder's current manifest: sha256:<hex>
jr-readme-manifest check <dir>      # recompute and compare against <dir>/README.md's
                                     # <!-- manifest: sha256:... --> marker line;
                                     # exit 0 on match, 1 on mismatch (prints expected vs actual)
```

Pure computation only — no network, no model, no LLM call, in either subcommand.

## Zero runtime toolchain reliance

Consumers call a **pre-compiled binary**, never `go build` and never a Python interpreter:

- `.bin/jr-readme-manifest-<os>-<arch>` — one binary per `{darwin,linux} x {arm64,amd64}`, built
  with `go build -trimpath` for a reproducible build, committed via git LFS (`.gitattributes`
  routes `.bin/jr-readme-manifest-*`).
- `.bin/jr-readme-manifest` — a tiny POSIX-sh selector that `exec`s the right binary for the
  current platform (maps `uname -s`/`uname -m`; errors clearly on an unsupported platform).

To rebuild the binaries after a change to `manifest/` or `cmd/`:

```sh
for os in darwin linux; do
  for arch in arm64 amd64; do
    GOOS=$os GOARCH=$arch go -C go build -trimpath -o ".bin/jr-readme-manifest-$os-$arch" ./cmd/jr-readme-manifest
  done
done
```

## Development

```sh
cd go
go vet ./...
go test ./... -count=1
```

`manifest/testdata/fixture/` is a golden fixture folder with a hardcoded expected digest in
`manifest/manifest_test.go` — if a contract-conforming code change changes that digest, the
contract itself changed and the fixture/test constant must be updated deliberately, not silently.
