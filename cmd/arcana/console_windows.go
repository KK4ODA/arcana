//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNoDisplay = errors.New("no display")

var procGetConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// pauseIfDoubleClicked keeps the console open after an error when Arcana
// was started from Explorer (the console then has no other process), so the
// message can be read.
func pauseIfDoubleClicked() {
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if n == 1 {
		fmt.Fprint(os.Stderr, "\nPress Enter to close this window.")
		var b [1]byte
		os.Stdin.Read(b[:])
	}
}
