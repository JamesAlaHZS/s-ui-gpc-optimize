package trafficreview

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Fast snapshots can exit before the stderr reader has drained its pipe.
// cmd.Wait must not close the pipe underneath that reader and mark a healthy
// collector degraded. Exercise real child processes and both output pipes.
func TestSnapshotDrainsStderrBeforeWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux conntrack subprocess contract")
	}
	cli := filepath.Join(t.TempDir(), "conntrack")
	script := "#!/bin/sh\nprintf 'tcp 6 100 src=192.0.2.10 dst=198.51.100.2 sport=1234 dport=443 bytes=10 src=198.51.100.2 dst=192.0.2.10 sport=443 dport=1234 bytes=20 id=1\\n'\ni=0; while [ $i -lt 1000 ]; do printf '1 flow entries have been shown.\\n' >&2; i=$((i+1)); done\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bad ConntrackHealth
	count := 0
	c := NewConntrackCollector(func(ConntrackRecord) { count++ }, func(h ConntrackHealth) {
		mu.Lock()
		defer mu.Unlock()
		if h.Degraded {
			bad = h
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		c.snapshot(ctx, cli)
	}
	mu.Lock()
	defer mu.Unlock()
	if bad.Degraded || count != 40 {
		t.Fatalf("health=%+v records=%d; want healthy and 40", bad, count)
	}
}
