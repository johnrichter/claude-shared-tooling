package state

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// versionKey is the top-level field every document carries: the schema version it was
// last written at.
const versionKey = "_schema_version"

// telemetryKey is the top-level field holding this document's additive counters.
const telemetryKey = "_telemetry"

// Doc is a state document: a plain JSON object. Every document carries versionKey at the
// top level; payload fields live under whatever other keys a caller chooses.
type Doc map[string]any

// MigrationFunc upgrades a document one schema version forward — its input is always at
// the version given by the key it's registered under in a Migrations chain.
type MigrationFunc func(Doc) Doc

// Migrations is a version-upgrade chain, keyed by the version being upgraded FROM. Migrate
// walks it from a document's current version up to a target version, one step at a time.
type Migrations map[int]MigrationFunc

// Empty returns a fresh document at schemaVersion with no payload — what Read hands back
// for a missing or corrupt file, and what a caller starts a new state file from.
func Empty(schemaVersion int) Doc {
	return Doc{versionKey: schemaVersion}
}

// SchemaVersion returns doc's recorded version, or 0 if the document has none (a
// pre-versioning file, or a freshly zero-valued Doc).
func SchemaVersion(doc Doc) int {
	v, ok := doc[versionKey]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64: // json.Unmarshal decodes numbers into interface{} as float64
		return int(n)
	default:
		return 0
	}
}

// ErrForwardVersion is returned when a document's schema_version is newer than the
// version this build knows how to migrate to. Migrate refuses to touch such a document —
// the alternative, silently upgrading anyway, risks dropping fields a newer schema added
// that this build has never heard of.
type ErrForwardVersion struct {
	Found, Known int
}

// Error renders the forward-version refusal as a loud, actionable message: the caller
// gets told which version it saw and which version this build supports.
func (e *ErrForwardVersion) Error() string {
	return fmt.Sprintf("state: schema_version %d is newer than this build supports (max %d) — upgrade before reading this state", e.Found, e.Known)
}

// Migrate walks doc from its recorded version up to target, applying each step's
// MigrationFunc in order. A document already at or past target (and not newer — see
// below) is returned unchanged except for the stamped version. A document newer than
// target is refused with *ErrForwardVersion rather than silently passed through: this
// build does not know what fields a version it has never seen might carry.
func Migrate(doc Doc, target int, migrations Migrations) (Doc, error) {
	v := SchemaVersion(doc)
	if v > target {
		return doc, &ErrForwardVersion{Found: v, Known: target}
	}
	for v < target {
		step, ok := migrations[v]
		if !ok {
			// A gap in the chain: no registered step upgrades from this version. This is
			// a caller configuration error (a Migrations map missing an entry it should
			// have), not something Migrate can recover from mid-walk — the payload from
			// here up is left exactly as found, only the version label advances to target.
			// A caller relying on target's fields being populated must keep its chain gap-
			// free; Migrate cannot detect "gap" versus "already fully upgraded" from here.
			break
		}
		doc = step(doc)
		nv := SchemaVersion(doc)
		if nv <= v {
			// A migration that doesn't advance the version would loop forever;
			// treat it as having reached as far as it can and stop here.
			break
		}
		v = nv
	}
	doc[versionKey] = target
	return doc, nil
}

// Read loads path and returns its decoded, migrated document. A missing file, an empty
// file, invalid JSON, or JSON whose top level isn't an object all degrade to Empty(target)
// with a nil error — Read never raises on corrupt input, so a damaged state file can never
// block whatever run depends on it. The one error Read does return is the loud one:
// *ErrForwardVersion, when the file's schema_version is newer than target — that is a
// real signal (this build is behind), not corruption, and callers should surface it rather
// than silently overwrite a newer file with a stale-format one.
func Read(path string, target int, migrations Migrations) (Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Empty(target), nil
	}
	if len(raw) == 0 {
		return Empty(target), nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Empty(target), nil
	}
	doc, err := Migrate(Doc(decoded), target, migrations)
	if err != nil {
		return doc, err
	}
	return doc, nil
}

// Write atomically persists doc to path via fsx.WriteAtomic: a crash or power loss at any
// point during the write leaves either the previous contents or the new ones in place,
// never a partial file.
func Write(path string, doc Doc, perm fs.FileMode) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	return fsx.WriteAtomic(path, data, perm)
}

// lockPollInterval is how often a blocked WithLock retry re-attempts acquiring the lock.
const lockPollInterval = 2 * time.Millisecond

// lockTimeout bounds how long WithLock waits for a contended lock before giving up loudly
// rather than hanging a caller forever.
const lockTimeout = 10 * time.Second

// staleLockAge is how long a lock file may sit unclaimed-looking before WithLock assumes
// its holder crashed without releasing it and breaks the lock rather than waiting out
// lockTimeout for a holder that no longer exists.
const staleLockAge = 30 * time.Second

// WithLock serializes fn against every other caller — same process or another — holding
// the lock for path, via an exclusively-created sidecar file (path+".lock") rather than an
// in-process mutex, so it also protects two separate processes racing a read-modify-write
// cycle against the same on-disk document. Use it around any Read-then-Write sequence
// (RegisterSource's read-refs/append-consumer/write-refs is one) where a plain
// read-then-write would lose a concurrent update. A lock file left behind by a crashed
// holder is broken automatically once it's older than staleLockAge, so a dead process can
// never wedge every future caller.
func WithLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("state: acquire lock %s: %w", lockPath, err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("state: %s: lock still held after %s, giving up", lockPath, lockTimeout)
		}
		time.Sleep(lockPollInterval)
	}
	defer os.Remove(lockPath)
	return fn()
}

// IncrementCounters accumulates deltas into doc's telemetry block (created if absent) and
// stamps its last_updated. Counters are additive across calls — each call adds to the
// running total rather than replacing it, matching each caller's own per-run figures.
func IncrementCounters(doc Doc, deltas map[string]int64, at string) {
	raw, _ := doc[telemetryKey].(map[string]any)
	if raw == nil {
		raw = map[string]any{}
	}
	for k, delta := range deltas {
		var cur int64
		switch v := raw[k].(type) {
		case int64:
			cur = v
		case float64:
			cur = int64(v)
		}
		raw[k] = cur + delta
	}
	raw["last_updated"] = at
	doc[telemetryKey] = raw
}

// Counter reads a single named counter from doc's telemetry block, or 0 if absent.
func Counter(doc Doc, key string) int64 {
	raw, _ := doc[telemetryKey].(map[string]any)
	switch v := raw[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// Now is the ISO-8601 UTC timestamp helper callers use for telemetry/registry
// last_updated/last_seen stamps, so every caller of this package timestamps the same way.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
