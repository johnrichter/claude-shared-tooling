package state

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Adversarial probes beyond the implementer's own suite.

// TestReadDirectoryPathDegradesToEmpty: os.ReadFile on a directory returns an error (not a
// corrupt-JSON case) — confirm Read's safe-degradation covers this path too, not just
// missing/corrupt files.
func TestReadDirectoryPathDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	doc, err := Read(dir, 2, Migrations{})
	if err != nil {
		t.Fatalf("Read on directory path: %v, want nil (safe degradation)", err)
	}
	if SchemaVersion(doc) != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion(doc))
	}
}

// TestRecordTaskRefusesWhitespaceOnlyCommit: a commit sha of only whitespace is not a real
// sha — the rung-1 invariant must treat it the same as empty, per strings.TrimSpace in the
// implementation. Confirms the guard isn't merely `== ""`.
func TestRecordTaskRefusesWhitespaceOnlyCommit(t *testing.T) {
	task := Task{ID: "T1", Status: StatusInProgress}
	err := RecordTask(&task, statusPtr(StatusDone), stringPtr("   "), nil)
	if err == nil {
		t.Fatalf("RecordTask allowed done with whitespace-only commit sha")
	}
}

// TestWriteTasksRefusesWhitespaceOnlyCommit mirrors the whitespace probe at the on-disk
// choke point.
func TestWriteTasksRefusesWhitespaceOnlyCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	tasks := []Task{{ID: "T1", Status: StatusDone, CommitSHA: "  \t "}}
	err := WriteTasks(path, tasks, 0o644)
	if err == nil {
		t.Fatalf("WriteTasks allowed done with whitespace-only commit sha")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("WriteTasks refusal still created a file on disk")
	}
}

// TestMigrateUnknownVersionAboveZeroWithNoStepBreaksSafely: a document at version 1 with no
// registered migration from 1 must not spin or panic — it should stop and stamp target.
func TestMigrateUnknownVersionAboveZeroWithNoStepBreaksSafely(t *testing.T) {
	doc := Doc{versionKey: 1, "payload": "x"}
	got, err := Migrate(doc, 5, Migrations{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if SchemaVersion(got) != 5 {
		t.Fatalf("SchemaVersion = %d, want 5 (stamped even with no migration step)", SchemaVersion(got))
	}
	if got["payload"] != "x" {
		t.Fatalf("payload lost across migration stub: %+v", got)
	}
}

// TestReadForwardVersionDoesNotMutateOnDiskFile: Read's forward-version refusal must not
// silently rewrite the file — only Write (an explicit caller action) may touch disk.
func TestReadForwardVersionDoesNotMutateOnDiskFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte(`{"_schema_version": 99, "payload": "keep-me"}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path, 3, Migrations{})
	if err == nil {
		t.Fatalf("Read: want ErrForwardVersion, got nil")
	}
	onDisk, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(onDisk) != string(original) {
		t.Fatalf("Read mutated file on forward-version refusal: got %q, want unchanged %q", onDisk, original)
	}
}

// TestRegisterSourceConcurrentConsumersAllRecorded: N goroutines each RegisterSource a
// distinct consumer for the same ref against one shared registry file with no external
// lock. A plain read-modify-write race would lose updates. This is the adversarial check
// on the comment's own claim ("dedupe against each other's writes without any in-process
// shared state") — if writes race, both the idempotency contract and the FB6-adjacent
// registry accounting silently under-count consumers.
func TestRegisterSourceConcurrentConsumersAllRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = RegisterSource(path, "ref-a", "consumer-"+string(rune('A'+i)), Now())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("RegisterSource[%d]: %v", i, err)
		}
	}
	doc, err := Read(path, RegistrySchemaVersion, registryMigrations)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	entry := readRefs(doc)["ref-a"]
	if len(entry.Consumers) != n {
		t.Fatalf("concurrent RegisterSource calls: got %d distinct consumers recorded, want %d — writes lost under concurrency (no file lock in RegisterSource)", len(entry.Consumers), n)
	}
}

// TestRegisterSourceRepeatSameConsumerNoDuplication is the base idempotency case from a
// single caller re-registering.
func TestRegisterSourceRepeatSameConsumerNoDuplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	for i := 0; i < 5; i++ {
		if err := RegisterSource(path, "ref-b", "consumer-x", Now()); err != nil {
			t.Fatalf("RegisterSource call %d: %v", i, err)
		}
	}
	doc, _ := Read(path, RegistrySchemaVersion, registryMigrations)
	entry := readRefs(doc)["ref-b"]
	if len(entry.Consumers) != 1 || entry.Consumers[0] != "consumer-x" {
		t.Fatalf("repeat registration duplicated: %+v", entry.Consumers)
	}
}

// TestWriteTasksMixedValidAndInvalidRefusesWholeBatch confirms an all-or-nothing write: one
// bad row among many valid ones refuses the entire batch rather than partially persisting.
func TestWriteTasksMixedValidAndInvalidRefusesWholeBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	tasks := []Task{
		{ID: "ok-1", Status: StatusDone, CommitSHA: "sha1"},
		{ID: "ok-2", Status: StatusNotStarted},
		{ID: "bad", Status: StatusDone},
		{ID: "ok-3", Status: StatusDone, CommitSHA: "sha3"},
	}
	if err := WriteTasks(path, tasks, 0o644); err == nil {
		t.Fatalf("WriteTasks accepted a batch containing an invalid row")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("partial batch write leaked a file to disk despite refusal")
	}
}

// TestErrForwardVersionMessageNamesBothVersions: the refusal must be loud and actionable —
// name both the found and known versions in its message, not just say "refused".
func TestErrForwardVersionMessageNamesBothVersions(t *testing.T) {
	_, err := Migrate(Doc{versionKey: 7}, 3, Migrations{})
	if err == nil {
		t.Fatal("want ErrForwardVersion")
	}
	msg := err.Error()
	if !strings.Contains(msg, "7") || !strings.Contains(msg, "3") {
		t.Fatalf("ErrForwardVersion message %q does not name both found (7) and known (3) versions", msg)
	}
}
