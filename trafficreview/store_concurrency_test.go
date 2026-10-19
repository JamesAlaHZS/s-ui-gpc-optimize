package trafficreview

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Reproduce the read-then-write pattern shared by proxy checkpoints and billing
// imports. A competing connection must wait rather than invalidate its snapshot.
func TestWriteTransactionWaitsForOtherStore(t *testing.T) {
	a := testStore(t)
	b, err := Open(a.path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM review_meta").Scan(&count); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Alert(1000, "test", "competing writer") }()
	select {
	case err := <-done:
		t.Fatalf("competing writer bypassed active write transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = setMeta(tx, "concurrency_test", "committed"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("competing writer did not resume")
	}
	if err = b.db.QueryRow("SELECT COUNT(*) FROM review_alerts WHERE kind='test'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("competing write lost: count=%d, err=%v", count, err)
	}
}

func TestConcurrentStoresPreserveProxyCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	seed, err := Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	const workers, records = 3, 40
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			s, err := Open(path, DefaultRules())
			if err != nil {
				errors <- err
				return
			}
			defer s.Close()
			for i := 0; i < records; i++ {
				err = s.RecordProxy("concurrent", []ProxyRecord{{ID: fmt.Sprintf("%d-%d", worker, i), At: 1000, Domain: "chatgpt.com", Protocol: "tcp", Up: 10, Down: 20, Closed: true}})
				if err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var up, down int64
	if err := seed.db.QueryRow("SELECT SUM(up),SUM(down) FROM traffic_hours WHERE layer='proxy'").Scan(&up, &down); err != nil {
		t.Fatal(err)
	}
	if up != workers*records*10 || down != workers*records*20 {
		t.Fatalf("lost or duplicated counters: up=%d down=%d", up, down)
	}
}
