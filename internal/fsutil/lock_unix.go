//go:build !windows

package fsutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrLocked
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) {
		// Some FUSE file systems do not implement flock. Running without a
		// lock is preferable to refusing to start.
		return nil
	}
	return err
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
