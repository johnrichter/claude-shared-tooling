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
			// Anchor on the full phrase rather than scanning for a bare "of"
			// token: any other "of" earlier in the header would otherwise be
			// parsed as the page-size marker and fail on the word after it.
			_, after, _ := strings.Cut(line, "page size of")
			fields := strings.Fields(after)
			if len(fields) == 0 {
				return 0, fmt.Errorf("malformed vm_stat page-size header %q", line)
			}
			n, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse vm_stat page size %q: %w", line, err)
			}
			// A zero page size is never a real reading. Accepting one would
			// make the closing multiplication yield 0 bytes free, which a
			// caller reads as "host is out of memory" rather than "vm_stat
			// output was unusable" -- the same silent-failure mode a missing
			// "Pages free:" line already guards against below.
			if n == 0 {
				return 0, fmt.Errorf("vm_stat reported a zero page size in %q", line)
			}
			pageSize = n
			sawPageSize = true
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
