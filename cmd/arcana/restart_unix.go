//go:build !windows

package main

import (
	"os"
	"syscall"
)

// relaunch replaces this process with the newly installed program, keeping
// the same terminal and process ID.
func relaunch(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
