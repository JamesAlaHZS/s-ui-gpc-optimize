package trafficreview

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func addHistoryDestination(t *testing.T, s *Store, at int64, d Destination) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = addDestination(tx, at, d, "history-test"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryAccountingIdentitySignedDifferenceAndAbsentMeasurements(t *testing.T) {
	s := testStore(t)
	const at = int64(1800000000 / 3600 * 3600)
	for _, n := range []NICSnapshot{
		{At: at + 10, Interface: "eth0", BootID: "a", RX: 100, TX: 100},
		{At: at + 20, Interface: "eth0", BootID: "a", RX: 120, TX: 150},
	} {
		if err := s.RecordNIC(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []Destination{
		{Layer: "network", Peer: "remote", Leg: "to_remote", TXBytes: 20, RXBytes: 100},
		{Layer: "network", Peer: "client", Leg: "to_client", TXBytes: 40, RXBytes: 10},
		{Layer: "network", Peer: "legacy", Leg: "unknown", TXBytes: 10},
	} {
		addHistoryDestination(t, s, at+30, d)
	}
	r := Range{From: at, To: at + 3600, Limit: 500}
	v, err := s.Summary(r)
	if err != nil {
		t.Fatal(err)
	}
	if v.NetworkEgressBytes != 70 || v.NetworkEgressBytes != v.ToRemoteEgressBytes+v.ToClientEgressBytes+v.UnknownDirectionEgressBytes || v.EgressDifferenceBytes != -20 || v.NetworkSampleCount != 3 || v.ProxySampleCount != 0 || v.FirstSampleAt != at+10 {
		t.Fatalf("inconsistent accounting: %+v", v)
	}
	// A real, observed zero payload is different from no proxy records.
	if err := s.RecordProxy("epoch", []ProxyRecord{{ID: "zero", At: at + 40}}); err != nil {
		t.Fatal(err)
	}
	v, err = s.Summary(r)
	if err != nil || v.ProxySampleCount == 0 || v.ProxyUploadBytes != 0 || v.ProxyDownloadBytes != 0 {
		t.Fatalf("observed zero payload lost: %+v, %v", v, err)
	}
	// The same contract holds on minute fragments, not only whole hours.
	r.To = at + 60
	v, err = s.Summary(r)
	if err != nil || v.ProxySampleCount == 0 || v.NetworkSampleCount != 3 || v.NetworkEgressBytes != 70 {
		t.Fatalf("minute boundary accounting differs: %+v, %v", v, err)
	}
}

func TestHourlyTotalsSurviveDetailRetentionAndStayWithinThreeYears(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Hour)
	at := now.AddDate(0, 0, -200).Unix()
	for _, n := range []NICSnapshot{
		{At: at + 10, Interface: "eth0", BootID: "a", RX: 100, TX: 200},
		{At: at + 20, Interface: "eth0", BootID: "a", RX: 400, TX: 700},
	} {
		if err := s.RecordNIC(n); err != nil {
			t.Fatal(err)
		}
	}
	addHistoryDestination(t, s, at+20, Destination{Layer: "network", Leg: "to_remote", TXBytes: 150, RXBytes: 200})
	if err := s.RecordProxy("e", []ProxyRecord{{ID: "old", At: at + 20, Up: 100, Down: 200}}); err != nil {
		t.Fatal(err)
	}
	tooOld := now.AddDate(-3, 0, -2).Unix()
	addHistoryDestination(t, s, tooOld, Destination{Layer: "network", Leg: "to_client", TXBytes: 999})
	if err := s.Retain(now.Unix()); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, table := range []string{"traffic_minutes", "nic_minutes", "nic_samples"} {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("expired %s retained: %d, %v", table, n, err)
		}
	}
	r := Range{From: at, To: at + 3600, Limit: 500}
	v, err := s.Summary(r)
	if err != nil || v.TXBytes != 500 || v.RXBytes != 300 || v.NetworkEgressBytes != 150 || v.ProxyUploadBytes != 100 || v.ProxyDownloadBytes != 200 || v.NetworkSampleCount == 0 || v.ProxySampleCount == 0 || !v.DestinationDetailsPartial {
		t.Fatalf("retention erased totals: %+v, %v", v, err)
	}
	points, err := s.Series(r)
	if err != nil || len(points) != 1 || points[0].TXBytes != 500 {
		t.Fatalf("retention erased series: %+v, %v", points, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM traffic_hours WHERE at=?", tooOld/3600*3600).Scan(&n); err != nil || n != 0 {
		t.Fatalf("history older than three years retained: %d, %v", n, err)
	}
	// An old partial hour has no surviving minute detail. Exclude the outer
	// part rather than pretending all 500 bytes happened inside the range.
	r.From, r.To = at+60, at+1800
	v, err = s.Summary(r)
	if err != nil || !v.Partial || v.TXBytes != 0 || v.NetworkEgressBytes != 0 {
		t.Fatalf("expired boundary silently overcounted: %+v, %v", v, err)
	}
}

func TestMinuteAndHourlyFragmentsNeverDoubleCount(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour).Unix()
	for _, offset := range []int64{0, 1800, 3600, 5400, 7200, 9000} {
		addHistoryDestination(t, s, at+offset, Destination{Layer: "network", Leg: "to_remote", TXBytes: 10})
	}
	for _, tc := range []struct{ from, to, want int64 }{
		{0, 10800, 60}, {1800, 9000, 40}, {1800, 3600, 10}, {3600, 7200, 20}, {0, 1800, 10},
	} {
		v, err := s.Summary(Range{From: at + tc.from, To: at + tc.to, Limit: 500})
		if err != nil || v.NetworkEgressBytes != tc.want {
			t.Fatalf("fragment %d..%d: got %+v, %v", tc.from, tc.to, v, err)
		}
	}
}

func TestLegacyBackfillRunsOnceAndUsesOnlySurvivingMinutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	const at = int64(1800000000 / 3600 * 3600)
	// Reconstruct the pre-migration shape without manufacturing old totals.
	if _, err = s.db.Exec("DELETE FROM review_meta WHERE key='hourly_history_v1'; INSERT INTO nic_minutes VALUES(?,11,22,60,0)", at); err != nil {
		t.Fatal(err)
	}
	addHistoryDestination(t, s, at, Destination{Layer: "network", Leg: "to_remote", TXBytes: 7})
	addHistoryDestination(t, s, at, Destination{Layer: "network", Leg: "to_client", Peer: "rx-only", RXBytes: 500})
	if _, err = s.db.Exec("DELETE FROM traffic_hours; ALTER TABLE traffic_minutes DROP COLUMN sample_count"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = Open(path, DefaultRules())
		if err != nil {
			t.Fatal(err)
		}
		v, e := s.Summary(Range{From: at, To: at + 3600, Limit: 500})
		if e != nil || v.TXBytes != 22 || v.NetworkEgressBytes != 7 || v.NetworkSampleCount != 1 || v.NetworkIngressBytes != 500 {
			t.Fatalf("migration %d duplicated/lost data: %+v, %v", i, v, e)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentLegacyMigrationIsAtomicAndDoesNotDoubleCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-legacy.db")
	s, err := Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	addHistoryDestination(t, s, 1800000000, Destination{Layer: "network", Leg: "to_remote", TXBytes: 17})
	if _, err = s.db.Exec("DELETE FROM review_meta WHERE key='hourly_history_v1'; DELETE FROM traffic_hours"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			opened, e := Open(path, DefaultRules())
			if e == nil {
				var n int64
				e = opened.db.QueryRow("SELECT SUM(tx) FROM traffic_hours").Scan(&n)
				if e == nil && n != 17 {
					t.Errorf("concurrent migration total=%d, want 17", n)
				}
				if closeErr := opened.Close(); e == nil {
					e = closeErr
				}
			}
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetainedBoundaryMarksPartialWithoutDiscardingWholeHourTotals(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC().Add(-time.Hour * 48).Truncate(time.Hour).Unix()
	addHistoryDestination(t, s, at+600, Destination{Layer: "network", Leg: "to_remote", TXBytes: 30})
	// Model destination cardinality cleanup in the past; the alert happens
	// later than the queried hour, so a range-limited alert lookup cannot help.
	if _, err := s.db.Exec("DELETE FROM traffic_minutes; INSERT INTO review_meta VALUES('destination_retained_from',?)", at+1800); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Range{
		{From: at + 60, To: at + 3600},
		{From: at, To: at + 2400},
	} {
		v, err := s.Summary(r)
		if err != nil || !v.Partial || !v.DestinationDetailsPartial || v.NetworkEgressBytes != 0 {
			t.Fatalf("cardinality-trimmed fragment claimed complete: %+v, %v", v, err)
		}
	}
	v, err := s.Summary(Range{From: at, To: at + 3600})
	if err != nil || v.NetworkEgressBytes != 30 || !v.DestinationDetailsPartial {
		t.Fatalf("whole-hour durable total lost: %+v, %v", v, err)
	}
}

func TestSeriesUnalignedWindowKeepsInteriorPointsVisibleAndBreaksGaps(t *testing.T) {
	s := testStore(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Hour).Unix()
	if _, err := s.db.Exec("INSERT INTO nic_samples VALUES(?, 'eth0',1,2,10,0),(?, 'eth0',3,4,10,0),(?, 'eth0',5,6,10,0)", at+10, at+20, at+100); err != nil {
		t.Fatal(err)
	}
	points, err := s.Series(Range{From: at + 1, To: at + 111})
	if err != nil || len(points) != 4 || points[0].Quality != "ok" || points[1].Quality != "ok" || points[2].Quality != "partial" || points[3].Quality != "ok" {
		t.Fatalf("interior series was hidden or gap connected: %+v, %v", points, err)
	}
}

func TestThreeYearSeriesKeepsNewestBucketAndEveryByte(t *testing.T) {
	s := testStore(t)
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(3, 0, 0)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO nic_hours VALUES(?,1,2,3600,0,?,?,360)")
	if err != nil {
		t.Fatal(err)
	}
	var hours int64
	for at := start; at.Before(end); at = at.Add(time.Hour) {
		if _, err = stmt.Exec(at.Unix(), at.Unix(), at.Unix()+3590); err != nil {
			t.Fatal(err)
		}
		hours++
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	points, err := s.Series(Range{From: start.Unix(), To: end.Unix()})
	if err != nil || len(points) > 5000 || len(points) == 0 {
		t.Fatalf("series point count %d, %v", len(points), err)
	}
	var total int64
	for _, p := range points {
		total += p.TXBytes
	}
	if total != hours*2 || points[len(points)-1].Time < end.Add(-24*time.Hour).Unix() {
		t.Fatalf("series truncated late history: %d/%d bytes, last=%d", total, hours*2, points[len(points)-1].Time)
	}
}
