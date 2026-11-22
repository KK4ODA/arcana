//go:build !linux && !windows && !darwin

package storage

import "errors"

var errUnsupported = errors.New("volume detection is not implemented on this platform")

// Stat is not implemented on this platform.
func Stat(path string) (Volume, error) { return Volume{}, errUnsupported }

// ListVolumes is not implemented on this platform.
func ListVolumes() ([]Volume, error) { return nil, errUnsupported }
