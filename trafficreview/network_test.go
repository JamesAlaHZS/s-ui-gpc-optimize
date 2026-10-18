package trafficreview

import "testing"

func TestDirectionUsesOriginalAndReplyTuples(t *testing.T) {
	cases := []struct {
		name                string
		original, reply     FlowTuple
		local               map[string]bool
		peer, leg           string
		originalIsTX, known bool
	}{
		{"IPv4 direct to remote", FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.4"}, FlowTuple{SourceIP: "198.51.100.4", DestinationIP: "192.0.2.10"}, map[string]bool{"192.0.2.10": true}, "198.51.100.4", "to_remote", true, true},
		{"remote initiated SSH reply is local TX", FlowTuple{SourceIP: "198.51.100.5", DestinationIP: "192.0.2.10"}, FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.5"}, map[string]bool{"192.0.2.10": true}, "198.51.100.5", "to_client", false, true},
		{"NAT local address only in reply", FlowTuple{SourceIP: "10.0.0.4", DestinationIP: "198.51.100.6"}, FlowTuple{SourceIP: "198.51.100.6", DestinationIP: "192.0.2.10"}, map[string]bool{"192.0.2.10": true}, "198.51.100.6", "to_remote", true, true},
		{"IPv6 UDP to remote", FlowTuple{SourceIP: "2001:db8::10", DestinationIP: "2001:db8::20"}, FlowTuple{SourceIP: "2001:db8::20", DestinationIP: "2001:db8::10"}, map[string]bool{"2001:db8::10": true}, "2001:db8::20", "to_remote", true, true},
		{"ambiguous both local", FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "192.0.2.11"}, FlowTuple{SourceIP: "192.0.2.11", DestinationIP: "192.0.2.10"}, map[string]bool{"192.0.2.10": true, "192.0.2.11": true}, "", "unknown", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPeer, gotLeg, gotTX, gotKnown := Direction(ConntrackRecord{Original: tc.original, Reply: tc.reply}, tc.local)
			if gotPeer != tc.peer || gotLeg != tc.leg || gotTX != tc.originalIsTX || gotKnown != tc.known {
				t.Fatalf("direction = %q %q %t %t, want %q %q %t %t", gotPeer, gotLeg, gotTX, gotKnown, tc.peer, tc.leg, tc.originalIsTX, tc.known)
			}
		})
	}
}

func TestRemoteInitiatedUDPCountsReplyAsHostEgress(t *testing.T) {
	s := testStore(t)
	r := ConntrackRecord{At: 1000, Event: "new", Protocol: "udp", RawID: "ssh-like", Original: FlowTuple{SourceIP: "2001:db8::20", DestinationIP: "2001:db8::10", SourcePort: 12345, DestinationPort: 22}, Reply: FlowTuple{SourceIP: "2001:db8::10", DestinationIP: "2001:db8::20", SourcePort: 22, DestinationPort: 12345}, OrigBytesKnown: true, ReplyBytesKnown: true, OrigBytes: 30, ReplyBytes: 70}
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, map[string]bool{"2001:db8::10": true}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Leg != "to_client" || rows[0].TXBytes != 70 || rows[0].RXBytes != 30 || rows[0].FeeStatus != "unknown" {
		t.Fatalf("remote-initiated counters mislabeled: %+v", rows)
	}
}

