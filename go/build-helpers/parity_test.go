package main

// Adoption parity gate: proves this promoted, canonical build-helpers is behaviorally
// identical to the genericized source it was adopted from, before anything is based on it. The
// verdict IS `go test`'s pass/fail — a failure halts the adoption for operator review rather than
// letting undetected drift ride into everything built on top of this module.
//
// Three independent checks:
//   1. Golden fixtures (bh/testdata/**) that are machine-consumed test input/output — e.g. the
//      .jsonl accounting logs — are byte-identical to the source's. Prose/manifest fixtures named
//      EXPECTED.md are human-readable rendered-output write-ups, not machine-consumed goldens; a
//      public-scrub of this promoted module legitimately rewords them (contact info, links,
//      internal references) without changing the underlying behavior, so they are excluded from
//      the byte comparison and only checked for presence on both sides.
//   2. Every committed ../.bin/build-helpers-<goos>-<goarch> hash equals a fresh `go build` from
//      HEAD — the committed binary is exactly what the current sources + toolchain
//      produce, so the execed binary can never silently diverge from the reviewed source.
//   3. The four-way model-ID enum set is identical across all four independent enumerators:
//      plan-schema.json's model.enum, the Go Model consts, anthropic-specifications.json's
//      pricing.list, and build-engine.workflow.js's DEFAULT_RATES. The "inherit" sentinel (a
//      Go/schema-only value that is never priced) is excluded from the priced-model comparison.
//
// Checks 1 and 3 read files that live only in the source tree (its testdata, its plan-schema.json,
// and its build-engine.workflow.js), so they need a pointer to the source's build-helpers module
// root via BUILD_HELPERS_PARITY_SOURCE. Absent it they skip — the standalone module stays green —
// but the adoption gate MUST run with it set. Check 2 is self-contained and always runs.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const paritySourceEnv = "BUILD_HELPERS_PARITY_SOURCE"

// modelInherit is the Go/schema-only sentinel: it names no priced model, so it is dropped before
// any set comparison against a rate table.
const modelInherit = "inherit"

// crossTargets is the committed cross-compile matrix under ../.bin, per the module README.
var crossTargets = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

// paritySourceRoot resolves the source's build-helpers module root, or skips when the gate is not
// being run against a co-located source (keeping the standalone module's `go test ./...` green).
func paritySourceRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv(paritySourceEnv)
	if root == "" {
		t.Skipf("%s unset — set it to the source's build-helpers module root to run the adoption parity gate", paritySourceEnv)
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		t.Fatalf("%s=%q is not a readable directory: %v", paritySourceEnv, root, err)
	}
	return root
}

func TestParity_GoldenFixturesMatchSource(t *testing.T) {
	srcRoot := paritySourceRoot(t)
	srcData := filepath.Join(srcRoot, "bh", "testdata")
	dstData := filepath.Join("bh", "testdata")

	src := collectFixtures(t, srcData)
	dst := collectFixtures(t, dstData)

	rels := unionKeys(src, dst)
	byteCompared := 0
	for _, rel := range rels {
		sb, inSrc := src[rel]
		db, inDst := dst[rel]
		switch {
		case !inSrc:
			t.Errorf("golden fixture %q exists in the promoted module but not in the source — adopted goldens drifted", rel)
		case !inDst:
			t.Errorf("golden fixture %q exists in the source but not in the promoted module — a golden was dropped on adoption", rel)
		case isProseFixture(rel):
			// Presence-checked above; content is allowed to diverge (e.g. public-scrub reword).
		case !bytes.Equal(sb, db):
			t.Errorf("golden fixture %q is not byte-identical to the source (%d vs %d bytes)", rel, len(sb), len(db))
		default:
			byteCompared++
		}
	}
	t.Logf("parity goldens: %d fixture files present, %d byte-compared under bh/testdata", len(rels), byteCompared)
}

