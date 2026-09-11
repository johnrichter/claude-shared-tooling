//go:build darwin

package sysops

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// freeMemoryBytes reports available memory by shelling out to vm_stat and
// multiplying its reported page size by its "Pages free" count. macOS has
// no /proc/meminfo equivalent, and the stdlib syscall package exposes only
// string-valued sysctls on darwin — not the numeric vm.* MIB entries this
// would otherwise need — so vm_stat is the portable, dependency-free path.
func freeMemoryBytes() (uint64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("run vm_stat: %w", err)
	}

	var pageSize, pagesFree uint64
	sawPageSize := false
	sawPagesFree := false
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, "page size of"):
			fields := strings.Fields(line)
			for i, f := range fields {
				if f != "of" || i+1 >= len(fields) {
					continue
				}
				n, err := strconv.ParseUint(fields[i+1], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse vm_stat page size %q: %w", line, err)
				}
				pageSize = n
				sawPageSize = true
			}
		case strings.HasPrefix(line, "Pages free:"):
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return 0, fmt.Errorf("malformed vm_stat Pages free line %q", line)
			}
			n, err := strconv.ParseUint(strings.TrimSuffix(fields[2], "."), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse vm_stat Pages free: %w", err)
			}
			pagesFree = n
			sawPagesFree = true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan vm_stat output: %w", err)
	}
	if !sawPageSize {
		return 0, fmt.Errorf("vm_stat output has no page-size header")
	}
	if !sawPagesFree {
		return 0, fmt.Errorf("vm_stat output has no Pages free field")
	}
	return pagesFree * pageSize, nil
}
