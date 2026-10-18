package core

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

func TestDrainReviewTCPShortSessionAndOriginalStats(t *testing.T) {
	tracker := NewSessionTracker()
	client, tracked := trackConn(t, tracker, "in", "alice")
	defer client.Close()

	clientWrite := make(chan error, 1)
	go func() { _, err := client.Write([]byte("hello")); clientWrite <- err }()
	if _, err := io.ReadFull(tracked, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	if err := <-clientWrite; err != nil {
		t.Fatal(err)
	}
	trackedWrite := make(chan error, 1)
	go func() { _, err := tracked.Write([]byte("world!")); trackedWrite <- err }()
	if _, err := io.ReadFull(client, make([]byte, 6)); err != nil {
		t.Fatal(err)
	}
	// net.Pipe can hand the bytes to Read before the tracked Write returns;
	// wait for its counter update before observing a deterministic snapshot.
	if err := <-trackedWrite; err != nil {
		t.Fatal(err)
	}

	live, dropped := tracker.DrainReview()
	if dropped != 0 || len(live) != 1 || live[0].Closed || live[0].Upload != 5 || live[0].Download != 6 {
		t.Fatalf("live review snapshot: %+v, dropped=%d", live, dropped)
	}
	if err := tracked.Close(); err != nil {
		t.Fatal(err)
	}
	_ = tracked.Close() // The same wrapper must enqueue its final counters only once.
	closed, dropped := tracker.DrainReview()
	if dropped != 0 || len(closed) != 1 || !closed[0].Closed || closed[0].Upload != 5 || closed[0].Download != 6 || closed[0].ID != live[0].ID {
		t.Fatalf("closed review snapshot: %+v, dropped=%d", closed, dropped)
	}
	if again, _ := tracker.DrainReview(); len(again) != 0 {
		t.Fatalf("duplicate closed snapshot: %+v", again)
	}
	var up, down int64
	for _, stat := range *tracker.GetStats() {
		if stat.Resource == "user" && stat.Tag == "alice" {
			if stat.Direction {
				up += stat.Traffic
			} else {
				down += stat.Traffic
			}
		}
	}
	if up != 5 || down != 6 {
		t.Fatalf("review drained original stats: up=%d down=%d", up, down)
	}
}

type reviewPacketConn struct{}

func (*reviewPacketConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	_, _ = b.Write([]byte("udp-in"))
	return M.ParseSocksaddr("198.51.100.1:443"), nil
}
func (*reviewPacketConn) WritePacket(_ *buf.Buffer, _ M.Socksaddr) error { return nil }
func (*reviewPacketConn) Close() error                                   { return nil }
func (*reviewPacketConn) LocalAddr() net.Addr                            { return &net.UDPAddr{} }
func (*reviewPacketConn) SetDeadline(time.Time) error                    { return nil }
func (*reviewPacketConn) SetReadDeadline(time.Time) error                { return nil }
func (*reviewPacketConn) SetWriteDeadline(time.Time) error               { return nil }

func TestDrainReviewUDPShortSession(t *testing.T) {
	tracker := NewSessionTracker()
	metadata := adapter.InboundContext{Inbound: "in", User: "alice", Source: M.ParseSocksaddr("192.0.2.1:2345"), Destination: M.ParseSocksaddr("198.51.100.1:443")}
	tracked := tracker.RoutedPacketConnection(context.Background(), &reviewPacketConn{}, metadata, nil, &testOutbound{tag: "direct"})
	readBuffer := buf.NewPacket()
	defer readBuffer.Release()
	if _, err := tracked.ReadPacket(readBuffer); err != nil {
		t.Fatal(err)
	}
	if err := tracked.WritePacket(buf.As([]byte("udp-out")), metadata.Destination); err != nil {
		t.Fatal(err)
	}
	if err := tracked.Close(); err != nil {
		t.Fatal(err)
	}
	snapshots, dropped := tracker.DrainReview()
	if dropped != 0 || len(snapshots) != 1 || !snapshots[0].Closed || snapshots[0].Network != "udp" || snapshots[0].Upload != 6 || snapshots[0].Download != 7 {
		t.Fatalf("UDP short session: %+v, dropped=%d", snapshots, dropped)
	}
}

func TestDrainReviewBoundedClosedQueue(t *testing.T) {
	tracker := NewSessionTracker()
	metadata := adapter.InboundContext{Inbound: "in", User: "alice"}
	for i := 0; i < closedReviewLimit+3; i++ {
		session, _, _ := tracker.newSession(metadata, nil, nil, "tcp")
		tracker.leave(session)
	}
	snapshots, dropped := tracker.DrainReview()
	if len(snapshots) != closedReviewLimit || dropped != 3 {
		t.Fatalf("queue: got %d snapshots, %d dropped", len(snapshots), dropped)
	}
	if again, dropped := tracker.DrainReview(); len(again) != 0 || dropped != 0 {
		t.Fatalf("queue did not drain: %d snapshots, %d dropped", len(again), dropped)
	}
}

func TestDrainReviewConcurrentCloseAndDrain(t *testing.T) {
	tracker := NewSessionTracker()
	metadata := adapter.InboundContext{Inbound: "in", User: "alice"}
	const count = 200
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, _, _ := tracker.newSession(metadata, nil, nil, "tcp")
			session.Upload.Add(1)
			tracker.leave(session)
		}()
	}
	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		for _, snapshot := range func() []ReviewSnapshot { s, _ := tracker.DrainReview(); return s }() {
			if snapshot.Closed {
				if seen[snapshot.ID] {
					t.Errorf("duplicate closure %s", snapshot.ID)
				}
				seen[snapshot.ID] = true
			}
		}
	}
	wg.Wait()
	final, dropped := tracker.DrainReview()
	if dropped != 0 {
		t.Fatalf("unexpected overflow: %d", dropped)
	}
	for _, snapshot := range final {
		if !snapshot.Closed || seen[snapshot.ID] {
			t.Fatalf("bad final snapshot: %+v", snapshot)
		}
		seen[snapshot.ID] = true
	}
	if len(seen) != count {
		t.Fatalf("saw %d closures, want %d", len(seen), count)
	}
}
