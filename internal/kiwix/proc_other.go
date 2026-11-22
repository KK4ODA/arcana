//go:build !windows

package kiwix

import (
	"os"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

// exitHint explains an exit status that has a known cause, or returns "".
func exitHint(*os.ProcessState) string { return "" }
