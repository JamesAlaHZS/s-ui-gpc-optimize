package trafficreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"strings"
)

type PeerRule struct {
	CIDR     string `json:"cidr"`
	FeeClass string `json:"feeClass"`
	Reason   string `json:"reason"`
	prefix   netip.Prefix
}

type Rules struct {
	Version   string     `json:"version"`
	PeerRules []PeerRule `json:"peerRules"`
}

func DefaultRules() Rules { return Rules{Version: RuleVersion} }

// LoadRules accepts operator hypotheses only. No built-in peer list pretends
// to know Google's private billing routes; every match remains a candidate.
func LoadRules(path string) (Rules, error) {
	r := DefaultRules()
	if path == "" {
		return r, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return r, err
	}
	if len(b) > 1<<20 {
		return r, errors.New("rules exceed 1 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&r); err != nil {
		return r, err
	}
	if d.Decode(new(any)) != io.EOF {
		return r, errors.New("rules must contain one JSON object")
	}
	if len(r.Version) == 0 || len(r.Version) > 64 || len(r.PeerRules) > 1024 {
		return r, errors.New("invalid rule version or rule count")
	}
	for i := range r.PeerRules {
		p := &r.PeerRules[i]
		p.prefix, err = netip.ParsePrefix(p.CIDR)
		if err != nil {
			return r, err
		}
		if p.FeeClass != "carrier_candidate" && p.FeeClass != "standard_candidate" {
			return r, errors.New("feeClass must be a candidate, never confirmed or free")
		}
		if len(strings.TrimSpace(p.Reason)) == 0 || len(p.Reason) > 512 {
			return r, errors.New("candidate needs an explicit bounded evidence reason")
		}
	}
	h := sha256.Sum256(b)
	r.Version += ":" + hex.EncodeToString(h[:8])
	return r, nil
}

func (r Rules) ClassifyPeer(peer string) (string, string) {
	a, err := netip.ParseAddr(peer)
	if err != nil {
		return "unknown", "No verified billing evidence"
	}
	a = a.Unmap()
	best := -1
	var match *PeerRule
	for i := range r.PeerRules {
		p := &r.PeerRules[i]
		if p.prefix.Contains(a) && p.prefix.Bits() > best {
			best = p.prefix.Bits()
			match = p
		}
	}
	if match != nil {
		return match.FeeClass, "Operator hypothesis: " + match.Reason
	}
	return "unknown", "No verified billing evidence"
}

// Business labels describe supplied/sniffed domains, not authenticated service
// ownership. Shared IPs, ECH, multiplexed UDP and unknown domains stay unknown.
func Business(domain string) (string, string) {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	groups := []struct {
		name  string
		roots []string
	}{
		{"OpenAI", []string{"chatgpt.com", "openai.com", "oaistatic.com", "oaiusercontent.com"}},
		{"Telegram", []string{"telegram.org", "telegram.me", "t.me"}},
		{"Microsoft", []string{"microsoft.com", "windowsupdate.com", "windows.com", "live.com"}},
		{"Google", []string{"google.com", "googleapis.com", "googleusercontent.com", "gvt1.com"}},
		{"Autodesk", []string{"autodesk.com"}},
	}
	for _, g := range groups {
		for _, root := range g.roots {
			if d == root || strings.HasSuffix(d, "."+root) {
				return g.name, "inferred"
			}
		}
	}
	return "unknown", "unknown"
}
