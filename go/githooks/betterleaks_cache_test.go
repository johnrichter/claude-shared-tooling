package githooks

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ─── cacheKey: composite-hash sensitivity ───

func TestCacheKeyChangesWhenFileContentChanges(t *testing.T) {
	configHash := sha256.Sum256([]byte("config-a"))
	binaryHash := sha256.Sum256([]byte("binary-a"))

	pathHash := sha256.Sum256([]byte("same/path.txt"))

	a := cacheKey(pathHash, sha256.Sum256([]byte("content-a")), configHash, binaryHash)
	b := cacheKey(pathHash, sha256.Sum256([]byte("content-b")), configHash, binaryHash)
	if a == b {
		t.Fatalf("cacheKey unchanged across different file content: %q", a)
	}
}

func TestCacheKeyChangesWhenMergedConfigChanges(t *testing.T) {
	fileHash := sha256.Sum256([]byte("content"))
	binaryHash := sha256.Sum256([]byte("binary"))

	before, err := buildBetterleaksConfig(nil, nil)
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}
	after, err := buildBetterleaksConfig([]BetterleaksRule{{ID: "extra-fixture", Regex: `[0-9]{10}`}}, nil)
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}

	pathHash := sha256.Sum256([]byte("f.txt"))
	beforeKey := cacheKey(pathHash, fileHash, sha256.Sum256(before), binaryHash)
	afterKey := cacheKey(pathHash, fileHash, sha256.Sum256(after), binaryHash)
	if beforeKey == afterKey {
		t.Fatalf("cacheKey unchanged after adding an ExtraRules entry: %q", beforeKey)
	}
}

func TestCacheKeyChangesWhenBinaryChanges(t *testing.T) {
	fileHash := sha256.Sum256([]byte("content"))
	configHash := sha256.Sum256([]byte("config"))

	dir := t.TempDir()
	binA := writeFile(t, dir, "binary-a", "#!/bin/sh\necho a\n")
	binB := writeFile(t, dir, "binary-b", "#!/bin/sh\necho bbbbbb\n")

	hashA, err := hashFile(binA)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	hashB, err := hashFile(binB)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}

	pathHash := sha256.Sum256([]byte("f.txt"))
	keyA := cacheKey(pathHash, fileHash, configHash, hashA)
	keyB := cacheKey(pathHash, fileHash, configHash, hashB)
	if keyA == keyB {
		t.Fatalf("cacheKey unchanged across two distinct binaries: %q", keyA)
	}
}

// TestCacheKeyChangesWhenRealBinaryChanges is the same property against the
// real, provisioned betterleaks binary alongside a distinct fake one - the
// composite-hash wiring, not betterleaks' own content, is what is under
// test, so any second distinct file suffices as the "other" binary.
func TestCacheKeyChangesWhenRealBinaryChanges(t *testing.T) {
	bin := testBetterleaksBinary(t)
	fileHash := sha256.Sum256([]byte("content"))
	configHash := sha256.Sum256([]byte("config"))

	other := writeFile(t, t.TempDir(), "other-binary", "not-betterleaks\n")

	realHash, err := hashFile(bin)
	if err != nil {
		t.Fatalf("hashFile(real binary): %v", err)
	}
	otherHash, err := hashFile(other)
	if err != nil {
		t.Fatalf("hashFile(other): %v", err)
	}

	pathHash := sha256.Sum256([]byte("f.txt"))
	if cacheKey(pathHash, fileHash, configHash, realHash) == cacheKey(pathHash, fileHash, configHash, otherHash) {
		t.Fatal("cacheKey unchanged between the real betterleaks binary and an unrelated file")
	}
}

// ─── Get/Put: round-trip, missing entry, corrupt entry ───

func TestBetterleaksCacheGetPutRoundTrip(t *testing.T) {
	cache := BetterleaksCache{Dir: t.TempDir()}
	key := "abcd" + "1234"
	want := []cachedFinding{{RuleID: "r1", Description: "d1"}}

	if err := cache.Put(key, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, hit, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !hit {
		t.Fatal("Get: want a hit after Put, got a miss")
	}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Get returned %+v, want %+v", got, want)
	}
}

func TestBetterleaksCacheGetMissingEntryIsCleanMiss(t *testing.T) {
	cache := BetterleaksCache{Dir: t.TempDir()}
	got, hit, err := cache.Get("nonexistent0000")
	if err != nil {
		t.Fatalf("Get: want nil error for a missing entry, got %v", err)
	}
	if hit {
		t.Fatalf("Get: want a miss for a nonexistent key, got hit %+v", got)
	}
}

