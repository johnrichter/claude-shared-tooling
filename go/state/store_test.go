package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadMissingDegradesToEmpty confirms a missing file returns a fresh empty document,
// never an error.
func TestReadMissingDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	doc, err := Read(path, 3, Migrations{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if SchemaVersion(doc) != 3 {
		t.Fatalf("SchemaVersion = %d, want 3", SchemaVersion(doc))
	}
}

// TestReadCorruptDegradesToEmpty confirms invalid JSON degrades to an empty document
// rather than raising.
func TestReadCorruptDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path, 3, Migrations{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if SchemaVersion(doc) != 3 || len(doc) != 1 {
		t.Fatalf("doc = %+v, want fresh empty at version 3", doc)
	}
}

// TestReadNonObjectDegradesToEmpty confirms top-level JSON that isn't an object (e.g. an
// array) degrades to empty rather than raising.
func TestReadNonObjectDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("[1,2,3]"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path, 1, Migrations{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if SchemaVersion(doc) != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", SchemaVersion(doc))
	}
}

// TestReadEmptyFileDegradesToEmpty confirms a zero-byte file degrades to empty rather than
// raising a JSON decode error.
func TestReadEmptyFileDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path, 2, Migrations{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if SchemaVersion(doc) != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion(doc))
	}
}

// TestWriteReadRoundTrip confirms a written document reads back with its payload and
// version intact.
func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	doc := Empty(1)
	doc["made"] = 5
	if err := Write(path, doc, 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path, 1, Migrations{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if SchemaVersion(got) != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", SchemaVersion(got))
	}
	if made, _ := got["made"].(float64); made != 5 {
		t.Fatalf("made = %v, want 5", got["made"])
	}
}

// TestWriteAtomicNoStrayTempFile confirms Write never leaves a temp file behind.
func TestWriteAtomicNoStrayTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, Empty(1), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (temp file leaked?)", len(entries))
	}
}

// TestMigrateChainUpgradesOldVersion confirms a document below target walks every
// registered step in order up to target.
func TestMigrateChainUpgradesOldVersion(t *testing.T) {
	migrations := Migrations{
		0: func(d Doc) Doc { d["telemetry_added"] = true; d[versionKey] = 1; return d },
		1: func(d Doc) Doc { d["archived_added"] = true; d[versionKey] = 2; return d },
	}
	doc := Doc{} // no _schema_version at all — treated as 0
	got, err := Migrate(doc, 2, migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if SchemaVersion(got) != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion(got))
	}
	if got["telemetry_added"] != true || got["archived_added"] != true {
		t.Fatalf("got %+v, want both migration steps applied", got)
	}
}

// TestMigrateAlreadyCurrentIsNoop confirms a document already at target is left with only
// its version restamped, no migration steps invoked.
func TestMigrateAlreadyCurrentIsNoop(t *testing.T) {
	called := false
	migrations := Migrations{2: func(d Doc) Doc { called = true; return d }}
	doc := Doc{versionKey: 2}
	got, err := Migrate(doc, 2, migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if called {
		t.Fatal("migration step invoked on a document already at target")
	}
	if SchemaVersion(got) != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion(got))
	}
}

// TestMigrateForwardVersionRefusedLoudly confirms a document newer than target is refused
// with a named error rather than silently passed through or corrupted.
func TestMigrateForwardVersionRefusedLoudly(t *testing.T) {
	doc := Doc{versionKey: 5}
	_, err := Migrate(doc, 2, Migrations{})
	if err == nil {
		t.Fatal("Migrate: want error for forward version, got nil")
	}
	var fv *ErrForwardVersion
	if !errors.As(err, &fv) {
		t.Fatalf("Migrate error = %v, want *ErrForwardVersion", err)
	}
	if fv.Found != 5 || fv.Known != 2 {
		t.Fatalf("ErrForwardVersion = %+v, want Found=5 Known=2", fv)
	}
}

// TestReadForwardVersionOnDiskRefusedLoudly confirms Read propagates the forward-version
// refusal for a real on-disk file (as opposed to corrupt/missing input, which degrades
// silently) — the newer-schema case is a real signal, not corruption.
func TestReadForwardVersionOnDiskRefusedLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, Doc{versionKey: 99}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path, 2, Migrations{})
	if err == nil {
		t.Fatal("Read: want error for forward version, got nil")
	}
}

// TestIncrementCountersAdditiveAcrossCalls confirms counters accumulate rather than reset
// on each call.
func TestIncrementCountersAdditiveAcrossCalls(t *testing.T) {
	doc := Empty(1)
	IncrementCounters(doc, map[string]int64{"made": 2, "skipped": 1}, "t1")
	IncrementCounters(doc, map[string]int64{"made": 3}, "t2")
	if got := Counter(doc, "made"); got != 5 {
		t.Fatalf("made = %d, want 5", got)
	}
	if got := Counter(doc, "skipped"); got != 1 {
		t.Fatalf("skipped = %d, want 1", got)
	}
}

// TestCounterAbsentReturnsZero confirms an unset counter reads 0, not an error.
func TestCounterAbsentReturnsZero(t *testing.T) {
	if got := Counter(Empty(1), "never-set"); got != 0 {
		t.Fatalf("Counter = %d, want 0", got)
	}
}
