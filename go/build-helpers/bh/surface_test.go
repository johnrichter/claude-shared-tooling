package bh

import (
	"testing"
	"testing/fstest"
)

// Satisfied surface: every declared entry present per its kind, required entries non-trivial.
func TestVerifyFileSurfaceSatisfied(t *testing.T) {
	fsys := fstest.MapFS{
		"cmd/report/main.go":       {Data: []byte("package main\n")},
		"internal/serializer/x.go": {Data: []byte("package serializer\n")},
		"docs/notes.md":            {Data: []byte("hi\n")},
		"emptydir/.keep":           {Data: []byte("x\n")},
	}
	entries := []FileSurfaceEntry{
		{Path: "cmd/report/*.go", Kind: FSGlob, Required: true},
		{Path: "internal/serializer/*.go", Kind: FSGlob},
		{Path: "docs/notes.md", Required: true},
		{Path: "emptydir", Kind: FSDir},
	}
	res := VerifyFileSurface(fsys, entries)
	if !res.OK {
		t.Fatalf("expected ok=true, got violations: %+v", res.Violations)
	}
}

// A missing required file fails.
func TestVerifyFileSurfaceMissingRequiredFile(t *testing.T) {
	fsys := fstest.MapFS{}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "docs/notes.md", Required: true}})
	if res.OK {
		t.Fatal("missing required file must fail")
	}
}

// A glob with zero matches fails.
func TestVerifyFileSurfaceEmptyGlob(t *testing.T) {
	fsys := fstest.MapFS{"unrelated.go": {Data: []byte("package x\n")}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "pkg/*.go", Kind: FSGlob}})
	if res.OK {
		t.Fatal("glob matching zero files must fail")
	}
}

// A dir entry naming a path with no children (never created — MapFS has no way to represent an
// explicitly-empty directory, so "no children" and "absent" collapse to the same fs.Stat error;
// both are the same real-world failure: the declared directory has nothing in it).
func TestVerifyFileSurfaceEmptyDir(t *testing.T) {
	fsys := fstest.MapFS{"otherdir/.keep": {Data: []byte("x\n")}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "emptydir", Kind: FSDir}})
	if res.OK {
		t.Fatal("dir with no children must fail")
	}
}

// A required file with zero bytes fails (non-trivial content check).
func TestVerifyFileSurfaceRequiredEmptyFile(t *testing.T) {
	fsys := fstest.MapFS{"docs/notes.md": {Data: []byte{}}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "docs/notes.md", Required: true}})
	if res.OK {
		t.Fatal("required file with zero bytes must fail (non-trivial content)")
	}
}

// A non-required empty file passes — non-triviality is only enforced for Required entries.
func TestVerifyFileSurfaceNonRequiredEmptyFilePasses(t *testing.T) {
	fsys := fstest.MapFS{"docs/notes.md": {Data: []byte{}}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "docs/notes.md"}})
	if !res.OK {
		t.Fatalf("non-required empty file must pass (only presence is checked): %+v", res.Violations)
	}
}

// A required glob with an empty match fails.
func TestVerifyFileSurfaceRequiredGlobEmptyMatch(t *testing.T) {
	fsys := fstest.MapFS{"pkg/x.go": {Data: []byte{}}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "pkg/*.go", Kind: FSGlob, Required: true}})
	if res.OK {
		t.Fatal("required glob whose sole match is empty must fail")
	}
}

// A file entry that resolves to a directory (kind mismatch) fails.
func TestVerifyFileSurfaceFileEntryIsActuallyDir(t *testing.T) {
	fsys := fstest.MapFS{"somedir/.keep": {Data: []byte("x\n")}}
	res := VerifyFileSurface(fsys, []FileSurfaceEntry{{Path: "somedir"}})
	if res.OK {
		t.Fatal("a file-kind entry that resolves to a directory must fail")
	}
}

// An empty/nil entries slice is vacuously OK.
func TestVerifyFileSurfaceEmptyEntriesOK(t *testing.T) {
	res := VerifyFileSurface(fstest.MapFS{}, nil)
	if !res.OK || len(res.Violations) != 0 {
		t.Fatalf("nil entries must be vacuously ok, got %+v", res)
	}
}

// Multiple violations are all collected, not just the first.
func TestVerifyFileSurfaceCollectsAllViolations(t *testing.T) {
	fsys := fstest.MapFS{}
	entries := []FileSurfaceEntry{
		{Path: "a.go"},
		{Path: "pkg/*.go", Kind: FSGlob},
		{Path: "somedir", Kind: FSDir},
	}
	res := VerifyFileSurface(fsys, entries)
	if res.OK || len(res.Violations) != 3 {
		t.Fatalf("expected 3 violations (one per entry), got %+v", res.Violations)
	}
}
