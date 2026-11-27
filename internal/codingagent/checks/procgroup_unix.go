//go:build !windows

package checks

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in its own process group and makes cancelling it kill the whole group: a checker's children (go vet's tools,
// rustc under cargo, node behind a wrapper script) end with it instead of running on and holding its output pipes.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
