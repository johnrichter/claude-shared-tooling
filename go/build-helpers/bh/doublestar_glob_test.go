package bh

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// Adversarial fixtures proving the doublestar swap actually fixes the documented
// fs.Glob-lacks-`**` gap, not just that VerifyFileSurface still passes trivial cases. Each fixture
// first pins fs.Glob's failure on the exact same pattern+tree (so the baseline gap is visible and
// this test would have failed before the swap), then asserts VerifyFileSurface now matches it.

// `**` at the front must recurse across an arbitrary number of path separators — fs.Glob's `*`
// matches within one path segment only and cannot express this.
func TestAdv_DoubleStarRecursesAcrossSeparators(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/a.go":            {Data: []byte("package pkg\n")},
		"pkg/sub/b.go":        {Data: []byte("package pkg\n")},
		"pkg/sub/deep/c.go":   {Data: []byte("package pkg\n")},
		"other/unrelated.txt": {Data: []byte("x\n")},
	}
	pattern := "pkg/**/*.go"

	// Baseline: fs.Glob does not support `**` — it treats it as a literal/single-segment `*` and
	// misses the nested matches, proving the gap this task fixes.
	fsMatches, err := fs.Glob(fsys, pattern)
	if err != nil {
		t.Fatalf("fs.Glob unexpectedly errored on baseline pattern: %v", err)
	}
	if len(fsMatches) >= 3 {
		t.Fatalf("baseline assumption violated: fs.Glob was expected to under-match `**`, got %v (gap may already be fixed elsewhere)", fsMatches)
	}

	// Fixed behavior: VerifyFileSurface (now backed by doublestar) matches all three nested files.
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: pattern, Kind: FSGlob, Required: true}})
	if !res.OK {
		t.Fatalf("expected `**` glob to match nested files via doublestar, got violations: %+v", res.Violations)
	}
}

// `dir/**` (trailing double-star, no further segment) must match every file at any depth under
// dir, including files directly inside dir itself — the exact shape named in the task's declared
// file_surface glob entries.
func TestAdv_DirDoubleStarMatchesEveryDepth(t *testing.T) {
	fsys := fstest.MapFS{
		"go/build-helpers/bh/surface.go":     {Data: []byte("package bh\n")},
		"go/build-helpers/go.mod":            {Data: []byte("module x\n")},
		"go/build-helpers/testdata/nested/f": {Data: []byte("x\n")},
	}
	pattern := "go/build-helpers/**"
	deepFile := "go/build-helpers/bh/surface.go"

	// Baseline: fs.Glob's `**` degrades to one-segment `*`, so it lists build-helpers' immediate
	// children (bh, go.mod, testdata) but never descends into bh/ to find surface.go.
	fsMatches, err := fs.Glob(fsys, pattern)
	if err != nil {
		t.Fatalf("fs.Glob unexpectedly errored on baseline pattern: %v", err)
	}
	for _, m := range fsMatches {
		if m == deepFile {
			t.Fatalf("baseline assumption violated: fs.Glob was expected to miss the nested file %q, got matches %v (gap may already be fixed elsewhere)", deepFile, fsMatches)
		}
	}

	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: pattern, Kind: FSGlob, Required: true}})
	if !res.OK {
		t.Fatalf("expected `dir/**` to match every nested file via doublestar, got violations: %+v", res.Violations)
	}
}

// A `**` pattern with genuinely zero matches must still fail — the fix must not turn the
// zero-match glob failure path into a false pass.
func TestAdv_DoubleStarZeroMatchesStillFails(t *testing.T) {
	fsys := fstest.MapFS{"unrelated/x.go": {Data: []byte("package x\n")}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "nomatch/**/*.go", Kind: FSGlob}})
	if res.OK {
		t.Fatal("a `**` glob with zero real matches must still fail")
	}
}
