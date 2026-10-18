package trafficreview

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCollectorSystemdReadinessDatagram(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd Unix datagram test requires Linux")
	}
	// Keep the Unix socket on Linux's filesystem and below its path limit.
	dir, err := os.MkdirTemp("/tmp", "sui-notify-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "notify.sock")
	listener, err := net.ListenPacket("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	if err := notifyCollectorReady(); err != nil {
		t.Fatal(err)
	}
	listener.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	n, _, err := listener.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "READY=1" {
		t.Fatalf("readiness notification: %q %v", buf[:n], err)
	}
	t.Setenv("NOTIFY_SOCKET", "")
	if err := notifyCollectorReady(); err != nil {
		t.Fatal(err)
	}
}
