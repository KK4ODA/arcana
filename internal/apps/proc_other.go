//go:build !linux && !windows

package apps

import (
	"os/exec"
	"syscall"
)

// configureProcess does nothing here: macOS has no way to end a child
// when its parent dies, so a program left by a crashed Arcana runs until
// it is stopped or the computer restarts (ADR-0030).
func configureProcess(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd) { _ = cmd.Process.Signal(syscall.SIGTERM) }

type jobHandle struct{}

func attachJob(*exec.Cmd) jobHandle { return jobHandle{} }
func (jobHandle) close()            {}