func TestBetterleaksCacheGetMissingDirIsCleanMiss(t *testing.T) {
	cache := BetterleaksCache{Dir: filepath.Join(t.TempDir(), "does-not-exist")}
	got, hit, err := cache.Get("anykey00000000")
	if err != nil {
		t.Fatalf("Get: want nil error for a missing cache dir, got %v", err)
	}
	if hit {
		t.Fatalf("Get: want a miss when the cache dir does not exist, got hit %+v", got)
	}
}

func TestBetterleaksCacheGetCorruptEntryIsCleanMiss(t *testing.T) {
	cache := BetterleaksCache{Dir: t.TempDir()}
	key := "deadbeef0000"
	entryPath := cache.entryPath(key)
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("not valid json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, hit, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Get: want nil error for a corrupt entry, got %v", err)
	}
	if hit {
		t.Fatalf("Get: want a miss for a corrupt entry, got hit %+v", got)
	}
}

func TestBetterleaksCachePutEmptyFindingsIsAHitOnZeroFindings(t *testing.T) {
	cache := BetterleaksCache{Dir: t.TempDir()}
	key := "cleanfile00000"
	if err := cache.Put(key, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, hit, err := cache.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !hit {
		t.Fatal("Get: want a hit for an explicit zero-findings entry, got a miss")
	}
	if len(got) != 0 {
		t.Fatalf("Get returned %+v, want zero findings", got)
	}
}

func TestBetterleaksCacheDisabledIsAlwaysMiss(t *testing.T) {
	cache := BetterleaksCache{} // Dir == "": caching off
	if err := cache.Put("anykey", []cachedFinding{{RuleID: "r"}}); err != nil {
		t.Fatalf("Put with empty Dir: %v", err)
	}
	_, hit, err := cache.Get("anykey")
	if err != nil {
		t.Fatalf("Get with empty Dir: %v", err)
	}
	if hit {
		t.Fatal("Get with empty Dir: want caching off to never report a hit")
	}
}

// TestCacheKeyChangesWhenPathChanges pins the fourth key input. A verdict
// depends on the file's path, not only its bytes: the base config ships
// path-conditioned rules (see cacheKey), so two identically-worded files at
// different paths must never share one entry.
func TestCacheKeyChangesWhenPathChanges(t *testing.T) {
	fileHash := sha256.Sum256([]byte("content"))
	configHash := sha256.Sum256([]byte("config"))
	binaryHash := sha256.Sum256([]byte("binary"))

	a := cacheKey(sha256.Sum256([]byte("bundle.p12")), fileHash, configHash, binaryHash)
	b := cacheKey(sha256.Sum256([]byte("notes.txt")), fileHash, configHash, binaryHash)
	if a == b {
		t.Fatalf("cacheKey unchanged across two paths with identical content: %q", a)
	}
}

// TestBetterleaksCachePutIsSafeUnderConcurrentWriters covers the case
// atomicity has to survive in practice: not one writer racing a reader, but
// several processes scanning the same file for the first time at once
// (parallel git-tools invocations across two worktrees sharing a cache dir).
// Each writer gets its own uniquely named temp file and renames over the
// last, so the entry is always one complete verdict, no writer fails, and no
// temp file is left behind.
func TestBetterleaksCachePutIsSafeUnderConcurrentWriters(t *testing.T) {
	cache := BetterleaksCache{Dir: t.TempDir()}
	const key = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	want := cachedFinding{RuleID: "r1", Description: "d1"}

	const writers, readers = 32, 16
	var wg sync.WaitGroup
	fail := make(chan string, writers+readers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cache.Put(key, []cachedFinding{want}); err != nil {
				fail <- fmt.Sprintf("Put: %v", err)
			}
		}()
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, hit, err := cache.Get(key)
				if err != nil {
					fail <- fmt.Sprintf("Get: %v", err)
					return
				}
				if hit && (len(got) != 1 || got[0] != want) {
					fail <- fmt.Sprintf("Get observed a torn entry: %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(fail)
	for msg := range fail {
		t.Error(msg)
	}

	if n := countCacheEntries(t, cache.Dir); n != 1 {
		t.Fatalf("cache holds %d files after %d concurrent Puts of one key, want 1 (a leftover temp file would also count here)", n, writers)
	}
	got, hit, err := cache.Get(key)
	if err != nil || !hit || len(got) != 1 || got[0] != want {
		t.Fatalf("after concurrent Puts: got %+v hit=%v err=%v, want the single complete verdict", got, hit, err)
	}
}
