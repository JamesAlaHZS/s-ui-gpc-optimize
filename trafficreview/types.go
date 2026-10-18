// Package trafficreview keeps local measurements separate from billing evidence.
// It never contacts a cloud API, resolves peers, or changes proxy routing.
package trafficreview

import "time"

const RuleVersion = "local-v1"
const Disclaimer = "Local byte measurements are not Google billing classification. Direct routing, domain names and ASN ownership do not prove Standard or Carrier Peering. Imported bills preserve operator-supplied project/day/SKU totals; source authenticity is not automatically verified, and an individual flow is never confirmed billable. Proxy payload and network-interface bytes use different accounting layers."

type Range struct {
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Timezone string `json:"timezone"`
	Limit    int    `json:"-"`
}

func DefaultRange(now time.Time) Range {
	return Range{From: now.Add(-24 * time.Hour).Unix(), To: now.Unix() + 1, Timezone: "America/Los_Angeles", Limit: 200}
}

type Summary struct {
	Range
	RuleVersion          string `json:"ruleVersion"`
	RXBytes              int64  `json:"rxBytes"`
	TXBytes              int64  `json:"txBytes"`
	ProxyUploadBytes     int64  `json:"proxyUploadBytes"`
	ProxyDownloadBytes   int64  `json:"proxyDownloadBytes"`
	NetworkEgressBytes   int64  `json:"networkEgressBytes"`
	NetworkIngressBytes  int64  `json:"networkIngressBytes"`
	LastSampleAt         int64  `json:"lastSampleAt"`
	SamplingSeconds      int    `json:"samplingSeconds"`
	Partial              bool   `json:"partial"`
	FlowBillingConfirmed bool   `json:"flowBillingConfirmed"`
	Disclaimer           string `json:"disclaimer"`
}

type SeriesPoint struct {
	Time    int64   `json:"time"`
	RXBytes int64   `json:"rxBytes"`
	TXBytes int64   `json:"txBytes"`
	RXBPS   float64 `json:"rxBps"`
	TXBPS   float64 `json:"txBps"`
	Quality string  `json:"quality"`
}

type Destination struct {
	Layer       string `json:"layer"`
	Peer        string `json:"peer"`
	Domain      string `json:"domain"`
	User        string `json:"user"`
	Inbound     string `json:"inbound"`
	Outbound    string `json:"outbound"`
	Protocol    string `json:"protocol"`
	Business    string `json:"business"`
	Confidence  string `json:"confidence"`
	UpBytes     int64  `json:"upBytes"`
	DownBytes   int64  `json:"downBytes"`
	TXBytes     int64  `json:"txBytes"`
	RXBytes     int64  `json:"rxBytes"`
	Leg         string `json:"leg"`
	FeeStatus   string `json:"feeStatus"`
	FeeBasis    string `json:"feeBasis"`
	RuleVersion string `json:"ruleVersion"`
}

type ProxyRecord struct {
	ID, Peer, Domain, User, Inbound, Outbound, Protocol string
	At, Up, Down                                        int64
	Closed                                              bool
}

type Alert struct {
	ID          int64  `json:"id"`
	Time        int64  `json:"time"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	RuleVersion string `json:"ruleVersion"`
}

type Bill struct {
	Date         string `json:"date"`
	ProjectID    string `json:"projectID"`
	SKUID        string `json:"skuID"`
	Description  string `json:"description"`
	UsageGiB     string `json:"usageGiB"`
	Cost         string `json:"cost"`
	Currency     string `json:"currency"`
	SourceSHA256 string `json:"sourceSHA256"`
	Scope        string `json:"scope"`
}

type Health struct {
	Enabled          bool   `json:"enabled"`
	Available        bool   `json:"available"`
	LastSampleAt     int64  `json:"lastSampleAt"`
	SamplingSeconds  int    `json:"samplingSeconds"`
	NetworkInterface string `json:"networkInterface"`
	DatabaseBytes    int64  `json:"databaseBytes"`
	Status           string `json:"status"`
	Conntrack        any    `json:"conntrack"`
	Gaps             int64  `json:"gaps"`
}
