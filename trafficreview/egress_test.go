package trafficreview

import (
	"net/url"
	"testing"
	"time"
)

func TestEgressRankingFiltersBeforeLimitAndKeepsLayersSeparate(t *testing.T) {
	s := testStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Destination{
		{Layer: "proxy", Peer: "proxy", Leg: "to_remote", UpBytes: 999999},
		{Layer: "network", Peer: "large-download", Leg: "to_remote", TXBytes: 3, RXBytes: 99999},
		{Layer: "network", Peer: "website", Leg: "to_remote", TXBytes: 900, RXBytes: 10},
		{Layer: "network", Peer: "client", Leg: "to_client", TXBytes: 8000, RXBytes: 20},
		{Layer: "network", Peer: "unknown", Leg: "unknown", TXBytes: 7},
	} {
		if err := addDestination(tx, 1000, d, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := testRange()
	r.Layer, r.Leg, r.Sort, r.Limit = "network", "to_remote", "egress", 1
	rows, err := s.Destinations(r)
	if err != nil || len(rows) != 1 || rows[0].Peer != "website" {
		t.Fatalf("wrong outbound ranking: %+v, %v", rows, err)
	}
	r.Leg = ""
	rows, err = s.Destinations(r)
	if err != nil || len(rows) != 1 || rows[0].Peer != "client" {
		t.Fatalf("client return traffic missing: %+v, %v", rows, err)
	}
	if err := s.RecordNIC(NICSnapshot{At: 1000, Interface: "eth0", BootID: "a", RX: 100, TX: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordNIC(NICSnapshot{At: 1010, Interface: "eth0", BootID: "a", RX: 10100, TX: 10100}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(r)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TXBytes != 10000 || summary.ToRemoteEgressBytes != 903 || summary.ToClientEgressBytes != 8000 || summary.NetworkEgressBytes != 8910 || summary.ProxyUploadBytes != 999999 {
		t.Fatalf("accounting layers or direction mixed: %+v", summary)
	}
}

func TestEgressQueryValidation(t *testing.T) {
	for _, query := range []string{"layer=network&leg=to_client&sort=egress", "layer=proxy&leg=to_remote&sort=total"} {
		q, _ := url.ParseQuery(query)
		if _, err := ParseRange(q, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"layer=network%27", "leg=unknown", "sort=tx%3BDROP"} {
		q, _ := url.ParseQuery(query)
		if _, err := ParseRange(q, time.Now()); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
}

func TestEmptyBillingWindowRemainsPendingAfterSuccessfulSync(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if err := s.recordSyncRun(now.Unix(), now.Format("200601"), "sync", 1, 1, 1, 0, 1, 0, now.Unix(), "ok", ""); err != nil {
		t.Fatal(err)
	}
	status, err := s.BillingSyncStatus(now)
	if err != nil {
		t.Fatal(err)
	}
	if status.DataPending {
		t.Fatal("fixture should have a recent completed export")
	}
	overview, err := s.BillingOverview(testRange(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.SyncStatus.DataPending {
		t.Fatal("empty selected window misrepresented as imported")
	}
}
