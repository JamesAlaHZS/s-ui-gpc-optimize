package trafficreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type NICSnapshot struct {
	At                       int64
	Interface, BootID, Index string
	RX, TX                   uint64
}

func ReadNIC(sys fs.FS, proc fs.FS, iface string, at int64) (NICSnapshot, error) {
	n := NICSnapshot{At: at, Interface: iface}
	if iface == "" || iface == "lo" || strings.ContainsAny(iface, "/\\.") {
		return n, errors.New("explicit non-loopback interface required")
	}
	read := func(name string) (string, error) {
		b, e := fs.ReadFile(sys, path.Join("class/net", iface, name))
		return strings.TrimSpace(string(b)), e
	}
	rx, e := read("statistics/rx_bytes")
	if e != nil {
		return n, e
	}
	tx, e := read("statistics/tx_bytes")
	if e != nil {
		return n, e
	}
	n.RX, e = strconv.ParseUint(rx, 10, 64)
	if e != nil {
		return n, e
	}
	n.TX, e = strconv.ParseUint(tx, 10, 64)
	if e != nil {
		return n, e
	}
	n.Index, e = read("ifindex")
	if e != nil {
		return n, e
	}
	b, e := fs.ReadFile(proc, "sys/kernel/random/boot_id")
	if e != nil {
		return n, e
	}
	n.BootID = strings.TrimSpace(string(b))
	if n.BootID == "" || n.RX > math.MaxInt64 || n.TX > math.MaxInt64 {
		return n, errors.New("invalid boot identity or interface counters")
	}
	return n, nil
}

func (s *Store) RecordNIC(n NICSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var previous NICSnapshot
	old := meta(tx, "last_nic")
	if old != "" {
		if e = json.Unmarshal([]byte(old), &previous); e != nil {
			return fmt.Errorf("invalid persisted interface checkpoint: %w", e)
		}
	}
	if n.At <= 0 || n.RX > math.MaxInt64 || n.TX > math.MaxInt64 {
		return errors.New("invalid interface sample")
	}
	var rx, txBytes, elapsed int64
	quality := 0
	kind := ""
	message := ""
	switch {
	case old == "":
		quality = 1
		kind = "baseline_gap"
		message = "Interface collection begins here; earlier bytes are not backfilled"
	case n.At == previous.At:
		// A fast stop/start can sample twice inside one Unix second. Keep
		// the earlier checkpoint so the next tick includes intervening bytes.
		return nil
	case n.At < previous.At:
		return errors.New("non-increasing sample time")
	case previous.BootID != n.BootID || previous.Index != n.Index || previous.Interface != n.Interface || n.RX < previous.RX || n.TX < previous.TX:
		quality = 4
		kind = "counter_reset_gap"
		message = "Boot/interface/counter changed; new baseline, intervening bytes unknown"
	default:
		rx = int64(n.RX - previous.RX)
		txBytes = int64(n.TX - previous.TX)
		elapsed = n.At - previous.At
		if elapsed > 30 {
			quality = 2
			kind = "collector_gap"
			message = "Interface totals recovered across collector downtime; exact timing and per-flow attribution unavailable"
		}
	}
	if _, e = tx.Exec("INSERT INTO nic_samples VALUES(?,?,?,?,?,?)", n.At, n.Interface, rx, txBytes, elapsed, quality); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO nic_minutes VALUES(?,?,?,?,?) ON CONFLICT(at) DO UPDATE SET rx=rx+excluded.rx,tx=tx+excluded.tx,elapsed=elapsed+excluded.elapsed,quality=quality|excluded.quality`, n.At/60*60, rx, txBytes, elapsed, quality); e != nil {
		return e
	}
	if kind != "" {
		if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", n.At, kind, message, s.rules.Version); e != nil {
			return e
		}
	}
	b, _ := json.Marshal(n)
	if e = setMeta(tx, "last_nic", string(b)); e != nil {
		return e
	}
	if e = setMeta(tx, "interface", n.Interface); e != nil {
		return e
	}
	if txBytes > 256*1024*1024 && elapsed > 0 && float64(txBytes)/float64(elapsed) > 4*1024*1024 {
		if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", n.At, "traffic_spike", "Public interface TX exceeded 4 MiB/s and 256 MiB in a sample; no automatic blocking", s.rules.Version); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) SetConntrackHealth(h ConntrackHealth) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := json.Marshal(h)
	if e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var old ConntrackHealth
	previous := meta(tx, "conntrack_health")
	_ = json.Unmarshal([]byte(previous), &old)
	if h.EventGaps > old.EventGaps || h.ENOBUFS > old.ENOBUFS || h.ParseErrors > old.ParseErrors || h.Degraded && (!old.Degraded || previous == "") {
		message := fmt.Sprintf("Conntrack coverage incomplete: eventGaps=%d enobufs=%d parseErrors=%d; %s", h.EventGaps, h.ENOBUFS, h.ParseErrors, clip(h.LastError, 400))
		if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", time.Now().Unix(), "conntrack_event_gap", message, s.rules.Version); e != nil {
			return e
		}
	}
	if e = setMeta(tx, "conntrack_health", string(b)); e != nil {
		return e
	}
	return tx.Commit()
}

func decodeHealth(v string) any {
	var h ConntrackHealth
	if json.Unmarshal([]byte(v), &h) != nil {
		return map[string]any{"available": false, "degraded": true, "lastError": "Invalid collector health checkpoint"}
	}
	return h
}

// IsAccountingEnabled is observational; enabling it is a separate explicit
// administrator deployment step, never a side effect of starting the monitor.
func IsAccountingEnabled(proc fs.FS) bool {
	b, e := fs.ReadFile(proc, "sys/net/netfilter/nf_conntrack_acct")
	return e == nil && strings.TrimSpace(string(b)) == "1"
}

func defaultSystemRoots() (fs.FS, fs.FS) { return os.DirFS("/sys"), os.DirFS("/proc") }
