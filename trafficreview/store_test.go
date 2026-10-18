package trafficreview

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "review.sqlite"), DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func testRange() Range { return Range{From: 900, To: 3000, Limit: 500} }

func TestOpenRefusesUnrelatedDatabaseWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE important(value TEXT); INSERT INTO important VALUES('keep-me')"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path, DefaultRules()); err == nil {
		s.Close()
		t.Fatal("accepted the unrelated database")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unrelated database changed on rejected Open")
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM important").Scan(&value); err != nil || value != "keep-me" {
		t.Fatalf("original row changed: %q, %v", value, err)
	}
}

func TestProxyWatermarkRetryAndShortClosedConnection(t *testing.T) {
	s := testStore(t)
	record := ProxyRecord{ID: "short-flow", Peer: "203.0.113.9", Domain: "chatgpt.com", User: "u", At: 1001, Up: 42, Down: 84, Closed: true}
	for i := 0; i < 2; i++ {
		if err := s.RecordProxy("boot-1", []ProxyRecord{record}); err != nil {
			t.Fatal(err)
		}
	}
	record.At, record.Up, record.Down = 1011, 50, 100
	if err := s.RecordProxy("boot-1", []ProxyRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProxy("boot-1", []ProxyRecord{{ID: "bad", At: 1012, Up: -1}}); err == nil {
		t.Fatal("accepted a negative counter")
	}
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UpBytes != 50 || rows[0].DownBytes != 100 {
		t.Fatalf("short flow/retry double counted or lost: %+v", rows)
	}
	if rows[0].Confidence != "inferred" || rows[0].FeeStatus != "unknown" {
		t.Fatalf("classification overstated: %+v", rows[0])
	}
}

func TestNICBaselineNormalDowntimeAndReset(t *testing.T) {
	s := testStore(t)
	samples := []NICSnapshot{
		{At: 1000, Interface: "eth0", BootID: "boot-a", Index: "2", RX: 1000, TX: 2000},
		{At: 1010, Interface: "eth0", BootID: "boot-a", Index: "2", RX: 1020, TX: 2030},
		{At: 1100, Interface: "eth0", BootID: "boot-a", Index: "2", RX: 1070, TX: 2080},
		{At: 1110, Interface: "eth0", BootID: "boot-b", Index: "2", RX: 3, TX: 4},
		{At: 1120, Interface: "eth0", BootID: "boot-b", Index: "2", RX: 8, TX: 10},
	}
	for _, sample := range samples {
		if err := s.RecordNIC(sample); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query("SELECT rx,tx,quality FROM nic_samples ORDER BY at")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := [][3]int64{{0, 0, 1}, {20, 30, 0}, {50, 50, 2}, {0, 0, 4}, {5, 6, 0}}
	for i, expected := range want {
		if !rows.Next() {
			t.Fatalf("missing sample %d", i)
		}
		var got [3]int64
		if err := rows.Scan(&got[0], &got[1], &got[2]); err != nil {
			t.Fatal(err)
		}
		if got != expected {
			t.Fatalf("sample %d: got %v, want %v", i, got, expected)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra sample")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestConntrackAbsoluteCountersSnapshotAndDestroyDeduplicate(t *testing.T) {
	s := testStore(t)
	local := map[string]bool{"192.0.2.10": true}
	r := ConntrackRecord{At: 1000, Event: "new", Protocol: "tcp", RawID: "11", Original: FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.3", SourcePort: 40000, DestinationPort: 443}, Reply: FlowTuple{SourceIP: "198.51.100.3", DestinationIP: "192.0.2.10", SourcePort: 443, DestinationPort: 40000}, OrigBytesKnown: true, ReplyBytesKnown: true, OrigBytes: 10, ReplyBytes: 20}
	write := func() {
		t.Helper()
		if err := s.RecordConntrack("boot-a", []ConntrackRecord{r}, local); err != nil {
			t.Fatal(err)
		}
	}
	write()
	r.At, r.Event, r.OrigBytes, r.ReplyBytes = 1010, "snapshot", 100, 200
	write()
	r.At, r.Event, r.OrigBytes, r.ReplyBytes = 1020, "destroy", 150, 250
	write()
	write()
	r.At, r.Event, r.OrigBytes, r.ReplyBytes = 1015, "snapshot", 120, 220
	write()
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TXBytes != 150 || rows[0].RXBytes != 250 || rows[0].Leg != "to_remote" {
		t.Fatalf("absolute counters double-counted or reversed: %+v", rows)
	}
}

func TestInitialConntrackSnapshotDoesNotBackfillLifetime(t *testing.T) {
	s := testStore(t)
	local := map[string]bool{"192.0.2.10": true}
	r := ConntrackRecord{At: 1000, Event: "snapshot", Protocol: "udp", RawID: "22", Original: FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.4", SourcePort: 50000, DestinationPort: 443}, Reply: FlowTuple{SourceIP: "198.51.100.4", DestinationIP: "192.0.2.10", SourcePort: 443, DestinationPort: 50000}, OrigBytesKnown: true, ReplyBytesKnown: true, OrigBytes: 1000, ReplyBytes: 2000}
	if err := s.RecordConntrack("boot-a", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	r.At, r.Event, r.OrigBytes, r.ReplyBytes = 1010, "destroy", 1200, 2200
	if err := s.RecordConntrack("boot-a", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TXBytes != 200 || rows[0].RXBytes != 200 {
		t.Fatalf("initial lifetime traffic was backfilled: %+v", rows)
	}
}

func TestQueryUsesParametersForUntrustedPeerAndReturnsEmptyLists(t *testing.T) {
	s := testStore(t)
	if got, err := s.Destinations(testRange()); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty destinations: %v, %v", got, err)
	}
	if got, err := s.Alerts(testRange()); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty alerts: %v, %v", got, err)
	}
	if got, err := s.Series(testRange()); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty series: %v, %v", got, err)
	}
	unsafe := "x'); DROP TABLE traffic_minutes; --"
	if err := s.RecordProxy("boot", []ProxyRecord{{ID: "injection", Peer: unsafe, Domain: unsafe, At: 1000, Up: 7}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Peer != unsafe {
		t.Fatalf("untrusted text was not stored literally: %+v", rows)
	}
	if _, err := s.Summary(testRange()); err != nil {
		t.Fatal(err)
	}
}
