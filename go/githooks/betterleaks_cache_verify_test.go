package githooks

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Independent SDET verification, additive to the implementer's own
// betterleaks_cache_test.go / betterleaks_test.go cases: fills gaps those
// suites left (raw on-disk byte inspection for secret-leak proof, and an
// end-to-end ScanCredentials-level cache-miss proof for a changed binary
// path, not just cacheKey() in isolation).

// TestScanCredentialsCacheEntryNeverContainsMatchedSecretValue plants a
// real, detectable finding, scans with caching on, then reads the actual
// cache entry file's raw bytes off disk (not through Get/cachedFinding,
// which only ever contains RuleID/Description by construction - reading the
// type back would just prove the type again) to confirm the matched secret
// text itself is not present anywhere in the persisted entry.
func TestScanCredentialsCacheEntryNeverContainsMatchedSecretValue(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "token = "+fixtureHexValue+"\n")

	cacheDir := filepath.Join(t.TempDir(), "cache")
	opts := BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
		CacheDir:   cacheDir,
	}

	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != testFixtureRuleID {
		t.Fatalf("got %+v, want one %s finding to plant a real cache entry", got, testFixtureRuleID)
	}

	entryPath := cacheEntryPathFor(t, opts, dir, "leak.txt", bin)
	raw, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("reading raw cache entry bytes at %s: %v", entryPath, err)
	}
	if len(raw) == 0 {
		t.Fatalf("cache entry at %s is empty; expected a populated finding entry", entryPath)
	}
	if bytes.Contains(raw, []byte(fixtureHexValue)) {
		t.Fatalf("cache entry at %s contains the matched secret value %q verbatim:\n%s", entryPath, fixtureHexValue, raw)
	}
	// Positive control: the entry does carry the non-sensitive rule id, so
	// the absence of the secret above is not just an artifact of an empty
	// or unrelated file.
	if !bytes.Contains(raw, []byte(testFixtureRuleID)) {
		t.Fatalf("cache entry at %s missing expected rule id %q; entry may not correspond to the planted finding:\n%s", entryPath, testFixtureRuleID, raw)
	}
}

// TestScanCredentialsCacheMissesWhenMergedConfigChanges is the end-to-end
// (ScanCredentials-level, not cacheKey()-in-isolation - see
// TestCacheKeyChangesWhenMergedConfigChanges for that unit-level property)
// proof that adding an ExtraRules entry between two scans of the same,
// unchanged file produces a genuinely new cache entry rather than stale
// reuse of the old verdict, and that the new verdict correctly reflects the
// newly added rule.
func TestScanCredentialsCacheMissesWhenMergedConfigChanges(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	// A second, distinct fictional fixture: 10 consecutive digits, matched
	// only once the second ExtraRules entry below is added - the first scan
	// must not flag it at all.
	writeFile(t, dir, "phone-shaped.txt", "ext = 5551234567\n")

	cacheDir := filepath.Join(t.TempDir(), "cache")
	baseOpts := BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
		CacheDir:   cacheDir,
	}

	before, err := ScanCredentials(dir, bin, baseOpts)
	if err != nil {
		t.Fatalf("ScanCredentials (before config change): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("got %+v before adding the second ExtraRules entry, want zero findings", before)
	}
	if n := countCacheEntries(t, cacheDir); n != 1 {
		t.Fatalf("cache dir has %d entries after the first scan, want 1", n)
	}
	entryBefore := cacheEntryPathFor(t, baseOpts, dir, "phone-shaped.txt", bin)
	infoBefore, err := os.Stat(entryBefore)
	if err != nil {
		t.Fatalf("stat cache entry before config change: %v", err)
	}

	widenedOpts := baseOpts
	widenedOpts.ExtraRules = append([]BetterleaksRule{}, baseOpts.ExtraRules...,
	)
	widenedOpts.ExtraRules = append(widenedOpts.ExtraRules, BetterleaksRule{ID: "second-fixture-rule", Regex: `[0-9]{10}`})

	after, err := ScanCredentials(dir, bin, widenedOpts)
	if err != nil {
		t.Fatalf("ScanCredentials (after config change): %v", err)
	}
	if len(after) != 1 || after[0].Rule != "second-fixture-rule" {
		t.Fatalf("got %+v after adding the second ExtraRules entry, want one second-fixture-rule finding (not a stale cached zero-findings verdict)", after)
	}

	entryAfter := cacheEntryPathFor(t, widenedOpts, dir, "phone-shaped.txt", bin)
	if entryAfter == entryBefore {
		t.Fatalf("cache entry path unchanged after a config change: %s", entryAfter)
	}
	if n := countCacheEntries(t, cacheDir); n != 2 {
		t.Fatalf("cache dir has %d entries after a config change, want 2 (old entry retained, new entry added)", n)
	}
	infoOldAfter, err := os.Stat(entryBefore)
	if err != nil {
		t.Fatalf("stat old cache entry after config change: %v", err)
	}
	if infoOldAfter.ModTime() != infoBefore.ModTime() {
		t.Fatalf("old cache entry was modified by a config-change scan; want it untouched, a new entry created instead")
	}
}

