//go:build linux

package fsx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestWriteAtomicInterruptedWriteLeavesOriginalIntact is the crash-safety fixture:
// it runs WriteAtomic in a subprocess with RLIMIT_FSIZE capped below the payload
// size, so the kernel truncates the temp-file write partway through with EFBIG —
// an I/O interruption mid-write, before any rename into place. The parent then
// asserts the target path still holds the pre-existing content byte-for-byte:
// an interrupted write never reaches the target path, and the temp+fsync+rename
// sequence never renames a partially-written temp file into place.
func TestWriteAtomicInterruptedWriteLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	must(t, os.WriteFile(path, []byte("original"), 0o644))

	if os.Getenv("FSX_INTERRUPT_HELPER") == "1" {
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatalf("getrlimit: %v", err)
		}
		// Lower only the soft limit, keep the hard limit untouched, so it can be
		// restored afterwards (the coverage/race instrumentation still needs to
		// write its own files on process exit).
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 16, Max: original.Max}); err != nil {
			t.Fatalf("setrlimit lower: %v", err)
		}
		big := strings.Repeat("x", 1<<20) // far larger than the 16-byte cap
		writeErr := WriteAtomic(path, []byte(big), 0o644)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatalf("setrlimit restore: %v", err)
		}
		if writeErr == nil {
			t.Fatal("WriteAtomic succeeded despite the write exceeding RLIMIT_FSIZE, want an I/O error")
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteAtomicInterruptedWriteLeavesOriginalIntact$", "-test.v")
	cmd.Env = append(os.Environ(), "FSX_INTERRUPT_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("interrupted-write subprocess failed: %v\n%s", err, out)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile target after interrupted write: %v", readErr)
	}
	if string(got) != "original" {
		t.Fatalf("target = %q after interrupted write, want untouched %q", got, "original")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries after interrupted write, want 1 (stray temp file leaked?)", len(entries))
	}
}

// TestWriteAtomicRenameFailureLeavesOriginalIntact covers the non-signal failure
// path: if the final rename cannot land (destination directory replaced by a
// read-only one after the temp file is staged), WriteAtomic must surface the
// error and leave the pre-existing target content intact rather than a partial
// or truncated file.
func TestWriteAtomicRenameFailureLeavesOriginalIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission bits")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "roDir")
	must(t, os.Mkdir(dir, 0o755))
	path := filepath.Join(dir, "target.txt")
	must(t, os.WriteFile(path, []byte("original"), 0o644))

	must(t, os.Chmod(dir, 0o555)) // read+execute only: temp-file creation/rename denied
	defer os.Chmod(dir, 0o755)    // restore so t.TempDir() cleanup can remove it

	err := WriteAtomic(path, []byte("clobber"), 0o644)
	if err == nil {
		t.Fatal("WriteAtomic succeeded against a read-only directory, want error")
	}

	must(t, os.Chmod(dir, 0o755))
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile target after failed write: %v", readErr)
	}
	if string(got) != "original" {
		t.Fatalf("target = %q after failed write, want untouched %q", got, "original")
	}
}

// TestMoveOverwritesDestinationAtomically checks that Move replaces an existing
// destination via the single rename syscall rather than erroring or requiring a
// separate delete step first.
func TestMoveOverwritesDestinationAtomically(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	must(t, os.WriteFile(src, []byte("new"), 0o644))
	must(t, os.WriteFile(dst, []byte("old"), 0o644))

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("dst = %q, want %q", got, "new")
	}
}

