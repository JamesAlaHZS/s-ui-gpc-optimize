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
