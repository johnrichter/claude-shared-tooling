package bh

import (
	"testing"
)

// Adversarial: Archive claims to be pure ("callers own reading the three docs and atomically
// writing the three results back") — verify it never mutates its ExecState input in place via
// slice-aliasing (ex.Log has spare capacity that append could silently reuse).
func TestArchive_DoesNotMutateInputExecState(t *testing.T) {
	p := archivePlan()
	ex := doneM1Exec(t)
	// Force spare capacity on Log so a naive append(ex.Log, ...) would alias the backing array.
	grown := make([]string, len(ex.Log), len(ex.Log)+8)
	copy(grown, ex.Log)
	ex.Log = grown
	snapshot := ex.Log
	snapshotLen := len(snapshot)

	_, err := Archive(p, ex, ArchiveDoc{}, ArchiveOptions{MilestoneIDs: []string{"M1"}}, at0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Log) != snapshotLen {
		t.Fatalf("Archive mutated caller's ExecState.Log length: got %d, want %d", len(ex.Log), snapshotLen)
	}
	// Re-slice to full cap and check no stray entry was written into shared backing array.
	full := snapshot[:cap(snapshot)]
	for i := snapshotLen; i < len(full); i++ {
		if full[i] != "" {
			t.Fatalf("Archive wrote into caller's Log backing array beyond its length at index %d: %q", i, full[i])
		}
	}
}
