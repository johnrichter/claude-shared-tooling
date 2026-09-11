//go:build !windows && !linux && !darwin

package sysops

import (
	"fmt"
	"runtime"
)

// freeMemoryBytes has no implementation wired here for this platform
// (a BSD, most likely). openFileLimit still works, via the shared
// getrlimit(2) path in guard_unix.go.
func freeMemoryBytes() (uint64, error) {
	return 0, fmt.Errorf("sysops: free-memory check not supported on %s", runtime.GOOS)
}
