package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/alireza0/s-ui/core"
	"github.com/alireza0/s-ui/trafficreview"
)

// Exercise actual proxy I/O, the native tracker, service conversion and local
// database persistence. A lack of proxy rows is not evidence of zero payload.
func TestTrafficReviewServicePersistsRealProxyPayload(t *testing.T) {
	requestBody := bytes.Repeat([]byte("upload"), 100)
	responseBody := bytes.Repeat([]byte("download"), 200)
	c := core.NewCore()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, requestBody) {
			t.Errorf("origin request body: %d bytes, %v", len(body), err)
		}
		// Delivery to the origin can wake this handler before the proxy's
		// asynchronous copy has committed its upload counter. Observe the live
		// counter before returning a response and immediately stopping review.
		// Sessions is read-only; DrainReview would steal the service's queue.
		observed := false
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			for _, session := range c.GetInstance().SessionTracker().Sessions() {
				if session.Upload >= int64(len(requestBody)) {
					observed = true
					break
				}
			}
			if observed {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !observed {
			t.Error("proxy upload counter did not catch up with delivered request")
		}
		_, _ = w.Write(responseBody)
	}))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	configuration := fmt.Sprintf(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"review-test","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port)
	if err := c.Start([]byte(configuration)); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	t.Setenv("SUI_TRAFFIC_REVIEW_DB", dbPath)
	if err := StartTrafficReview(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(StopTrafficReview)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Post(origin.URL, "application/octet-stream", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !bytes.Equal(body, responseBody) {
		t.Fatalf("proxy response: %d bytes, %v", len(body), err)
	}
	// Stop performs the final drain synchronously; no ten-second sleep or
	// competing DrainReview call is needed to observe the service's own path.
	StopTrafficReview()
	// A successful drain records coverage even before the first timer tick.
	// The first heartbeat is only a baseline; it does not invent a full day.
	probe, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint string
	err = probe.QueryRow("SELECT value FROM review_meta WHERE key='proxy_coverage_checkpoint'").Scan(&checkpoint)
	_ = probe.Close()
	var coverage struct {
		At    int64  `json:"at"`
		Epoch string `json:"epoch"`
	}
	if err != nil || json.Unmarshal([]byte(checkpoint), &coverage) != nil || coverage.At == 0 || coverage.Epoch == "" {
		t.Fatalf("successful service drain did not persist coverage baseline: %q, %v", checkpoint, err)
	}
	store, err := trafficreview.Open(dbPath, trafficreview.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().Unix()
	summary, err := store.Summary(trafficreview.Range{From: now - 60, To: now + 60, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProxySampleCount == 0 || summary.ProxyUploadBytes < int64(len(requestBody)) || summary.ProxyDownloadBytes < int64(len(responseBody)) {
		t.Fatalf("real payload lost between tracker and store: %+v", summary)
	}
	if summary.TXBytes != 0 || summary.NetworkEgressBytes != 0 {
		t.Fatalf("proxy payload must not invent NIC or kernel measurements: %+v", summary)
	}
}
