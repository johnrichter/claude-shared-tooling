package sysops

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestRun_CapturesStdout checks a successful child's stdout is captured
// byte-for-byte.
func TestRun_CapturesStdout(t *testing.T) {
	res, err := Run(context.Background(), "printf", []string{"hello"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := string(res.Stdout); got != "hello" {
		t.Fatalf("Stdout = %q, want %q", got, "hello")
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

// TestRun_BoundsCapture checks a stream exceeding its configured limit is
// truncated, not buffered in full.
func TestRun_BoundsCapture(t *testing.T) {
	res, err := Run(context.Background(), "sh", []string{"-c", "printf '%01000d' 0"}, Options{MaxStdoutBytes: 16})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stdout) != 16 {
		t.Fatalf("captured %d bytes, want 16", len(res.Stdout))
	}
	if !res.StdoutTruncated {
		t.Fatalf("StdoutTruncated = false, want true")
	}
}

// TestRun_TimeoutEnforced checks a run exceeding its timeout is killed and
// reports context.DeadlineExceeded.
func TestRun_TimeoutEnforced(t *testing.T) {
	_, err := Run(context.Background(), "sleep", []string{"5"}, Options{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestRun_SpawnFailureWrapped checks a missing binary surfaces as an error
// rather than a silent zero-value Result.
func TestRun_SpawnFailureWrapped(t *testing.T) {
	_, err := Run(context.Background(), "sysops-nonexistent-binary-xyz", nil, Options{})
	if err == nil {
		t.Fatalf("expected error for missing binary")
	}
}

// TestRun_NonZeroExitIsNotAnError checks a child exiting non-zero is a
// normal, structured outcome (read via Result.ExitCode), not a returned
// error.
func TestRun_NonZeroExitIsNotAnError(t *testing.T) {
	res, err := Run(context.Background(), "sh", []string{"-c", "exit 3"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", res.ExitCode)
	}
}

// TestGuard_PreflightMemory checks Preflight passes a trivial floor and
// fails an impossible one.
func TestGuard_PreflightMemory(t *testing.T) {
	g := NewGuard()
	if runtime.GOOS != "linux" {
		t.Skip("free-memory preflight only implemented for linux in this test")
	}
	if err := g.Preflight(Requirement{MinFreeMemoryBytes: 1}); err != nil {
		t.Fatalf("Preflight with trivial floor: %v", err)
	}
	if err := g.Preflight(Requirement{MinFreeMemoryBytes: 1 << 62}); err == nil {
		t.Fatalf("Preflight with impossible floor: want error, got nil")
	}
}

// TestGuard_OpenFileLimit checks OpenFileLimit reports a positive soft
// limit on a supported platform and an explicit error on Windows.
func TestGuard_OpenFileLimit(t *testing.T) {
	g := NewGuard()
	lim, err := g.OpenFileLimit()
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatalf("expected unsupported error on windows")
		}
		return
	}
	if err != nil {
		t.Fatalf("OpenFileLimit: %v", err)
	}
	if lim.Soft == 0 {
		t.Fatalf("Soft = 0, want > 0")
	}
}

// TestPlatform_ReportsRuntimeValues checks Platform mirrors runtime.GOOS
// and runtime.GOARCH.
func TestPlatform_ReportsRuntimeValues(t *testing.T) {
	p := Platform()
	if p.OS != runtime.GOOS {
		t.Fatalf("OS = %q, want %q", p.OS, runtime.GOOS)
	}
	if p.Arch != runtime.GOARCH {
		t.Fatalf("Arch = %q, want %q", p.Arch, runtime.GOARCH)
	}
	if p.String() != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("String() = %q", p.String())
	}
}
