//go:build linux

package sdnotify

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func listen(t *testing.T) (*net.UnixConn, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, path
}

func read(t *testing.T, c *net.UnixConn) string {
	t.Helper()
	buf := make([]byte, 256)
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func TestNotify(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if ok, err := Notify("READY=1"); ok || err != nil {
		t.Fatal("notified without systemd")
	}
	conn, path := listen(t)
	t.Setenv("NOTIFY_SOCKET", path)
	if ok, err := Notify("READY=1\nSTATUS=Serving"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := read(t, conn); got != "READY=1\nSTATUS=Serving" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchdog(t *testing.T) {
	conn, path := listen(t)
	t.Setenv("NOTIFY_SOCKET", path)
	t.Setenv("WATCHDOG_USEC", "")
	if WatchdogInterval() != 0 {
		t.Fatal("interval without watchdog")
	}
	t.Setenv("WATCHDOG_USEC", "200000")
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()+1))
	if WatchdogInterval() != 0 {
		t.Fatal("watchdog meant for another process")
	}
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	if WatchdogInterval() != 100*time.Millisecond {
		t.Fatal(WatchdogInterval())
	}
	var healthy atomic.Bool
	healthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watchdog(ctx, func(context.Context) error {
		if healthy.Load() {
			return nil
		}
		return errors.New("HTTP server stuck")
	})
	if got := read(t, conn); got != "WATCHDOG=1" {
		t.Fatalf("got %q", got)
	}
	healthy.Store(false)
	for i := 0; i < 5; i++ {
		if got := read(t, conn); got == "STATUS=Not responding: HTTP server stuck" {
			return
		}
	}
	t.Fatal("keep-alives continued while unhealthy")
}
