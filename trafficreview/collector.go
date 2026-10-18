package trafficreview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func Epoch() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

type CollectorOptions struct {
	Interface string
	Interval  time.Duration
	Conntrack bool
}

// Collect has no network listener. A separate least-privilege unit may grant
// CAP_NET_ADMIN to conntrack without granting it to the panel's HTTP process.
func (s *Store) Collect(ctx context.Context, o CollectorOptions) error {
	if o.Interval < time.Second || o.Interval > time.Minute {
		return errors.New("sample interval must be 1s..1m")
	}
	sys, proc := defaultSystemRoots()
	local, e := InterfaceAddresses(o.Interface)
	if e != nil {
		return e
	}
	first, e := ReadNIC(sys, proc, o.Interface, time.Now().Unix())
	if e != nil {
		return e
	}
	if e = s.RecordNIC(first); e != nil {
		return e
	}
	if _, e = s.db.Exec("INSERT INTO review_meta VALUES('interval_seconds',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.Itoa(int(o.Interval/time.Second))); e != nil {
		return e
	}
	lastNICAt := first.At
	epoch := Epoch()
	queue := make(chan ConntrackRecord, 8192)
	health := make(chan ConntrackHealth, 1)
	var dropped atomic.Uint64
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	if o.Conntrack {
		collector := NewConntrackCollector(func(r ConntrackRecord) {
			select {
			case queue <- r:
			default:
				dropped.Add(1)
			}
		}, func(h ConntrackHealth) {
			select {
			case health <- h:
			default:
				select {
				case <-health:
				default:
				}
				select {
				case health <- h:
				default:
				}
			}
		})
		wg.Add(1)
		go func() { defer wg.Done(); collector.Run(child, o.Interval) }()
	} else {
		_ = s.SetConntrackHealth(ConntrackHealth{Degraded: true, LastError: "Conntrack collection disabled; destination wire-byte coverage unavailable"})
	}
	if e = s.Alert(first.At, "collector_start_gap", "Collector epoch "+epoch+" started; kernel flows first observed as snapshots are baselined"); e != nil {
		return e
	}
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	ticks := 0
	pending := make([]ConntrackRecord, 0, 8192)
	flush := func() error {
		for len(pending) < 16384 {
			select {
			case r := <-queue:
				pending = append(pending, r)
			default:
				goto drained
			}
		}
	drained:
		if n := dropped.Swap(0); n > 0 {
			if e := s.Alert(time.Now().Unix(), "queue_gap", fmt.Sprintf("%d conntrack observations dropped by bounded queue; per-peer totals incomplete", n)); e != nil {
				return e
			}
		}
		if len(pending) > 0 {
			if e := s.RecordConntrack(epoch, pending, local); e != nil {
				return e
			}
			pending = pending[:0]
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			if at := time.Now().Unix(); at > lastNICAt {
				if n, readErr := ReadNIC(sys, proc, o.Interface, at); readErr == nil {
					if e = s.RecordNIC(n); e != nil {
						return e
					}
				} else {
					return readErr
				}
			}
			if e = flush(); e != nil {
				return e
			}
			return nil
		case h := <-health:
			if !IsAccountingEnabled(proc) {
				h.Available = false
				h.Degraded = true
				h.LastError = "nf_conntrack_acct is disabled or unreadable; byte counters are not available"
			}
			if e = s.SetConntrackHealth(h); e != nil {
				return e
			}
		case <-ticker.C:
			n, e := ReadNIC(sys, proc, o.Interface, time.Now().Unix())
			if e != nil {
				return e
			}
			if e = s.RecordNIC(n); e != nil {
				return e
			}
			lastNICAt = n.At
			if e = flush(); e != nil {
				return e
			}
			ticks++
			if ticks%6 == 0 {
				if e = s.Retain(n.At); e != nil {
					return e
				}
			}
		}
	}
}

// DefaultDB is intentionally opt-in. It never falls back into the panel DB.
func DefaultDB() string { return os.Getenv("SUI_TRAFFIC_REVIEW_DB") }
