//go:build !windows

package sysops

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup puts the child in its own process group and redirects
// cancellation to signal that whole group, not just the immediate child. A
// child that spawns its own children — a shell running a long-lived
// grandchild, say — would otherwise leave those grandchildren alive holding
// the output pipe open, wedging Wait long past the deadline. Setpgid makes
// the child a group leader (group id == child pid), so a negative-pid kill
// reaches the entire tree.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
