package trafficreview

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEventCommandOmitsUnsupportedFamilyFlag(t *testing.T) {
	want := []string{"-E", "-e", "NEW,DESTROY", "-o", "timestamp,extended,id"}
	if got := eventArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("event arguments = %q, want %q", got, want)
	}
}

func TestSingleEventStreamParsesIPv4AndIPv6(t *testing.T) {
	var records []ConntrackRecord
	c := NewConntrackCollector(func(r ConntrackRecord) { records = append(records, r) }, nil)
	stream := "[NEW] tcp 6 120 src=192.0.2.10 dst=198.51.100.2 sport=1234 dport=443 bytes=10 src=198.51.100.2 dst=192.0.2.10 sport=443 dport=1234 bytes=20 id=1\n" +
		"[DESTROY] udp 17 30 src=2001:db8::10 dst=2001:db8::20 sport=55555 dport=443 bytes=30 src=2001:db8::20 dst=2001:db8::10 sport=443 dport=55555 bytes=40 id=2\n"
	if err := c.scanRecords(strings.NewReader(stream), ""); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Protocol != "tcp" || records[0].Event != "new" || records[0].Original.SourceIP != "192.0.2.10" || records[1].Protocol != "udp" || records[1].Event != "destroy" || records[1].Original.SourceIP != "2001:db8::10" {
		t.Fatalf("single stream lost a family: %+v", records)
	}
}

func TestParseConntrackLine(t *testing.T) {
	tests := []struct {
		name, line, forced           string
		wantEvent, wantProto         string
		wantOrig, wantReply          uint64
		wantAt                       int64
		origKnown, replyKnown        bool
		wantOriginal, wantReplyTuple FlowTuple
	}{
		{
			name:      "destroy with NAT keeps both tuples and final byte counters",
			line:      "[2026-10-08T07:01:02.000000] [DESTROY] tcp 6 0 TIME_WAIT src=10.0.0.4 dst=198.51.100.9 sport=51933 dport=443 packets=3 bytes=333 src=198.51.100.9 dst=203.0.113.10 sport=443 dport=60001 packets=4 bytes=444 [ASSURED] mark=0 id=42",
			wantEvent: "destroy", wantProto: "tcp", wantOrig: 333, wantReply: 444, origKnown: true, replyKnown: true,
			wantAt:         time.Date(2026, 10, 8, 7, 1, 2, 0, time.Local).Unix(),
			wantOriginal:   FlowTuple{"10.0.0.4", "198.51.100.9", 51933, 443},
			wantReplyTuple: FlowTuple{"198.51.100.9", "203.0.113.10", 443, 60001},
		},
		{
			name:      "new IPv6 UDP association has unknown counters",
			line:      "[NEW] udp 17 30 src=2001:db8::10 dst=2001:db8::20 sport=55555 dport=443 [UNREPLIED] src=2001:db8::20 dst=2001:db8::10 sport=443 dport=55555",
			wantEvent: "new", wantProto: "udp",
			wantAt:         123,
			wantOriginal:   FlowTuple{"2001:db8::10", "2001:db8::20", 55555, 443},
			wantReplyTuple: FlowTuple{"2001:db8::20", "2001:db8::10", 443, 55555},
		},
		{
			name:   "snapshot has absolute counters not a delta",
			line:   "tcp 6 431999 ESTABLISHED src=192.0.2.5 dst=198.51.100.7 sport=1234 dport=443 packets=5 bytes=1000 src=198.51.100.7 dst=192.0.2.5 sport=443 dport=1234 packets=7 bytes=2000",
			forced: "snapshot", wantEvent: "snapshot", wantProto: "tcp", wantOrig: 1000, wantReply: 2000, origKnown: true, replyKnown: true,
			wantAt:         123,
			wantOriginal:   FlowTuple{"192.0.2.5", "198.51.100.7", 1234, 443},
			wantReplyTuple: FlowTuple{"198.51.100.7", "192.0.2.5", 443, 1234},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseConntrackLine(tc.line, tc.forced, 123)
			if err != nil {
				t.Fatal(err)
			}
			if got.At != tc.wantAt || got.Event != tc.wantEvent || got.Protocol != tc.wantProto || got.Original != tc.wantOriginal || got.Reply != tc.wantReplyTuple || got.OrigBytes != tc.wantOrig || got.ReplyBytes != tc.wantReply || got.OrigBytesKnown != tc.origKnown || got.ReplyBytesKnown != tc.replyKnown {
				t.Errorf("unexpected record: %+v", got)
			}
		})
	}
}

