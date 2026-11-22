//go:build !windows

package mdns

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseControl lets Arcana share port 5353 with the system responder.
func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if serr == nil {
			// Not every kernel supports SO_REUSEPORT; SO_REUSEADDR is
			// enough for multicast on Linux.
			_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}
