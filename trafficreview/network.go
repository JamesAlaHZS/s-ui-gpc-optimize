package trafficreview

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
)

func InterfaceAddresses(iface string) (map[string]bool, error) {
	i, e := net.InterfaceByName(iface)
	if e != nil {
		return nil, e
	}
	list, e := i.Addrs()
	if e != nil {
		return nil, e
	}
	out := map[string]bool{}
	for _, a := range list {
		p, e := netip.ParsePrefix(a.String())
		if e == nil {
			out[p.Addr().Unmap().String()] = true
		}
	}
	if len(out) == 0 {
		return nil, errors.New("interface has no usable address")
	}
	return out, nil
}

// Direction uses original AND translated reply tuples, never assumes that the
// original direction is VPS egress. Client-facing encrypted replies count too.
func Direction(r ConntrackRecord, local map[string]bool) (peer, leg string, origIsTX bool, known bool) {
	localIP := func(v string) bool { a, e := netip.ParseAddr(v); return e == nil && local[a.Unmap().String()] }
	if (localIP(r.Original.SourceIP) || localIP(r.Reply.DestinationIP)) && !localIP(r.Original.DestinationIP) && !localIP(r.Reply.SourceIP) {
		return r.Original.DestinationIP, "to_remote", true, true
	}
	if (localIP(r.Original.DestinationIP) || localIP(r.Reply.SourceIP)) && !localIP(r.Original.SourceIP) && !localIP(r.Reply.DestinationIP) {
		return r.Original.SourceIP, "to_client", false, true
	}
	return "", "unknown", false, false
}

func flowKey(epoch string, r ConntrackRecord) string {
	v := fmt.Sprintf("%s|%s|%s|%+v|%+v", epoch, r.Protocol, r.RawID, r.Original, r.Reply)
	h := sha256.Sum256([]byte(v))
	return "network:" + hex.EncodeToString(h[:])
}

// RecordConntrack consumes absolute kernel counters. First snapshots establish
// a baseline (not lifetime traffic), NEW events establish a zero baseline, and
// repeated final/snapshot events share an atomic persisted watermark.
func (s *Store) RecordConntrack(epoch string, records []ConntrackRecord, local map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, r := range records {
		if r.OrigBytes > math.MaxInt64 || r.ReplyBytes > math.MaxInt64 || r.At <= 0 {
			return errors.New("invalid conntrack byte counter")
		}
		if r.Event != "new" && r.Event != "snapshot" && r.Event != "destroy" {
			return errors.New("invalid conntrack event")
		}
		peer, leg, origTX, known := Direction(r, local)
		// NEW notifications normally precede the kernel accounting extension.
		// Their missing counters establish a zero lifetime baseline, not a gap.
		// The global conntrack dump also contains loopback and other-interface
		// flows; absent counters there say nothing about this interface's data.
		if known && r.Event != "new" && (!r.OrigBytesKnown || !r.ReplyBytesKnown) {
			if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM review_alerts WHERE kind='byte_counter_gap' AND at>=?)", r.At, "byte_counter_gap", "A kernel flow lacks byte counters; absent counters are unknown, not zero traffic", s.rules.Version, r.At-60); e != nil {
				return e
			}
		}
		id := flowKey(epoch, r)
		var prevOrig, prevReply, previousAt int64
		var flags int
		e = tx.QueryRow("SELECT up,down,known,seen FROM review_checkpoints WHERE id=?", id).Scan(&prevOrig, &prevReply, &flags, &previousAt)
		fresh := e == sql.ErrNoRows
		if e != nil && !fresh {
			return e
		}
		// A new flow reusing the tuple/ID after its destroy is a new lifetime.
		if r.Event == "new" && flags&8 != 0 {
			if r.At <= previousAt {
				// A delayed NEW must not reopen an already finished lifetime.
				continue
			}
			fresh = true
			prevOrig = 0
			prevReply = 0
			flags = 0
		}
		creditBaseline := fresh && r.Event == "new"
		if r.Event == "new" {
			flags |= 1
		}
		var origDelta, replyDelta int64
		apply := func(current uint64, has bool, previous *int64, bit int) int64 {
			if !has {
				return 0
			}
			n := int64(current)
			base := *previous
			if creditBaseline {
				base = 0
			}
			if fresh && r.Event != "new" || flags&bit == 0 && flags&1 == 0 {
				base = n
			}
			// Snapshots race with final events; taking the maximum avoids a
			// delayed smaller snapshot rewinding the watermark and double-counting.
			if n < *previous {
				n = *previous
			}
			delta := n - base
			if delta < 0 {
				delta = 0
			}
			*previous = n
			flags |= bit
			return delta
		}
		origDelta = apply(r.OrigBytes, r.OrigBytesKnown, &prevOrig, 2)
		replyDelta = apply(r.ReplyBytes, r.ReplyBytesKnown, &prevReply, 4)
		if r.Event == "destroy" {
			flags |= 8
		}
		if known && fresh && r.Event != "new" {
			if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM review_alerts WHERE kind='flow_baseline_gap' AND at>=?)", r.At, "flow_baseline_gap", "Pre-existing or unmatched flow baselined; earlier lifetime bytes excluded", s.rules.Version, r.At-60); e != nil {
				return e
			}
		}
		if known && (r.OrigBytesKnown || r.ReplyBytesKnown) {
			fee, basis := s.rules.ClassifyPeer(peer)
			d := Destination{Layer: "network", Peer: peer, Protocol: r.Protocol, Business: "unknown", Confidence: "unknown", Leg: leg, FeeStatus: fee, FeeBasis: basis}
			if origTX {
				d.TXBytes = origDelta
				d.RXBytes = replyDelta
				d.TXKnown = r.OrigBytesKnown
			} else {
				d.TXBytes = replyDelta
				d.RXBytes = origDelta
				d.TXKnown = r.ReplyBytesKnown
			}
			if e = addDestination(tx, r.At, d, s.rules.Version); e != nil {
				return e
			}
		}
		// A host-wide dump includes ordinary loopback, bridge and other-NIC
		// connections. Excluding those, or ambiguous host-local tuples, is
		// expected filtering rather than evidence of lost interface traffic.
		if _, e = tx.Exec("INSERT INTO review_checkpoints VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET up=excluded.up,down=excluded.down,seen=MAX(seen,excluded.seen),known=excluded.known", id, prevOrig, prevReply, r.At, flags); e != nil {
			return e
		}
	}
	return tx.Commit()
}
