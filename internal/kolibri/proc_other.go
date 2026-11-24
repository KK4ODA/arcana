//go:build !windows

package kolibri

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
