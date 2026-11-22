// Package sdnotify implements the systemd service notification protocol
// (sd_notify) without cgo or dependencies: readiness, status text and the
// watchdog keep-alive. Outside systemd every call is a no-op.
package sdnotify

import (
	"context"
	"net"
	"os"
	"strconv"
	"time"
)

// Notify sends state (for example "READY=1") to the service manager. It
// reports false when not running under systemd.
func Notify(state string) (bool, error) {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return false, nil
	}
	if path[0] == '@' { // abstract socket namespace
		path = "\x00" + path[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(state)); err != nil {
		return false, err
	}
	return true, nil
}

// WatchdogInterval returns how often to send keep-alives (half the
// configured WatchdogSec), or 0 when no watchdog is configured for this
// process.
func WatchdogInterval() time.Duration {
	usec, err := strconv.ParseInt(os.Getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// Watchdog sends WATCHDOG=1 at the configured interval for as long as
// healthy reports nil. If the service stops responding, keep-alives stop
// and systemd restarts it.
func Watchdog(ctx context.Context, healthy func(context.Context) error) {
	every := WatchdogInterval()
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hctx, cancel := context.WithTimeout(ctx, every)
			err := healthy(hctx)
			cancel()
			if err == nil {
				_, _ = Notify("WATCHDOG=1")
			} else {
				_, _ = Notify("STATUS=Not responding: " + err.Error())
			}
		}
	}
}
