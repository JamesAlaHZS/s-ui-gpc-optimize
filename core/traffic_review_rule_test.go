package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/alireza0/s-ui/trafficreview"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
)

func TestTrafficReviewRelayPreviewUsesSingBoxRuleSchema(t *testing.T) {
	s, err := trafficreview.Open(filepath.Join(t.TempDir(), "review.db"), trafficreview.DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().Unix() / 60 * 60
	if err := s.RecordProxy("test", []trafficreview.ProxyRecord{{ID: "1", Domain: "video.example.com", Peer: "198.51.100.2", At: at, Up: 1024}}); err != nil {
		t.Fatal(err)
	}
	r := trafficreview.Range{From: at, To: at + 60, Limit: 10}
	report, err := s.CarrierCorrelationWithRelay(r, "relay-out")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report.RelaySuggestion.SingBoxRule)
	if err != nil {
		t.Fatal(err)
	}
	var rule option.Rule
	if err := singjson.UnmarshalContext(context.Background(), data, &rule); err != nil {
		t.Fatalf("invalid sing-box route preview: %s: %v", data, err)
	}
	if !rule.IsValid() {
		t.Fatalf("invalid rule: %s", data)
	}
}