func TestConntrackTimestampEpoch(t *testing.T) {
	got, ok := conntrackTimestamp("1791442862.123456")
	if !ok || got != 1791442862 {
		t.Fatalf("timestamp = %d, %t", got, ok)
	}
}

func TestParseConntrackLineRejectsIncompleteAndInvalid(t *testing.T) {
	for _, line := range []string{
		"[DESTROY] icmp 1 src=192.0.2.1 dst=192.0.2.2",
		"[DESTROY] tcp 6 src=192.0.2.1 dst=192.0.2.2 sport=123 dport=443",
		"[DESTROY] tcp 6 src=invalid dst=192.0.2.2 sport=123 dport=443 src=192.0.2.2 dst=192.0.2.1 sport=443 dport=123",
		"[DESTROY] tcp 6 src=192.0.2.1 dst=192.0.2.2 sport=99999 dport=443 src=192.0.2.2 dst=192.0.2.1 sport=443 dport=123",
	} {
		if _, err := parseConntrackLine(line, "", 123); err == nil {
			t.Errorf("accepted malformed line: %s", line)
		}
	}
}

func TestNonTCPUDPProtocolsDoNotIncrementParseErrors(t *testing.T) {
	var records []ConntrackRecord
	var health ConntrackHealth
	c := NewConntrackCollector(func(r ConntrackRecord) { records = append(records, r) }, func(h ConntrackHealth) { health = h })
	stream := "[NEW] icmp 1 30 src=192.0.2.1 dst=192.0.2.2 type=8 code=0 id=1\n" +
		"[NEW] ipv6-icmp 58 30 src=2001:db8::1 dst=2001:db8::2 type=128 code=0 id=2\n" +
		"[NEW] udplite 136 30 src=192.0.2.3 dst=192.0.2.4 sport=1234 dport=443\n" +
		"[NEW] esp 50 30 src=192.0.2.5 dst=192.0.2.6 spi=123\n"
	if err := c.scanRecords(strings.NewReader(stream), ""); err != nil {
		t.Fatal(err)
	}
	if health.ParseErrors != 0 || health.Degraded || len(records) != 0 {
		t.Fatalf("non-TCP/UDP records should be ignored: health=%+v records=%+v", health, records)
	}
}

func TestProtocolNameNumberMismatchCountsParseError(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	if err := c.scanRecords(strings.NewReader("[NEW] not-tcp 6 30 src=192.0.2.1 dst=192.0.2.2\n"), ""); err != nil {
		t.Fatal(err)
	}
	if health.ParseErrors != 1 || !health.Degraded {
		t.Fatalf("malformed TCP/UDP protocol label should not be silently skipped: %+v", health)
	}
}

func TestMalformedTCPStillCountsParseError(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	line := "[DESTROY] tcp 6 0 src=192.0.2.1 dst=192.0.2.2 sport=123 dport=443\n"
	if err := c.scanRecords(strings.NewReader(line), ""); err != nil {
		t.Fatal(err)
	}
	if health.ParseErrors != 1 || !health.Degraded || !strings.Contains(health.LastError, "missing original/reply tuple") {
		t.Fatalf("malformed TCP record should remain visible: %+v", health)
	}
}

func TestSuccessfulSnapshotClearsRecoveredHealthAndPreservesGapCounters(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	c.health = ConntrackHealth{Available: true, Degraded: true, EventListeners: 1, EventGaps: 2, ENOBUFS: 2, LastError: "ENOBUFS"}
	c.snapshotSucceeded(0, 2, 2)
	if !health.Available || health.Degraded || health.LastError != "" || health.EventGaps != 2 || health.ENOBUFS != 2 {
		t.Fatalf("recovered health should clear transient error and retain history: %+v", health)
	}
}

func TestValidEventClearsRecoveredHealthAndPreservesGapCounters(t *testing.T) {
	var health ConntrackHealth
	var records []ConntrackRecord
	c := NewConntrackCollector(func(r ConntrackRecord) { records = append(records, r) }, func(h ConntrackHealth) { health = h })
	c.snapshotOK = true
	c.health = ConntrackHealth{Available: true, Degraded: true, EventListeners: 1, EventGaps: 2, ENOBUFS: 2, LastError: "ENOBUFS"}
	line := "[NEW] tcp 6 120 src=192.0.2.10 dst=198.51.100.2 sport=1234 dport=443 bytes=10 src=198.51.100.2 dst=192.0.2.10 sport=443 dport=1234 bytes=20 id=1\n"
	if err := c.scanRecords(strings.NewReader(line), ""); err != nil {
		t.Fatal(err)
	}
	if !health.Available || health.Degraded || health.LastError != "" || health.EventGaps != 2 || health.ENOBUFS != 2 || len(records) != 1 {
		t.Fatalf("valid event should restore current health without erasing historical gaps: health=%+v records=%+v", health, records)
	}
}

