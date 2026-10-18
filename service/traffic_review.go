package service

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/config"
	"github.com/alireza0/s-ui/core"
	"github.com/alireza0/s-ui/trafficreview"
	"os"
)

type reviewRuntime struct {
	store  *trafficreview.Store
	core   *core.Core
	cancel context.CancelFunc
	done   chan struct{}
}

var reviewState struct {
	sync.RWMutex
	runtime *reviewRuntime
}

func TrafficReviewStore() *trafficreview.Store {
	reviewState.RLock()
	defer reviewState.RUnlock()
	if reviewState.runtime == nil {
		return nil
	}
	return reviewState.runtime.store
}

// StartTrafficReview is opt-in and independent of the panel's schema/stats.
// The conntrack/NIC collector is a separate executable with no HTTP listener.
func StartTrafficReview(c *core.Core) error {
	path := trafficreview.DefaultDB()
	if path == "" {
		return nil
	}
	a, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	b, e := filepath.Abs(config.GetDBPath())
	if e != nil {
		return e
	}
	if strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return errors.New("traffic review database must be separate from panel database")
	}
	rules, e := trafficreview.LoadRules(os.Getenv("SUI_TRAFFIC_REVIEW_RULES"))
	if e != nil {
		return e
	}
	store, e := trafficreview.Open(a, rules)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &reviewRuntime{store: store, core: c, cancel: cancel, done: make(chan struct{})}
	reviewState.Lock()
	if reviewState.runtime != nil {
		reviewState.Unlock()
		store.Close()
		cancel()
		return errors.New("traffic review already running")
	}
	reviewState.runtime = r
	reviewState.Unlock()
	go r.run(ctx)
	return nil
}

func StopTrafficReview() {
	reviewState.Lock()
	r := reviewState.runtime
	reviewState.runtime = nil
	reviewState.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
		_ = r.store.Close()
	}
}

func (r *reviewRuntime) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var tracker *core.SessionTracker
	epoch := trafficreview.Epoch()
	type batch struct {
		epoch   string
		records []trafficreview.ProxyRecord
	}
	pending := []batch{}
	flush := func() {
		if r.core != nil {
			if box := r.core.GetInstance(); box != nil && box.SessionTracker() != tracker {
				// Save old closed sessions before switching tracker lifetimes.
				if tracker != nil {
					rows, lost := tracker.DrainReview()
					pending = append(pending, batch{epoch, proxyRecords(rows)})
					if lost > 0 {
						_ = r.store.Alert(time.Now().Unix(), "proxy_queue_gap", "Closed proxy review queue overflowed")
					}
				}
				tracker = box.SessionTracker()
				epoch = trafficreview.Epoch()
				_ = r.store.Alert(time.Now().Unix(), "proxy_start_gap", "New proxy tracker epoch; domain classification covers only observed supplied/sniffed metadata")
			}
		}
		if tracker != nil {
			rows, lost := tracker.DrainReview()
			if len(rows) > 0 {
				pending = append(pending, batch{epoch, proxyRecords(rows)})
			}
			if lost > 0 {
				_ = r.store.Alert(time.Now().Unix(), "proxy_queue_gap", "Closed proxy review queue overflowed; short-flow attribution incomplete")
			}
		}
		kept := pending[:0]
		failed := false
		for _, b := range pending {
			if !failed {
				if e := r.store.RecordProxy(b.epoch, b.records); e == nil {
					continue
				}
				failed = true
			}
			kept = append(kept, b)
		}
		pending = kept
		n := 0
		for _, b := range pending {
			n += len(b.records)
		}
		if n > 16384 {
			_ = r.store.Alert(time.Now().Unix(), "proxy_persistence_gap", "Retry buffer exceeded 16384 snapshots; oldest uncommitted attribution discarded")
			for len(pending) > 1 && n > 16384 {
				n -= len(pending[0].records)
				pending = pending[1:]
			}
			if len(pending) == 1 && len(pending[0].records) > 16384 {
				pending[0].records = pending[0].records[len(pending[0].records)-16384:]
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			if len(pending) > 0 {
				_ = r.store.Alert(time.Now().Unix(), "proxy_shutdown_gap", "Uncommitted proxy snapshots remain at shutdown")
			}
			return
		case <-ticker.C:
			flush()
		}
	}
}

func proxyRecords(rows []core.ReviewSnapshot) []trafficreview.ProxyRecord {
	out := make([]trafficreview.ProxyRecord, 0, len(rows))
	at := time.Now().Unix()
	for _, row := range rows {
		peer := row.Destination
		if host, _, e := net.SplitHostPort(peer); e == nil {
			peer = host
		}
		out = append(out, trafficreview.ProxyRecord{ID: row.ID, Peer: peer, Domain: row.Domain, User: row.User, Inbound: row.Inbound, Outbound: row.Outbound, Protocol: row.Network, At: at, Up: row.Upload, Down: row.Download, Closed: row.Closed})
	}
	return out
}
