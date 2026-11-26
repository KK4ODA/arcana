package apps

import (
	"os/exec"
	"syscall"
)

// configureProcess makes the program exit if Arcana dies without stopping
// it (a crash, or the drive being pulled).
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func terminate(cmd *exec.Cmd) { _ = cmd.Process.Signal(syscall.SIGTERM) }

type jobHandle struct{}

func attachJob(*exec.Cmd) jobHandle { return jobHandle{} }
func (jobHandle) close()            {}