func TestSuccessfulSnapshotDoesNotClearCurrentParseErrorOrMissingEventStream(t *testing.T) {
	t.Run("parse error during snapshot", func(t *testing.T) {
		var health ConntrackHealth
		c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
		c.health = ConntrackHealth{Available: true, Degraded: true, EventListeners: 1, ParseErrors: 1, LastError: "conntrack parse: malformed TCP"}
		c.snapshotSucceeded(0, 0, 0)
		if !health.Degraded || health.LastError != "conntrack parse: malformed TCP" {
			t.Fatalf("current parse error was cleared: %+v", health)
		}
	})
	t.Run("event stream still down", func(t *testing.T) {
		var health ConntrackHealth
		c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
		c.health = ConntrackHealth{Degraded: true, LastError: "conntrack event: exited"}
		c.snapshotSucceeded(0, 0, 0)
		if !health.Available || !health.Degraded || health.LastError != "conntrack event: exited" {
			t.Fatalf("snapshot incorrectly hid the event-stream failure: %+v", health)
		}
	})
}

func TestSnapshotWithNewGapCannotPrematurelyRecover(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	c.health = ConntrackHealth{Available: true, Degraded: true, EventListeners: 1, EventGaps: 1, ENOBUFS: 1, LastError: "ENOBUFS"}
	c.snapshotSucceeded(0, 0, 0)
	if !health.Degraded || health.LastError != "ENOBUFS" || c.snapshotOK {
		t.Fatalf("snapshot interrupted by a new gap cleared its failure: %+v", health)
	}
	line := "[NEW] tcp 6 120 src=192.0.2.10 dst=198.51.100.2 sport=1234 dport=443 src=198.51.100.2 dst=192.0.2.10 sport=443 dport=1234\n"
	if err := c.scanRecords(strings.NewReader(line), ""); err != nil {
		t.Fatal(err)
	}
	if !health.Degraded {
		t.Fatalf("event should not hide gap before clean snapshot: %+v", health)
	}
	c.snapshotSucceeded(0, 1, 1)
	if health.Degraded || health.LastError != "" || !c.snapshotOK || health.EventGaps != 1 {
		t.Fatalf("later clean snapshot should recover and preserve history: %+v", health)
	}
}

func TestMalformedEventNeedsCleanSnapshotBeforeRecovery(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	c.snapshotOK = true
	c.health = ConntrackHealth{Available: true, EventListeners: 1}
	malformed := "[DESTROY] tcp 6 0 src=192.0.2.1 dst=192.0.2.2 sport=123 dport=443\n"
	valid := "[NEW] tcp 6 120 src=192.0.2.10 dst=198.51.100.2 sport=1234 dport=443 src=198.51.100.2 dst=192.0.2.10 sport=443 dport=1234\n"
	if err := c.scanRecords(strings.NewReader(malformed+valid), ""); err != nil {
		t.Fatal(err)
	}
	if !health.Degraded || health.ParseErrors != 1 || c.snapshotOK {
		t.Fatalf("good event must not hide preceding parse error: %+v", health)
	}
	c.snapshotSucceeded(1, 0, 0)
	if health.Degraded || health.ParseErrors != 1 || !c.snapshotOK {
		t.Fatalf("clean snapshot should recover while preserving parse-error count: %+v", health)
	}
}

func TestENOBUFSMakesEventGapVisible(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	c.scanStderr(strings.NewReader("conntrack v1: ENOBUFS: No buffer space available\n"), true)
	if health.ENOBUFS != 1 || health.EventGaps != 1 || !health.Degraded {
		t.Fatalf("missing event loss: %+v", health)
	}
	c.scanStderr(strings.NewReader("ENOBUFS\n"), false)
	if health.ENOBUFS != 2 || health.EventGaps != 1 {
		t.Fatalf("snapshot incorrectly counted as event gap: %+v", health)
	}
}

func TestMissingToolDegradesWithoutError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.Run(ctx, time.Second)
	if health.Available || !health.Degraded || health.LastError == "" {
		t.Fatalf("missing-tool state: %+v", health)
	}
}

func TestEventStartFailureRecordsInitialGap(t *testing.T) {
	var health ConntrackHealth
	c := NewConntrackCollector(nil, func(h ConntrackHealth) { health = h })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	c.eventLoop(ctx, filepath.Join(t.TempDir(), "missing-conntrack"))
	if health.Available || !health.Degraded || health.EventGaps != 1 || health.EventRestarts != 0 {
		t.Fatalf("initial event failure should expose a gap, not a restart: %+v", health)
	}
}
