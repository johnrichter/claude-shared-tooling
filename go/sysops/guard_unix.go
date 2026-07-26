//go:build !windows

package sysops

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
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

// freeMemoryBytes reads available memory from /proc/meminfo's
// MemAvailable field (the kernel's own estimate of memory a new workload
// could claim without swapping). On a host without /proc/meminfo (e.g.
// macOS) it falls back to reporting an error rather than guessing.
func freeMemoryBytes() (uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed /proc/meminfo MemAvailable line %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse /proc/meminfo MemAvailable: %w", err)
		}
		return kb * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan /proc/meminfo: %w", err)
	}
	return 0, fmt.Errorf("/proc/meminfo has no MemAvailable field")
}
