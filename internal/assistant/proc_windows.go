package assistant

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// configureProcess keeps the engine from opening a console window.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
