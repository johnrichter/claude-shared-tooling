package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SDET probes beyond the implementer's own adversarial suite (adversarial_te_test.go).
// These target lock lifecycle, RegisterSource-specific corruption, and cross-invariant
// composition not exercised elsewhere.

// TestWithLockBreaksStaleLockFromCrashedHolder: a lock file left behind by a process that
// died mid-critical-section must not wedge every future caller forever — WithLock should
// detect its age exceeds staleLockAge and break it rather than blocking the full
// lockTimeout.
func TestWithLockBreaksStaleLockFromCrashedHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	ran := false
	start := time.Now()
	if err := WithLock(path, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !ran {
		t.Fatal("WithLock did not run fn after breaking a stale lock")
	}
	if elapsed := time.Since(start); elapsed >= lockTimeout {
		t.Fatalf("WithLock waited out the full lockTimeout (%s) instead of breaking the stale lock promptly; elapsed %s", lockTimeout, elapsed)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("WithLock left a lock file behind after fn returned: stat err=%v", err)
	}
}

// TestWithLockTimesOutOnFreshContendedLock: a lock held by a live (recently-touched) holder
// must NOT be broken early — WithLock must actually wait, and give up loudly (not hang
// forever, not silently proceed) once lockTimeout elapses.
func TestWithLockTimesOutOnFreshContendedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Fresh mtime (just written) -- must not be treated as stale.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		// Keep refreshing mtime so this test doesn't depend on lockTimeout >
		// staleLockAge ordering assumptions beyond what the package already declares.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				now := time.Now()
				os.Chtimes(lockPath, now, now)
			}
		}
	}()
	err := WithLock(path, func() error { t.Fatal("fn ran despite contended, live lock"); return nil })
	if err == nil {
		t.Fatal("WithLock returned nil despite a permanently-held, non-stale lock")
	}
}

// TestRegisterSourceOnCorruptRegistryDegradesAndRecovers: RegisterSource against a registry
// file with syntactically valid JSON but a malformed "refs" field must not raise — it
// should degrade like Read does, and still successfully record the new registration rather
// than failing the whole call.
func TestRegisterSourceOnCorruptRegistryDegradesAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	bad := []byte(`{"_schema_version": 1, "refs": "not-a-map"}`)
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterSource(path, "ref-z", "consumer-1", Now()); err != nil {
		t.Fatalf("RegisterSource on malformed refs field: %v, want nil (safe degradation)", err)
	}
	if !SeenSource(path, "ref-z") {
		t.Fatal("RegisterSource did not persist despite degrading past the malformed refs field")
	}
}

// TestRegisterSourceOnTrulyCorruptJSONStillSucceeds: registry file is not even valid JSON.
// RegisterSource must degrade to an empty base document and still complete the write,
// consistent with Read's "never raise" contract propagating through RegisterSource.
func TestRegisterSourceOnTrulyCorruptJSONStillSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterSource(path, "ref-y", "consumer-1", Now()); err != nil {
		t.Fatalf("RegisterSource on corrupt JSON: %v, want nil", err)
	}
	if !SeenSource(path, "ref-y") {
		t.Fatal("registration lost after recovering from corrupt JSON")
	}
}

// TestRegisterSourceRefusesOnForwardVersionRegistry: a registry file stamped with a newer
// schema version than this build knows must propagate the forward-version refusal rather
// than silently overwriting it with a stale-format document — same contract Read documents
// for any consumer, RegisterSource included since it wraps Read.
func TestRegisterSourceRefusesOnForwardVersionRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	future := []byte(`{"_schema_version": 999, "refs": {}}`)
	if err := os.WriteFile(path, future, 0o644); err != nil {
		t.Fatal(err)
	}
	err := RegisterSource(path, "ref-w", "consumer-1", Now())
	if err == nil {
		t.Fatal("RegisterSource silently proceeded against a forward-version registry file")
	}
	if _, ok := err.(*ErrForwardVersion); !ok {
		t.Fatalf("RegisterSource error type = %T, want *ErrForwardVersion", err)
	}
	onDisk, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(onDisk) != string(future) {
		t.Fatalf("RegisterSource mutated a forward-version file it refused: got %q", onDisk)
	}
}

// TestWriteTasksRefusesButLeavesPriorFileIntact: a refused WriteTasks call over a path that
// already holds a valid prior state file must leave that prior file byte-for-byte
// untouched -- the "nothing written" guarantee must hold even when there IS something on
// disk already, not just in the empty-path case adversarial_te_test.go covers.
func TestWriteTasksRefusesButLeavesPriorFileIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	good := []Task{{ID: "T1", Status: StatusDone, CommitSHA: "sha-good"}}
	if err := WriteTasks(path, good, 0o644); err != nil {
		t.Fatalf("seed WriteTasks: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := []Task{{ID: "T2", Status: StatusDone}}
	if err := WriteTasks(path, bad, 0o644); err == nil {
		t.Fatal("WriteTasks accepted an invalid batch over an existing valid file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("refused WriteTasks clobbered the prior valid file:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestRecordTaskDoneWithZeroCostAndRealShaIsValid: guards against an over-broad rung-1 guard
// that might reject CostUSD == 0 alongside done -- the design explicitly scopes the
// invariant to Status/CommitSHA only (doc.go, record.go doc comments); CostUSD left at its
// zero value is a legitimate done state.
func TestRecordTaskDoneWithZeroCostAndRealShaIsValid(t *testing.T) {
	task := Task{ID: "T1", Status: StatusInProgress}
	zero := 0.0
	if err := RecordTask(&task, statusPtr(StatusDone), stringPtr("sha-real"), &zero); err != nil {
		t.Fatalf("RecordTask refused done+real-sha+zero-cost: %v, want nil", err)
	}
	if task.Status != StatusDone || task.CommitSHA != "sha-real" || task.CostUSD != 0 {
		t.Fatalf("task after valid transition = %+v", task)
	}
}
