//go:build windows

package sysops

import "os/exec"

// setupProcessGroup is a no-op on Windows, which has no POSIX process groups.
// Timeout enforcement falls back to exec's default single-process kill plus
// cmd.WaitDelay, which bounds how long Wait blocks on pipe handles a surviving
// grandchild may still hold.
func setupProcessGroup(cmd *exec.Cmd) {}