func TestParity_CommittedBinariesMatchFreshBuild(t *testing.T) {
	for _, tgt := range crossTargets {
		name := fmt.Sprintf("build-helpers-%s-%s", tgt.goos, tgt.goarch)
		binPath := filepath.Join("..", ".bin", name)

		committed, err := committedBinaryHash(binPath)
		if err != nil {
			t.Errorf("%s: cannot read committed binary hash: %v", name, err)
			continue
		}
		fresh, err := freshBuildHash(t, tgt.goos, tgt.goarch)
		if err != nil {
			t.Errorf("%s: fresh build failed: %v", name, err)
			continue
		}
		if committed != fresh {
			t.Errorf("%s: committed hash %s != fresh build %s — the committed binary is stale versus HEAD sources (or the build toolchain drifted). Recompile and re-commit before adopting.", name, committed, fresh)
			continue
		}
		t.Logf("%s: committed == fresh build (%s)", name, committed)
	}
}

func TestParity_ModelIDFourWaySync(t *testing.T) {
	srcRoot := paritySourceRoot(t)

	// Source-tree offsets from the build-helpers module root: build-engine.workflow.js sits one
	// level up beside the module; plan-schema.json lives in the product-architect agent dir.
	schemaPath := filepath.Join(srcRoot, "..", "..", "..", "agents", "product-architect", "plan-schema.json")
	tieringPath := filepath.Join(srcRoot, "..", "build-engine.workflow.js")

	sources := map[string]map[string]bool{
		"plan-schema.json model.enum":                withoutInherit(schemaModelEnum(t, schemaPath)),
		"types.go Model consts":                      withoutInherit(goModelConsts(t, filepath.Join("bh", "types.go"))),
		"anthropic-specifications.json pricing.list": withoutInherit(specsPricingModels(t, filepath.Join("..", "anthropic-specifications.json"))),
		"build-engine.workflow.js DEFAULT_RATES":     withoutInherit(defaultRatesModels(t, tieringPath)),
	}

	union := map[string]bool{}
	for _, set := range sources {
		for id := range set {
			union[id] = true
		}
	}
	if len(union) == 0 {
		t.Fatal("no model IDs parsed from any of the four enumerators — parsing is broken, cannot certify sync")
	}

	for _, id := range sortedKeys(union) {
		var missing []string
		for name, set := range sources {
			if !set[id] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("model ID %q is missing from: %s — the four enumerators are out of sync; fix all four together before adopting", id, strings.Join(missing, ", "))
		}
	}
	t.Logf("four-way model-ID sync: %d priced model IDs identical across all four enumerators", len(union))
}

// --- fixtures ---

// isProseFixture reports whether a testdata-relative path is a human-readable prose/manifest
// fixture rather than a machine-consumed golden. EXPECTED.md files are rendered-output write-ups
// read by people reviewing a fixture directory, not parsed by the code under test, so they are
// exempt from the byte-identical requirement (see the parity-gate doc comment above) while every
// other fixture — the .jsonl inputs the tool actually reads — stays byte-compared.
func isProseFixture(rel string) bool {
	return filepath.Base(rel) == "EXPECTED.md"
}

// collectFixtures maps each regular file under root to its bytes, keyed by root-relative path.
// .gitattributes is skipped: it is a repo-hosting artifact, not a golden, and is intentionally
// carried only in the source tree.
func collectFixtures(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".gitattributes" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no golden fixtures found under %s", root)
	}
	return out
}

// --- binaries ---

var lfsOIDRE = regexp.MustCompile(`(?m)^oid sha256:([0-9a-f]{64})$`)

