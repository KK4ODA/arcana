//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the engine in its own process group, so the shell that
// starts llamafile and llamafile itself are stopped together.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
