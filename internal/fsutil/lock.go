package fsutil

import (
	"errors"
	"os"
)

// ErrLocked is returned when another process holds the library lock.
var ErrLocked = errors.New("library is in use by another Arcana process")

// Lock is an exclusive, advisory, OS-level lock on a file. The operating
// system releases it automatically when the process exits or crashes, so a
// sudden power loss or killed process never leaves a stale lock behind.
type Lock struct {
	f *os.File
}

// TryLock acquires an exclusive lock on path without blocking. It returns
// ErrLocked if another process holds it.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = unlockFile(l.f)
	err := l.f.Close()
	l.f = nil
	return err
}
