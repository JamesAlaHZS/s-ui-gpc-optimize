package trafficreview

import (
	"path/filepath"
	"testing"
)

func TestNICSameSecondRestartPreservesCounterDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	s, err := Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	n := NICSnapshot{At: 1800000000, Interface: "ens4", BootID: "boot", Index: "2", RX: 100, TX: 200}
	if err := s.RecordNIC(n); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n.RX, n.TX = 120, 250
	if err := s.RecordNIC(n); err != nil {
		t.Fatal(err)
	}
	n.At++
	n.RX, n.TX = 160, 300
	if err := s.RecordNIC(n); err != nil {
		t.Fatal(err)
	}
	var rx, tx int64
	if err := s.db.QueryRow("SELECT sum(rx),sum(tx) FROM nic_samples").Scan(&rx, &tx); err != nil {
		t.Fatal(err)
	}
	if rx != 60 || tx != 100 {
		t.Fatalf("restart delta rx=%d tx=%d; want 60/100", rx, tx)
	}
	if _, err := s.db.Exec("INSERT INTO review_meta VALUES('interval_seconds','1')"); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(Range{From: n.At - 10, To: n.At + 1})
	if err != nil || summary.SamplingSeconds != 1 {
		t.Fatalf("summary interval=%d err=%v", summary.SamplingSeconds, err)
	}
}