// committedBinaryHash returns the sha256 of the committed binary. Handles both a smudged binary
// (hash its bytes) and an unsmudged Git LFS pointer (read the recorded oid, which is that same
// sha256), so the check works whether or not LFS content is materialized in the working tree.
func committedBinaryHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(string(b), "version https://git-lfs.github.com/spec/v1") {
		m := lfsOIDRE.FindSubmatch(b)
		if m == nil {
			return "", fmt.Errorf("%s looks like an LFS pointer but has no oid sha256 line", path)
		}
		return string(m[1]), nil
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// freshBuildHash cross-compiles the module for the target with the README's exact reproducible
// flags and returns the sha256 of the resulting binary. -buildvcs=false is mandatory: without it
// Go stamps vcs.revision/time/modified into the binary, which vary by checkout and working-tree
// state (a worktree nested under another checkout even stamps the parent's HEAD), so a VCS-stamped
// build is inherently non-reproducible and could never satisfy the committed==fresh invariant.
//
// The hash is still tied to the exact Go toolchain that built the committed binaries; a different
// Go version can emit different bytes. The committed binaries were built with the toolchain pinned
// in the module README, so a hash mismatch under a different Go version means "rebuild with the
// pinned toolchain", not source drift. A hard toolchain assertion is intentionally omitted so the
// gate can still run under a deliberately upgraded toolchain when the binaries are recut against it.
func freshBuildHash(t *testing.T, goos, goarch string) (string, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fresh")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", out, ".")
	// CGO_ENABLED=0 is pinned rather than inherited from the host: an ambient CGO_ENABLED=1 with a
	// gcc on PATH can silently link a cgo runtime into a cross-compiled target, which would make
	// this fresh build match a similarly cgo-linked committed binary instead of catching it.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// --- model-ID enumerators (parsed independently of package bh, so this gate never shares code
// with the thing it verifies) ---

func schemaModelEnum(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw := readOrFatal(t, path)
	var doc struct {
		Defs struct {
			Task struct {
				Properties struct {
					Model struct {
						Enum []string `json:"enum"`
					} `json:"model"`
				} `json:"properties"`
			} `json:"task"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	if len(doc.Defs.Task.Properties.Model.Enum) == 0 {
		t.Fatalf("%s $defs.task.properties.model.enum is empty or missing", path)
	}
	return toSet(doc.Defs.Task.Properties.Model.Enum)
}

var goModelConstRE = regexp.MustCompile(`Model[A-Za-z0-9]+\s+Model\s*=\s*"([^"]+)"`)

func goModelConsts(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw := readOrFatal(t, path)
	m := goModelConstRE.FindAllStringSubmatch(string(raw), -1)
	if len(m) == 0 {
		t.Fatalf(`no Model consts (ModelXxx Model = "...") found in %s`, path)
	}
	ids := make([]string, len(m))
	for i, g := range m {
		ids[i] = g[1]
	}
	return toSet(ids)
}

func specsPricingModels(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw := readOrFatal(t, path)
	var doc struct {
		Pricing struct {
			List map[string]any `json:"list"`
		} `json:"pricing"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	if len(doc.Pricing.List) == 0 {
		t.Fatalf("%s pricing.list is empty or missing", path)
	}
	ids := make([]string, 0, len(doc.Pricing.List))
	for k := range doc.Pricing.List {
		ids = append(ids, k)
	}
	return toSet(ids)
}

var (
	defaultRatesBlockRE = regexp.MustCompile(`(?s)DEFAULT_RATES\s*=\s*\{(.*?)\}`)
	defaultRatesKeyRE   = regexp.MustCompile(`'([^']+)'\s*:`)
)

func defaultRatesModels(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw := readOrFatal(t, path)
	block := defaultRatesBlockRE.FindStringSubmatch(string(raw))
	if block == nil {
		t.Fatalf("could not find `DEFAULT_RATES = { ... }` in %s", path)
	}
	keys := defaultRatesKeyRE.FindAllStringSubmatch(block[1], -1)
	if len(keys) == 0 {
		t.Fatalf("DEFAULT_RATES block in %s yielded no keys", path)
	}
	ids := make([]string, len(keys))
	for i, g := range keys {
		ids[i] = g[1]
	}
	return toSet(ids)
}

// --- small helpers ---

func readOrFatal(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return b
}

func toSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

func withoutInherit(s map[string]bool) map[string]bool {
	delete(s, modelInherit)
	return s
}

func unionKeys(a, b map[string][]byte) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	return sortedKeys(seen)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
