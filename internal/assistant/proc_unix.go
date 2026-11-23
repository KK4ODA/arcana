//go:build !linux && !windows

package assistant

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
