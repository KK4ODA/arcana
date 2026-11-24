//go:build windows

package kolibri

import (
	"os/exec"
	"syscall"
)

// hideWindow stops Python from opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
