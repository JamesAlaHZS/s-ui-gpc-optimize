package trafficreview

import "testing"

func orderingFlow(event string, at int64, orig, reply uint64) ConntrackRecord {
	return ConntrackRecord{
		At: at, Event: event, Protocol: "tcp", RawID: "fixed-id",
		Original:  FlowTuple{SourceIP: "192.0.2.10", DestinationIP: "198.51.100.20", SourcePort: 50000, DestinationPort: 443},
		Reply:     FlowTuple{SourceIP: "198.51.100.20", DestinationIP: "192.0.2.10", SourcePort: 443, DestinationPort: 50000},
		OrigBytes: orig, ReplyBytes: reply, OrigBytesKnown: true, ReplyBytesKnown: true,
	}
}

func TestDelayedNewCannotCreditPriorSnapshotBaseline(t *testing.T) {
	s := testStore(t)
	local := map[string]bool{"192.0.2.10": true}
	// Snapshot reaches the DB before a buffered, earlier NEW event.
	for _, r := range []ConntrackRecord{
		orderingFlow("snapshot", 1100, 100, 200),
		orderingFlow("new", 1000, 0, 0),
		orderingFlow("destroy", 1200, 130, 250),
	} {
		if err := s.RecordConntrack("epoch", []ConntrackRecord{r}, local); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Destinations(testRange())
	if err != nil || len(rows) != 1 || rows[0].TXBytes != 30 || rows[0].RXBytes != 50 {
		t.Fatalf("delayed NEW recredited historical bytes: %+v, err=%v", rows, err)
	}
}

func TestClosedFlowIgnoresDelayedNewButAllowsLaterNewLifetime(t *testing.T) {
	s := testStore(t)
	local := map[string]bool{"192.0.2.10": true}
	for _, r := range []ConntrackRecord{
		orderingFlow("new", 1000, 0, 0),
		orderingFlow("destroy", 1100, 100, 200),
		orderingFlow("new", 1000, 0, 0),  // delayed; must not reopen closed flow
		orderingFlow("new", 1200, 5, 10), // genuine later lifetime, same tuple and id
		orderingFlow("destroy", 1300, 20, 30),
	} {
		if err := s.RecordConntrack("epoch", []ConntrackRecord{r}, local); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Destinations(testRange())
	if err != nil || len(rows) != 1 || rows[0].TXBytes != 120 || rows[0].RXBytes != 230 {
		t.Fatalf("flow lifetime ordering/counts: %+v, err=%v", rows, err)
	}
}