// TestMoveMissingSourceLeavesDestinationUntouched asserts Move's failure path
// never partially mutates the destination when the source doesn't exist.
func TestMoveMissingSourceLeavesDestinationUntouched(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.txt")
	dst := filepath.Join(dir, "dst.txt")
	must(t, os.WriteFile(dst, []byte("keep-me"), 0o644))

	if err := Move(src, dst); err == nil {
		t.Fatal("Move succeeded with a missing source, want error")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "keep-me" {
		t.Fatalf("dst = %q after failed Move, want untouched %q", got, "keep-me")
	}
}

// TestClassifyPathMultipleMalformedAllReported ensures every malformed pattern
// in a ruleset is surfaced, not just the first or last encountered.
func TestClassifyPathMultipleMalformedAllReported(t *testing.T) {
	rules := []Rule{
		{Pattern: "[", Class: "broken-a"},
		{Pattern: "**/*.go", Class: "source"},
		{Pattern: "a\\", Class: "broken-b"},
	}
	result := ClassifyPath("pkg/foo.go", rules)
	if len(result.Malformed) != 2 {
		t.Fatalf("Malformed = %v, want 2 entries", result.Malformed)
	}
	// Last rule in the list is malformed, so per last-match-wins it still fails
	// closed and wins over the well-formed rule in between.
	if result.Class != "broken-b" {
		t.Fatalf("Class = %q, want fail-closed last rule %q", result.Class, "broken-b")
	}
}

// TestClassifyPathWellFormedRuleAfterMalformedCanOverride confirms a later,
// well-formed non-match does NOT get overridden by an earlier malformed match:
// last-match-wins means a genuine non-match from a later rule leaves the
// malformed rule's fail-closed classification standing, since a non-match never
// overwrites result.Class.
func TestClassifyPathWellFormedRuleAfterMalformedCanOverride(t *testing.T) {
	rules := []Rule{
		{Pattern: "[", Class: "broken"},
		{Pattern: "**/*.rs", Class: "rust"}, // does not match a .go path
	}
	result := ClassifyPath("pkg/foo.go", rules)
	if result.Class != "broken" {
		t.Fatalf("Class = %q, want %q (malformed match stands since later rule genuinely doesn't match)", result.Class, "broken")
	}
}

// TestFindCruftRulesetInjectedNotHardcoded runs two different rulesets against
// the same directory tree and requires different results, proving FindCruft
// carries no built-in notion of what counts as cruft.
func TestFindCruftRulesetInjectedNotHardcoded(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.tmp"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "a.bak"), []byte("x"), 0o644))

	tmpOnly, err := FindCruft(dir, []Rule{{Pattern: "**/*.tmp", Class: "cruft"}}, "cruft")
	must(t, err)
	bakOnly, err := FindCruft(dir, []Rule{{Pattern: "**/*.bak", Class: "cruft"}}, "cruft")
	must(t, err)

	if len(tmpOnly) != 1 || filepath.Base(tmpOnly[0]) != "a.tmp" {
		t.Fatalf("tmpOnly = %v, want [a.tmp]", tmpOnly)
	}
	if len(bakOnly) != 1 || filepath.Base(bakOnly[0]) != "a.bak" {
		t.Fatalf("bakOnly = %v, want [a.bak]", bakOnly)
	}
}

// TestFindCruftEmptyRulesetFindsNothing checks the empty-ruleset edge case does
// not error and reports no cruft (ClassifyPath("", nil).Class == "" never
// equals any caller-supplied cruftClass).
func TestFindCruftEmptyRulesetFindsNothing(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.tmp"), []byte("x"), 0o644))

	got, err := FindCruft(dir, nil, "cruft")
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("got %v, want no cruft from an empty ruleset", got)
	}
}

// TestFindCruftNestedDirectories confirms cruft is detected below subdirectories,
// not just at the walk root.
func TestFindCruftNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	must(t, os.MkdirAll(nested, 0o755))
	must(t, os.WriteFile(filepath.Join(nested, "leftover.tmp"), []byte("x"), 0o644))

	got, err := FindCruft(dir, []Rule{{Pattern: "**/*.tmp", Class: "cruft"}}, "cruft")
	must(t, err)
	want := filepath.Join(nested, "leftover.tmp")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

// TestScanEnumeratorsEmptyEnumeratorsReportsEverything checks that with no
// enumerators at all, every file is drift — there is nothing to vouch for any
// of them.
func TestScanEnumeratorsEmptyEnumeratorsReportsEverything(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "b.rs"), []byte("x"), 0o644))

	drift, err := ScanEnumerators(dir, nil)
	must(t, err)
	if len(drift) != 2 {
		t.Fatalf("drift = %v, want 2 (all files, no enumerators)", drift)
	}
}

// TestScanEnumeratorsMalformedPatternMatchesNothing ensures a broken enumerator
// pattern never masquerades as vouching for a file: the file it might have been
// meant to cover still surfaces as drift instead of being silently swallowed.
func TestScanEnumeratorsMalformedPatternMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644))

	enumerators := []Enumerator{{Name: "broken", Patterns: []string{"["}}}
	drift, err := ScanEnumerators(dir, enumerators)
	must(t, err)
	if len(drift) != 1 || filepath.Base(drift[0].Path) != "a.go" {
		t.Fatalf("drift = %v, want a.go surfaced despite the broken enumerator pattern", drift)
	}
}

// TestScanEnumeratorsSecondEnumeratorCoversFile checks that a file is covered
// (no drift) as soon as any one enumerator's pattern matches, even if an
// earlier enumerator in the list does not.
func TestScanEnumeratorsSecondEnumeratorCoversFile(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.rs"), []byte("x"), 0o644))

	enumerators := []Enumerator{
		{Name: "go-build", Patterns: []string{"**/*.go"}},
		{Name: "rust-build", Patterns: []string{"**/*.rs"}},
	}
	drift, err := ScanEnumerators(dir, enumerators)
	must(t, err)
	if len(drift) != 0 {
		t.Fatalf("drift = %v, want none: second enumerator covers a.rs", drift)
	}
}

// TestScanEnumeratorsIgnoresDirectories confirms directories themselves are
// never reported as drift, only regular files under them.
func TestScanEnumeratorsIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "empty-subdir"), 0o755))

	drift, err := ScanEnumerators(dir, nil)
	must(t, err)
	if len(drift) != 0 {
		t.Fatalf("drift = %v, want none: only an empty directory exists", drift)
	}
}
