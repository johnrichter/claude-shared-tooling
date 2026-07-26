package sysops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRun_BoundsStderrIndependently checks stderr is capped independently
// of stdout, not sharing one combined limit.
func TestRun_BoundsStderrIndependently(t *testing.T) {
	res, err := Run(context.Background(), "sh", []string{"-c", "printf '%01000d' 0 1>&2"}, Options{MaxStderrBytes: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stderr) != 8 {
		t.Fatalf("captured %d stderr bytes, want 8", len(res.Stderr))
	}
	if !res.StderrTruncated {
		t.Fatalf("StderrTruncated = false, want true")
	}
	if res.StdoutTruncated {
		t.Fatalf("StdoutTruncated = true, want false (nothing written to stdout)")
	}
}

// TestRun_DefaultCapIsBoundedNotUnbounded checks a stream far larger than
// the 1 MiB default is truncated rather than fully buffered — the core
// claim of the task (no unbounded buffers).
func TestRun_DefaultCapIsBoundedNotUnbounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-output test in short mode")
	}
	// Write ~4 MiB of zero bytes via /dev/zero through dd, far past the
	// 1 MiB default cap.
	res, err := Run(context.Background(), "dd", []string{"if=/dev/zero", "bs=1M", "count=4"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stdout) != DefaultMaxCaptureBytes {
		t.Fatalf("captured %d bytes, want exactly the %d-byte default cap", len(res.Stdout), DefaultMaxCaptureBytes)
	}
	if !res.StdoutTruncated {
		t.Fatalf("StdoutTruncated = false, want true for 4 MiB of output against a 1 MiB cap")
	}
}

// TestRun_KilledBySignalReportsExitCodeNegativeOne checks a child killed by
// a signal (not a normal exit) reports ExitCode -1, distinct from any real
// exit status, and is not treated as a spawn-failure error.
func TestRun_KilledBySignalReportsExitCodeNegativeOne(t *testing.T) {
	res, err := Run(context.Background(), "sh", []string{"-c", "kill -KILL $$"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v, want nil (self-kill is a structured outcome)", err)
	}
	if res.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1 for a signal-killed child", res.ExitCode)
	}
}

// TestRun_TimeoutKillsProcessAndReleasesResources checks a timed-out run
// actually terminates the child (no wedged process) and Run returns
// promptly rather than blocking past the deadline.
func TestRun_TimeoutKillsProcessAndReleasesResources(t *testing.T) {
	start := time.Now()
	res, err := Run(context.Background(), "sh", []string{"-c", "sleep 30"}, Options{Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run took %v after a 100ms timeout — child process likely not killed/released", elapsed)
	}
	if res == nil {
		t.Fatalf("Result = nil, want a populated Result even on timeout")
	}
}

// TestRun_NoFileDescriptorLeak checks repeated runs with piped stdin/stdout
// don't leak file descriptors — a symptom of resources not being released
// per run.
func TestRun_NoFileDescriptorLeak(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fd-count check via /proc is linux-specific")
	}
	countFDs := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatalf("ReadDir /proc/self/fd: %v", err)
		}
		return len(entries)
	}
	before := countFDs()
	for i := 0; i < 50; i++ {
		_, err := Run(context.Background(), "sh", []string{"-c", "echo hi"}, Options{Stdin: strings.NewReader("input")})
		if err != nil {
			t.Fatalf("Run iteration %d: %v", i, err)
		}
	}
	after := countFDs()
	if after > before+2 {
		t.Fatalf("fd count grew from %d to %d after 50 runs — suspected descriptor leak", before, after)
	}
}

// TestRun_StdinIsCopiedToChild checks Options.Stdin actually reaches the
// child process.
func TestRun_StdinIsCopiedToChild(t *testing.T) {
	res, err := Run(context.Background(), "cat", nil, Options{Stdin: bytes.NewBufferString("piped-input")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "piped-input" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "piped-input")
	}
}

// TestRun_InvalidDirWrapsError checks a nonexistent working directory
// surfaces as a wrapped spawn-failure error, not a panic or silent zero
// Result.
func TestRun_InvalidDirWrapsError(t *testing.T) {
	_, err := Run(context.Background(), "echo", []string{"hi"}, Options{Dir: "/nonexistent/path/xyz-sysops"})
	if err == nil {
		t.Fatalf("expected error for nonexistent Dir")
	}
}

// TestGuard_ZeroRequirementIsNoOp checks a Requirement with both fields at
// zero never fails, regardless of host state.
func TestGuard_ZeroRequirementIsNoOp(t *testing.T) {
	g := NewGuard()
	if err := g.Preflight(Requirement{}); err != nil {
		t.Fatalf("Preflight with zero Requirement: %v, want nil", err)
	}
}

// TestGuard_PreflightOpenFilesBoundary checks the open-file-limit check
// fails when the floor exceeds the actual soft limit and passes at a
// trivial floor, exercising the boundary rather than only the pass case.
func TestGuard_PreflightOpenFilesBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no ulimit equivalent on windows")
	}
	g := NewGuard()
	lim, err := g.OpenFileLimit()
	if err != nil {
		t.Fatalf("OpenFileLimit: %v", err)
	}
	if err := g.Preflight(Requirement{MinOpenFiles: lim.Soft + 1_000_000}); err == nil {
		t.Fatalf("Preflight with floor above actual soft limit: want error, got nil")
	}
	if err := g.Preflight(Requirement{MinOpenFiles: 1}); err != nil {
		t.Fatalf("Preflight with trivial open-files floor: %v, want nil", err)
	}
}

// TestGuard_PreflightCombinedRequirementFailsOnFirstUnmet checks a combined
// memory+open-files Requirement reports failure when either sub-check
// fails, not just when both do.
func TestGuard_PreflightCombinedRequirementFailsOnFirstUnmet(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("free-memory preflight only implemented for linux in this test")
	}
	g := NewGuard()
	err := g.Preflight(Requirement{MinFreeMemoryBytes: 1 << 62, MinOpenFiles: 1})
	if err == nil {
		t.Fatalf("Preflight with impossible memory floor + trivial open-files floor: want error, got nil")
	}
}
