//go:build !windows

package sysops

import (
	"fmt"
	"syscall"
)

// openFileLimit reads the process's RLIMIT_NOFILE via getrlimit(2).
func openFileLimit() (RlimitInfo, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return RlimitInfo{}, fmt.Errorf("getrlimit RLIMIT_NOFILE: %w", err)
	}
	return RlimitInfo{Soft: uint64(rl.Cur), Hard: uint64(rl.Max)}, nil
}
