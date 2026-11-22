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
// kiwix-serve.exe needs the Microsoft Visual C++ runtime (msvcp140.dll,
// vcruntime140.dll, vcruntime140_1.dll). Kiwix does not ship it; Arcana's
// Windows build puts it beside kiwix-serve.exe, so this means those files
// are missing or damaged.
func exitHint(st *os.ProcessState) string {
	if st == nil {
		return ""
	}
	switch uint32(st.ExitCode()) {
	case statusDLLNotFound, statusEntryPointNotFound:
		return "kiwix-serve could not start because the Microsoft Visual C++ runtime could not be loaded. " +
			"Arcana ships it beside kiwix-serve (bin\\windows-amd64\\kiwix); if those files are missing or damaged, " +
			"copy Arcana onto the drive again, or install the Microsoft Visual C++ Redistributable (x64) from Microsoft, then restart Arcana. " +
			"Until then, ZIM content cannot be opened; maps, documents and notes still work."
	}
	return ""
}
