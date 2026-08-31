package githooks

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// ─── cacheKey: composite-hash sensitivity ───

func TestCacheKeyChangesWhenFileContentChanges(t *testing.T) {
	configHash := sha256.Sum256([]byte("config-a"))
	binaryHash := sha256.Sum256([]byte("binary-a"))

	a := cacheKey(sha256.Sum256([]byte("content-a")), configHash, binaryHash)
	b := cacheKey(sha256.Sum256([]byte("content-b")), configHash, binaryHash)
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

	beforeKey := cacheKey(fileHash, sha256.Sum256(before), binaryHash)
	afterKey := cacheKey(fileHash, sha256.Sum256(after), binaryHash)
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

	keyA := cacheKey(fileHash, configHash, hashA)
	keyB := cacheKey(fileHash, configHash, hashB)
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

	if cacheKey(fileHash, configHash, realHash) == cacheKey(fileHash, configHash, otherHash) {
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
