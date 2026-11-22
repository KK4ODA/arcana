//go:build windows

package kiwix

import (
	"os"
	"os/exec"
	"syscall"
)

// hideWindow stops kiwix-serve from opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// Windows ends a program with these statuses when it cannot load it.
const (
	statusDLLNotFound        = 0xC0000135
	statusEntryPointNotFound = 0xC0000139
)

// exitHint explains an exit status that has a known cause, or returns "".
// kiwix-serve.exe needs the Microsoft Visual C++ runtime (VCRUNTIME140.dll,
// MSVCP140.dll), which Kiwix does not ship and a new Windows installation
// may lack.
func exitHint(st *os.ProcessState) string {
	if st == nil {
		return ""
	}
	switch uint32(st.ExitCode()) {
	case statusDLLNotFound, statusEntryPointNotFound:
		return "kiwix-serve could not start because the Microsoft Visual C++ runtime is missing or out of date on this computer. " +
			"Install the Microsoft Visual C++ Redistributable (x64) from Microsoft, then restart Arcana. " +
			"Until then, ZIM content cannot be opened; maps, documents and notes still work."
	}
	return ""
}
