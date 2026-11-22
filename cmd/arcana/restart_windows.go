//go:build windows

package main

import (
	"os"
	"os/exec"
)

// relaunch starts the newly installed program in the same console and ends
// this one (Windows cannot replace a running process image).
func relaunch(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
