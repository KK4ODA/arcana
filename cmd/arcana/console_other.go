//go:build !windows

package main

import "errors"

var errNoDisplay = errors.New("no display")

func pauseIfDoubleClicked() {}
