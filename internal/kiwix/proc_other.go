//go:build !windows

package kiwix

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
