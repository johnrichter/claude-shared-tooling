package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteAtomicRoundTrip writes then overwrites a file and confirms the
// contents land and no temp file is left behind.
func TestWriteAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteAtomic(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
	// A second write never leaves a stray temp file behind.
	if err := WriteAtomic(path, []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteAtomic (overwrite): %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (temp file leaked?)", len(entries))
	}
}

// TestMoveRenamesInPlace confirms Move relocates a file so the source is gone
// and the destination holds the original bytes.
func TestMoveRenamesInPlace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Move(src, dst); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("src still exists after Move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("got %q, want %q", got, "payload")
	}
}

// TestClassifyPathEmptyRuleset confirms an empty ruleset yields no class.
func TestClassifyPathEmptyRuleset(t *testing.T) {
	result := ClassifyPath("anything.txt", nil)
	if result.Class != "" {
		t.Fatalf("Class = %q, want empty for empty ruleset", result.Class)
	}
}

// TestClassifyPathOverlapLastWins confirms overlapping rules resolve by list
// order, with the last matching rule winning.
func TestClassifyPathOverlapLastWins(t *testing.T) {
	rules := []Rule{
		{Pattern: "**/*.go", Class: "source"},
		{Pattern: "**/*_test.go", Class: "test"},
	}
	if got := ClassifyPath("pkg/foo_test.go", rules).Class; got != "test" {
		t.Fatalf("Class = %q, want %q", got, "test")
	}
	if got := ClassifyPath("pkg/foo.go", rules).Class; got != "source" {
		t.Fatalf("Class = %q, want %q", got, "source")
	}
}

// TestClassifyPathMalformedDemotedNotDropped confirms a malformed pattern fails
// closed (matches) and is reported in Malformed rather than silently dropped.
func TestClassifyPathMalformedDemotedNotDropped(t *testing.T) {
	rules := []Rule{
		{Pattern: "[", Class: "broken"},
	}
	result := ClassifyPath("anything.txt", rules)
	if result.Class != "broken" {
		t.Fatalf("Class = %q, want fail-closed match %q", result.Class, "broken")
	}
	if len(result.Malformed) != 1 || result.Malformed[0] != "[" {
		t.Fatalf("Malformed = %v, want the broken pattern reported", result.Malformed)
	}
}

// TestFindCruft confirms FindCruft returns only entries resolving to the named
// cruft class under the injected rules.
func TestFindCruft(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "keep.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "leftover.tmp"), []byte("x"), 0o644))

	rules := []Rule{{Pattern: "**/*.tmp", Class: "cruft"}}
	got, err := FindCruft(dir, rules, "cruft")
	if err != nil {
		t.Fatalf("FindCruft: %v", err)
	}
	want := filepath.Join(dir, "leftover.tmp")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

// TestScanEnumeratorsDetectsNewFileClass confirms a file no enumerator pattern
// covers is reported as drift.
func TestScanEnumeratorsDetectsNewFileClass(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "b.rs"), []byte("x"), 0o644)) // unaccounted-for

	enumerators := []Enumerator{{Name: "go-build", Patterns: []string{"**/*.go"}}}
	drift, err := ScanEnumerators(dir, enumerators)
	if err != nil {
		t.Fatalf("ScanEnumerators: %v", err)
	}
	want := filepath.Join(dir, "b.rs")
	if len(drift) != 1 || drift[0].Path != want {
		t.Fatalf("got %v, want [%s]", drift, want)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
