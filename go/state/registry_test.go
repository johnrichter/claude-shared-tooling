package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegisterSourceThenSeen confirms a registered ref reads back as seen.
func TestRegisterSourceThenSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-registry.json")
	if SeenSource(path, "channel:C1:1700000000.000100") {
		t.Fatal("SeenSource true before any registration")
	}
	if err := RegisterSource(path, "channel:C1:1700000000.000100", "feeder-a", "2026-07-26T00:00:00Z"); err != nil {
		t.Fatalf("RegisterSource: %v", err)
	}
	if !SeenSource(path, "channel:C1:1700000000.000100") {
		t.Fatal("SeenSource false after registration")
	}
}

// TestRegisterSourceIdempotentAcrossConsumers confirms the same ref registered by two
// different consumers dedupes into one entry with both consumers listed, and re-
// registering from the same consumer never duplicates it.
func TestRegisterSourceIdempotentAcrossConsumers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-registry.json")
	ref := "https://example.com/a"
	must(t, RegisterSource(path, ref, "feeder-a", "2026-07-26T00:00:00Z"))
	must(t, RegisterSource(path, ref, "feeder-a", "2026-07-26T00:01:00Z")) // same consumer, again
	must(t, RegisterSource(path, ref, "feeder-b", "2026-07-26T00:02:00Z")) // second consumer

	doc, err := Read(path, RegistrySchemaVersion, registryMigrations)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	refs := readRefs(doc)
	entry, ok := refs[ref]
	if !ok {
		t.Fatalf("ref %q not registered", ref)
	}
	if len(entry.Consumers) != 2 {
		t.Fatalf("Consumers = %v, want exactly 2 (feeder-a, feeder-b, deduped)", entry.Consumers)
	}
	if entry.FirstSeen != "2026-07-26T00:00:00Z" {
		t.Fatalf("FirstSeen = %q, want the original registration time unchanged", entry.FirstSeen)
	}
	if entry.LastSeen != "2026-07-26T00:02:00Z" {
		t.Fatalf("LastSeen = %q, want the most recent registration time", entry.LastSeen)
	}
}

// TestSeenSourceCorruptRegistryDegradesToFalse confirms a corrupt registry file reads as
// unseen rather than raising.
func TestSeenSourceCorruptRegistryDegradesToFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source-registry.json")
	must(t, os.WriteFile(path, []byte("{not json"), 0o644))
	if SeenSource(path, "anything") {
		t.Fatal("SeenSource true against a corrupt registry, want false")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