func TestNewConntrackWithoutCountersIsNotAnAccountingGap(t *testing.T) {
	s := testStore(t)
	r := ConntrackRecord{At: 1000, Event: "new", Protocol: "tcp", RawID: "new-before-accounting", Original: FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.7", SourcePort: 40000, DestinationPort: 443}, Reply: FlowTuple{SourceIP: "198.51.100.7", DestinationIP: "192.0.2.10", SourcePort: 443, DestinationPort: 40000}}
	local := map[string]bool{"192.0.2.10": true}
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	before, err := s.Summary(testRange())
	if err != nil || before.NetworkSampleCount != 0 {
		t.Fatalf("NEW without counters must remain unobserved: %+v, %v", before, err)
	}
	r.At, r.Event = 1010, "snapshot"
	r.OrigBytesKnown, r.ReplyBytesKnown = true, true
	r.OrigBytes, r.ReplyBytes = 70, 300
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil || len(rows) != 1 || rows[0].TXBytes != 70 || rows[0].RXBytes != 300 {
		t.Fatalf("NEW must preserve a zero baseline for later counters: %+v, %v", rows, err)
	}
	var gaps int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM review_alerts WHERE kind='byte_counter_gap'").Scan(&gaps); err != nil || gaps != 0 {
		t.Fatalf("normal NEW generated accounting gaps: %d, %v", gaps, err)
	}
}

func TestNonMonitoredConntrackFlowsDoNotReportCollectionGaps(t *testing.T) {
	s := testStore(t)
	local := map[string]bool{"192.0.2.10": true}
	for i, pair := range [][2]string{{"127.0.0.1", "127.0.0.53"}, {"::1", "::1"}, {"172.17.0.2", "172.17.0.3"}} {
		r := ConntrackRecord{At: int64(1000 + i*120), Event: "snapshot", Protocol: "udp", Original: FlowTuple{SourceIP: pair[0], DestinationIP: pair[1], SourcePort: 40000, DestinationPort: 53}, Reply: FlowTuple{SourceIP: pair[1], DestinationIP: pair[0], SourcePort: 53, DestinationPort: 40000}}
		if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
			t.Fatal(err)
		}
		r.At, r.Event = r.At+10, "destroy"
		r.OrigBytesKnown, r.ReplyBytesKnown = true, true
		r.OrigBytes, r.ReplyBytes = 100, 900
		if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM review_alerts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("off-interface flows reported gaps: %d, %v", count, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM traffic_minutes").Scan(&count); err != nil || count != 0 {
		t.Fatalf("off-interface flows attributed to NIC: %d, %v", count, err)
	}
}

func TestActualMissingCountersRetainMeasuredDirectionAndReportGap(t *testing.T) {
	s := testStore(t)
	r := ConntrackRecord{At: 1000, Event: "new", Protocol: "tcp", Original: FlowTuple{SourceIP: "198.51.100.7", DestinationIP: "192.0.2.10", SourcePort: 40000, DestinationPort: 443}, Reply: FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.7", SourcePort: 443, DestinationPort: 40000}}
	local := map[string]bool{"192.0.2.10": true}
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	r.At, r.Event = 1010, "snapshot"
	r.ReplyBytesKnown, r.ReplyBytes = true, 600
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil || len(rows) != 1 || rows[0].TXBytes != 600 || rows[0].Leg != "to_client" {
		t.Fatalf("known reply egress must survive missing original counter: %+v, %v", rows, err)
	}
	var gaps int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM review_alerts WHERE kind='byte_counter_gap'").Scan(&gaps); err != nil || gaps != 1 {
		t.Fatalf("a real missing direction must remain visible: %d, %v", gaps, err)
	}
}

func TestReceiveOnlyCounterDoesNotMakeUnknownEgressAnObservedZero(t *testing.T) {
	s := testStore(t)
	r := ConntrackRecord{At: 1000, Event: "new", Protocol: "tcp", Original: FlowTuple{SourceIP: "198.51.100.7", DestinationIP: "192.0.2.10", SourcePort: 40000, DestinationPort: 443}, Reply: FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.7", SourcePort: 443, DestinationPort: 40000}}
	local := map[string]bool{"192.0.2.10": true}
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	r.At, r.Event = 1010, "snapshot"
	r.OrigBytesKnown, r.OrigBytes = true, 600
	if err := s.RecordConntrack("boot", []ConntrackRecord{r}, local); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(testRange())
	if err != nil || summary.NetworkIngressBytes != 600 || summary.NetworkSampleCount != 0 {
		t.Fatalf("RX-only sample invented known zero TX: %+v, %v", summary, err)
	}
}
