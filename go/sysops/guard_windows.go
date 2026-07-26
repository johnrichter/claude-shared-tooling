//go:build windows

package sysops

import "fmt"

// ErrUnsupportedPlatform reports a Guard check with no implementation on
// the running platform.
var ErrUnsupportedPlatform = fmt.Errorf("sysops: not supported on %s", "windows")

// openFileLimit has no ulimit equivalent on Windows.
func openFileLimit() (RlimitInfo, error) {
	return RlimitInfo{}, ErrUnsupportedPlatform
}

// freeMemoryBytes has no /proc/meminfo equivalent wired here for Windows.
func freeMemoryBytes() (uint64, error) {
	return 0, ErrUnsupportedPlatform
}
