package sysops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// DefaultMaxCaptureBytes bounds a captured stream when Options.MaxStdoutBytes
// or Options.MaxStderrBytes is left at zero. It caps a single stream well
// below a typical context-window budget while staying generous for normal
// command output.
const DefaultMaxCaptureBytes = 1 << 20 // 1 MiB

// Options configures a subprocess run.
type Options struct {
	// Dir is the working directory. Empty means the caller's process cwd.
	Dir string
	// Env is the child's environment. Nil means it inherits the caller's.
	Env []string
	// Stdin, if set, is copied to the child's standard input.
	Stdin io.Reader
	// Timeout bounds the run's wall-clock duration. Zero means no timeout
	// beyond whatever ctx already carries.
	Timeout time.Duration
	// MaxStdoutBytes and MaxStderrBytes cap each stream's captured size.
	// Zero means DefaultMaxCaptureBytes.
	MaxStdoutBytes int
	MaxStderrBytes int
}

// Result is one subprocess run's structured outcome.
type Result struct {
	// ExitCode is the child's exit status, or -1 if it never ran or exited
	// on a signal.
	ExitCode int
	// Stdout and Stderr hold each stream's captured bytes, up to the run's
	// configured limit.
	Stdout []byte
	Stderr []byte
	// StdoutTruncated and StderrTruncated report whether the child wrote
	// more than the configured limit; the excess was discarded, not
	// buffered.
	StdoutTruncated bool
	StderrTruncated bool
	// Duration is the wall-clock time from spawn to exit (or to
	// cancellation).
	Duration time.Duration
}

// boundedBuffer caps how many bytes it retains, discarding the remainder and
// recording that it did. It never grows past its limit, so a runaway or
// hostile child cannot force unbounded memory use through output alone.
type boundedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// Run spawns name with args, capturing stdout and stderr into bounded
// buffers and enforcing opts.Timeout (if set) on top of ctx. It returns a
// populated Result even on failure — including a non-zero exit or a
// timeout — so a caller can inspect whatever the child produced before it
// failed. Spawn and wait failures are wrapped with the command name for
// context; err is nil only when the child ran to completion.
func Run(ctx context.Context, name string, args []string, opts Options) (*Result, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	maxOut := opts.MaxStdoutBytes
	if maxOut <= 0 {
		maxOut = DefaultMaxCaptureBytes
	}
	maxErr := opts.MaxStderrBytes
	if maxErr <= 0 {
		maxErr = DefaultMaxCaptureBytes
	}
	stdout := &boundedBuffer{limit: maxOut}
	stderr := &boundedBuffer{limit: maxErr}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = opts.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setupProcessGroup(cmd)
	// Backstop the cancellation path: once the child is signalled, don't let a
	// still-open output pipe (e.g. held by a surviving grandchild) block Wait
	// indefinitely. WaitDelay force-closes the pipes after this grace period.
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	runErr := cmd.Run()
	res := &Result{
		ExitCode:        cmd.ProcessState.ExitCode(),
		Stdout:          stdout.buf.Bytes(),
		Stderr:          stderr.buf.Bytes(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
		Duration:        time.Since(start),
	}
	if runErr == nil {
		return res, nil
	}

	// cmd.Run wraps context cancellation as "signal: killed" or similar,
	// losing the reason; surface the context error explicitly so callers can
	// errors.Is it (DeadlineExceeded for a timeout, Canceled otherwise)
	// rather than mistaking a killed child for a clean run.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("sysops: run %q: %w", name, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// A non-zero exit is a normal, structured outcome, not a spawn
		// failure: the caller reads res.ExitCode rather than treating this
		// as an infrastructure error.
		return res, nil
	}
	return res, fmt.Errorf("sysops: run %q: %w", name, runErr)
}
