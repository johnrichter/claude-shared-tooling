//go:build darwin

package sysops

import (
	"os"
	"path/filepath"
	"testing"
)

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

// withFakeVMStat puts a shell script named vm_stat, printing body, first on
// PATH for the duration of the test, so freeMemoryBytes's exec.Command
// ("vm_stat") resolves to a controlled fixture instead of the real tool.
// This exercises freeMemoryBytes's actual parsing path -- not a stand-in
// helper -- without needing production code changes to be testable.
func withFakeVMStat(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "vm_stat")
	content := "#!/bin/sh\ncat <<'EOF'\n" + body + "\nEOF\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake vm_stat: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestFreeMemoryBytes_MissingPagesFreeLine_ReturnsError guards against the
// case a vm_stat output has a valid page-size header but no "Pages free:"
// line at all (e.g. a future macOS renames or drops the field). The parser
// must fail loudly rather than silently report 0 bytes free, which a
// caller could otherwise mistake for "system is out of memory".
func TestFreeMemoryBytes_MissingPagesFreeLine_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages active: 100.")
	free, err := freeMemoryBytes()
	if err == nil {
		t.Fatalf("freeMemoryBytes: got (%d, nil), want an error when \"Pages free:\" is absent", free)
	}
}

// TestFreeMemoryBytes_UnparsablePagesFreeNumber_ReturnsError covers a
// "Pages free:" line whose count field is not a valid integer.
func TestFreeMemoryBytes_UnparsablePagesFreeNumber_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: notanumber.")
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error for unparsable Pages free count")
	}
}

// TestFreeMemoryBytes_TruncatedPagesFreeLine_ReturnsError covers a
// "Pages free:" line with no count field at all.
func TestFreeMemoryBytes_TruncatedPagesFreeLine_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free:")
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error for truncated Pages free line")
	}
}

// TestFreeMemoryBytes_MissingPageSizeHeader_ReturnsError covers vm_stat
// output missing the page-size header entirely.
func TestFreeMemoryBytes_MissingPageSizeHeader_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Pages free: 100.")
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error when page-size header is absent")
	}
}

// TestFreeMemoryBytes_UnparsablePageSize_ReturnsError covers a page-size
// header whose numeric field is not a valid integer.
func TestFreeMemoryBytes_UnparsablePageSize_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of notanumber bytes)\nPages free: 100.")
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error for unparsable page size")
	}
}

// TestFreeMemoryBytes_ZeroPageSize_ReturnsError covers a page-size header
// that parses cleanly but reports 0. Multiplying it out would return
// (0, nil) -- indistinguishable to a caller from a genuinely exhausted
// host -- so the parser must reject it the way it rejects a missing
// "Pages free:" line.
func TestFreeMemoryBytes_ZeroPageSize_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of 0 bytes)\nPages free: 100.")
	free, err := freeMemoryBytes()
	if err == nil {
		t.Fatalf("freeMemoryBytes: got (%d, nil), want an error for a zero page size", free)
	}
}

// TestFreeMemoryBytes_PageSizeHeaderWithNoCount_ReturnsError covers a
// header ending at the "page size of" marker with no number after it.
func TestFreeMemoryBytes_PageSizeHeaderWithNoCount_ReturnsError(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics: (page size of\nPages free: 100.")
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error for a page-size header with no count")
	}
}

// TestFreeMemoryBytes_PageSizeHeaderWithEarlierOfToken_Parses pins the
// parser to the "page size of" phrase. A header carrying an unrelated
// "of" ahead of the marker must still yield the real page size, not an
// error from parsing the word that follows the first "of".
func TestFreeMemoryBytes_PageSizeHeaderWithEarlierOfToken_Parses(t *testing.T) {
	withFakeVMStat(t, "Mach Virtual Memory Statistics of host 0: (page size of 16384 bytes)\nPages free: 3.")
	free, err := freeMemoryBytes()
	if err != nil {
		t.Fatalf("freeMemoryBytes: %v", err)
	}
	if want := uint64(3 * 16384); free != want {
		t.Fatalf("free = %d, want %d", free, want)
	}
}

// TestFreeMemoryBytes_VmStatNotFound_ReturnsError covers the vm_stat
// binary being unavailable on PATH.
func TestFreeMemoryBytes_VmStatNotFound_ReturnsError(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	if _, err := freeMemoryBytes(); err == nil {
		t.Fatal("freeMemoryBytes: got nil error, want error when vm_stat is not on PATH")
	}
}
