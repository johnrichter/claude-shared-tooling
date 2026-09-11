//go:build linux

package sysops

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// freeMemoryBytes reads available memory from /proc/meminfo's
// MemAvailable field (the kernel's own estimate of memory a new workload
// could claim without swapping).
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
