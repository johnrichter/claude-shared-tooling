package sysops

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestRun_ExternalCancelReportsCanceled checks a caller-driven ctx.Cancel
// (not a timeout) surfaces as context.Canceled, per Run's documented
// contract that both DeadlineExceeded and Canceled are surfaced.
func TestRun_ExternalCancelReportsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, "sleep", []string{"5"}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestRun_TimeoutKillsGrandchildProcessGroup checks a timed-out run kills a
// grandchild spawned by the child (via process-group signalling), not just
// the immediate child — a shell subshell holding stdout open would
// otherwise survive and wedge future fd/process accounting.
func TestRun_TimeoutKillsGrandchildProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill is unix-only; setupProcessGroup is a no-op on windows")
	}
	// Child forks a detached-from-signal-delivery grandchild (sh -c '(sleep 30)&')
	// then exits immediately; without group-kill the grandchild would survive
	// Run's cancellation of the direct child.
	start := time.Now()
	_, err := Run(context.Background(), "sh", []string{"-c", "(sleep 30 &) ; sleep 30"}, Options{Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run took %v after a 100ms timeout", elapsed)
	}
}

// TestRun_ZeroTimeoutMeansNoDeadline checks Options.Timeout left at zero
// does not impose an artificial deadline beyond ctx — a quick command well
// under any reasonable timeout still succeeds normally.
func TestRun_ZeroTimeoutMeansNoDeadline(t *testing.T) {
	res, err := Run(context.Background(), "sh", []string{"-c", "sleep 0.2; echo done"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "done\n" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "done\n")
	}
}

// TestPlatform_KnownOSValue sanity-checks Platform().OS is one of Go's
// known GOOS identifiers, not an empty or malformed string.
func TestPlatform_KnownOSValue(t *testing.T) {
	p := Platform()
	known := map[string]bool{"linux": true, "darwin": true, "windows": true, "freebsd": true, "openbsd": true, "netbsd": true}
	if !known[p.OS] {
		t.Fatalf("OS = %q, not a recognized GOOS value", p.OS)
	}
	if p.Arch == "" {
		t.Fatalf("Arch is empty")
	}
}
