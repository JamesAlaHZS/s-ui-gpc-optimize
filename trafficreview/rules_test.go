package trafficreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBusinessDomainSuffixCannotBeSpoofed(t *testing.T) {
	for _, domain := range []string{"openai.com.evil.example", "fakeopenai.com", "openai.com@evil.example", "203.0.113.9", ""} {
		if business, confidence := Business(domain); business != "unknown" || confidence != "unknown" {
			t.Errorf("spoofed/unknown domain %q classified %s/%s", domain, business, confidence)
		}
	}
	if business, confidence := Business("API.OPENAI.COM."); business != "OpenAI" || confidence != "inferred" {
		t.Fatalf("expected only inferred domain classification, got %s/%s", business, confidence)
	}
}

func TestPeerRulesRemainManualCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	content := `{"version":"operator-v1","peerRules":[{"cidr":"198.51.100.0/24","feeClass":"carrier_candidate","reason":"manual note"},{"cidr":"198.51.100.9/32","feeClass":"standard_candidate","reason":"specific note"}]}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rules.Version, "operator-v1:") {
		t.Fatalf("missing version/hash: %s", rules.Version)
	}
	if got, basis := rules.ClassifyPeer("198.51.100.9"); got != "standard_candidate" || !strings.Contains(basis, "Operator hypothesis") {
		t.Fatalf("longest candidate: %s, %s", got, basis)
	}
	if got, _ := rules.ClassifyPeer("198.51.100.10"); got != "carrier_candidate" {
		t.Fatalf("wide candidate: %s", got)
	}
	for _, peer := range []string{"203.0.113.1", "openai.com", ""} {
		if got, _ := rules.ClassifyPeer(peer); got != "unknown" {
			t.Errorf("unmatched peer %q classified %s", peer, got)
		}
	}
	for _, fee := range []string{"free", "confirmed", "carrier_confirmed"} {
		bad := `{"version":"x","peerRules":[{"cidr":"198.51.100.0/24","feeClass":"` + fee + `","reason":"manual"}]}`
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRules(path); err == nil {
			t.Errorf("accepted authoritative fee class %q", fee)
		}
	}
}

func TestDirectOutboundWithoutPeerRuleStaysUnknown(t *testing.T) {
	s := testStore(t)
	if err := s.RecordProxy("boot", []ProxyRecord{{ID: "direct", Peer: "198.51.100.20", Outbound: "direct", At: 1000, Up: 12, Down: 3}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Destinations(testRange())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FeeStatus != "unknown" {
		t.Fatalf("direct incorrectly implied free/Standard: %+v", rows)
	}
}