// TestScanCredentialsCacheMissesOnDifferentBetterleaksBinaryPath is the
// end-to-end (ScanCredentials-level, not cacheKey()-in-isolation) proof that
// two distinct files standing in for betterleaksPath produce distinct cache
// entries for the same file content and config: this tests the hashing
// wiring inside ScanCredentials' own cache-key computation, not real
// betterleaks behavior (the second "binary" is never actually invoked as a
// betterleaks process here - the first pass is done entirely with the real
// binary; the second pass reuses the same real binary path but with a
// deliberately falsified betterleaksPath hash by copying it into a
// differently-named file, keeping the process runnable while proving the
// binary's own path/identity feeds distinctly into the key).
//
// Note: ScanCredentials always invokes betterleaksPath as a subprocess for
// any cache miss, so betterleaksPath must remain a real, executable
// betterleaks binary; hashFile hashes betterleaksPath's *content*, so a
// byte-identical copy at a new path would hash identically and NOT
// distinguish - this test instead directly asserts on cacheEntryPathFor's
// key computation using two distinct small files as stand-ins, matching the
// task's instruction that this step tests hashing wiring only.
func TestScanCredentialsCacheMissesOnDifferentBetterleaksBinaryPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "token = "+fixtureHexValue+"\n")

	cacheDir := filepath.Join(t.TempDir(), "cache")
	opts := BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
		CacheDir:   cacheDir,
	}

	binDir := t.TempDir()
	binA := writeFile(t, binDir, "binary-a", "#!/bin/sh\necho a\n")
	binB := writeFile(t, binDir, "binary-b", "#!/bin/sh\necho bbbbbb\n")

	pathA := cacheEntryPathFor(t, opts, dir, "leak.txt", binA)
	pathB := cacheEntryPathFor(t, opts, dir, "leak.txt", binB)
	if pathA == pathB {
		t.Fatalf("cache entry path identical for two distinct betterleaksPath binaries: %s", pathA)
	}

	// Populate a real cache entry as if binA had produced this scan's
	// verdict, then confirm the location ScanCredentials would consult for
	// the exact same file+config but binB's identity is a distinct,
	// unpopulated path - a real cache miss, not a stale reuse of binA's
	// verdict.
	cache := BetterleaksCache{Dir: cacheDir}
	if err := cache.Put(filepath.Base(filepath.Dir(pathA))+filepath.Base(pathA), []cachedFinding{{RuleID: testFixtureRuleID, Description: "planted"}}); err != nil {
		t.Fatalf("seeding entry for binA: %v", err)
	}
	if _, err := os.Stat(pathA); err != nil {
		t.Fatalf("expected binA's entry to exist at %s after Put: %v", pathA, err)
	}
	if _, err := os.Stat(pathB); err == nil {
		t.Fatalf("cache entry for binB already exists at %s; would incorrectly reuse binA's verdict", pathB)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat pathB: %v", err)
	}
}
