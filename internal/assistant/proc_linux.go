package assistant

import (
	"os/exec"
	"syscall"
)

// configureProcess makes the engine exit if Arcana dies without stopping
// it (a crash, or the drive being pulled).
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
