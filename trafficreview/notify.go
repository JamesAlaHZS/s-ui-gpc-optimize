package trafficreview

import (
	"fmt"
	"net"
	"os"
)

// The panel may run as root while the collector owns SQLite. systemd must wait
// until the collector has opened the database/WAL before starting that panel.
// Outside a Type=notify unit this is a no-op.
func notifyCollectorReady() error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	conn, err := net.Dial("unixgram", address)
	if err != nil {
		return fmt.Errorf("notify collector readiness: %w", err)
	}
	defer conn.Close()
	_, err = conn.Write([]byte("READY=1"))
	return err
}
