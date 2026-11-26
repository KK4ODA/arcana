//go:build !unix

package main

import "os/exec"

func ownGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
