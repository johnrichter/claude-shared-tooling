//go:build darwin

package sysops

import "testing"

// TestFreeMemoryBytes_ReportsPositiveValue checks the darwin vm_stat path
// returns a usable free-memory figure rather than failing outright the way
// it did when this file inherited guard_unix.go's /proc/meminfo read.
func TestFreeMemoryBytes_ReportsPositiveValue(t *testing.T) {
	free, err := freeMemoryBytes()
	if err != nil {
		t.Fatalf("freeMemoryBytes: %v", err)
	}
	if free == 0 {
		t.Fatalf("free = 0, want > 0")
	}
}
