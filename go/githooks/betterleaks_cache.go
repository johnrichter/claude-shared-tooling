package githooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cachedFinding is a cache entry's per-finding payload: exactly the same
// fields Finding.Rule/Finding.Detail already carry, and just as non-sensitive
// (see ScanCredentials' finding-construction loop - the raw matched secret
// value, betterleaksFinding.Secret, never reaches a Finding at all outside
// the JWT demo-token check, so it never reaches a cache entry either). A
// cache entry never stores Path (re-attached per call from the live
// candidate list) or Category (re-derived from Rule via categoryForRuleID).
type cachedFinding struct {
	RuleID      string
	Description string
}

// BetterleaksCache is a hash-addressed, on-disk cache of betterleaks scan
// verdicts, keyed on every input a verdict depends on - the file's path and
// content, the merged config, the scanning binary - so that a cached entry
// is reusable exactly as long as it stays correct: see cacheKey. An entry is
// trusted on read and never evicted; see this package's doc comment for what
// that means for a caller. Dir is caller-supplied, never hardcoded or
// defaulted to a location this package invents on its own - matching
// ScanCredentials' own betterleaksPath parameter, and BetterleaksOptions'
// other fields, this package stays entirely provisioning-agnostic. An empty
// Dir means caching is off.
type BetterleaksCache struct {
	Dir string
}

// cacheKey derives a cache entry's key from the four things a scan verdict
// actually depends on: the scanned file's own path and content, the
// effective merged betterleaks config, and the betterleaks binary doing the
// scanning. Any change to any of the four invalidates every key that
// depended on it, simply by no longer producing the same hash - there is no
// separate invalidation step to keep in sync.
//
// The path is part of the key because a verdict genuinely depends on it:
// betterleaks rules may carry a path condition, and the base config ships
// several that do - including one (pkcs12-file) with no regex at all, which
// fires on a matching filename whatever the content is. Key two files by
// content alone and a "*.p12" file's finding is served from, or overwritten
// by, an identically-worded ".txt" file's clean verdict - a silent false
// negative in a secret scanner. Keying on the path costs nothing and closes
// that entirely.
//
// Every input is a fixed-width 32-byte digest, so the 128-byte preimage
// parses back into its four fields at fixed offsets: unlike a concatenation
// of variable-length fields, there is no boundary ambiguity to exploit and
// no separator needed. Length extension is not a concern either - this is a
// lookup key over public inputs, not a MAC, and the preimage length is
// constant.
func cacheKey(pathHash, fileHash, configHash, binaryHash [sha256.Size]byte) string {
	h := sha256.New()
	h.Write(pathHash[:])
	h.Write(fileHash[:])
	h.Write(configHash[:])
	h.Write(binaryHash[:])
	return hex.EncodeToString(h.Sum(nil))
}

// hashFile returns the sha256 of path's content, streamed rather than read
// whole: this cache exists for repos carrying a very large file, and reading
// several hundred megabytes into memory inside a git hook is a real
// out-of-memory hazard for no speed gain - streaming measures marginally
// faster and holds a constant few megabytes instead of the whole file.
func hashFile(path string) ([sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [sha256.Size]byte{}, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// hashScannableFile returns the sha256 of path's content for cache-key use,
// reporting ok=false - never an error - for anything that is not a plain,
// readable regular file: a dangling symlink, a fifo or device node whose
// read would block forever, a file that vanished between the walk and this
// read. Such a path is simply not cacheable, and ScanCredentials still hands
// it to betterleaks exactly as it would with caching off, so turning the
// cache on can never fail a scan that would otherwise have completed.
func hashScannableFile(path string) (sum [sha256.Size]byte, ok bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return [sha256.Size]byte{}, false
	}
	sum, err = hashFile(path)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sum, true
}

// entryPath returns the on-disk path for key, splitting the first two hex
// characters into a subdirectory (e.g. "ab/cdef...") so a large cache never
// puts an unbounded number of entries in one flat directory - a two-level
// split is the conventional fix, and 256 shallow subdirectories against a
// content-addressed hex key spread entries about as evenly as this cache's
// consumer (a full-tree scan on every merge/push/rebase) will ever demand.
func (c BetterleaksCache) entryPath(key string) string {
	return filepath.Join(c.Dir, key[:2], key[2:])
}

// Get looks up key, returning hit=false (never an error) both for a missing
// entry and for one that fails to read or parse: a corrupt cache entry must
// never abort a scan, only cost this one file's cache benefit.
func (c BetterleaksCache) Get(key string) (findings []cachedFinding, hit bool, err error) {
	if c.Dir == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(c.entryPath(key))
	if err != nil {
		return nil, false, nil
	}
	if jsonErr := json.Unmarshal(data, &findings); jsonErr != nil {
		return nil, false, nil
	}
	return findings, true, nil
}

// Put writes findings under key, atomically: it writes to a temp file in the
// entry's own subdirectory, then renames it into place, so a concurrent
// Get never observes a partially written entry. findings may be empty (a
// clean file's explicit zero-findings verdict), never nil-vs-empty
// distinguished - json.Marshal renders both as "[]".
func (c BetterleaksCache) Put(key string, findings []cachedFinding) error {
	if c.Dir == "" {
		return nil
	}
	if findings == nil {
		findings = []cachedFinding{}
	}
	data, err := json.Marshal(findings)
	if err != nil {
		return fmt.Errorf("githooks: marshaling betterleaks cache entry: %w", err)
	}

	entryPath := c.entryPath(key)
	dir := filepath.Dir(entryPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("githooks: creating betterleaks cache dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("githooks: creating betterleaks cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("githooks: writing betterleaks cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("githooks: closing betterleaks cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, entryPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("githooks: renaming betterleaks cache entry into place: %w", err)
	}
	return nil
}
