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
	Layer    string `json:"-"`
	Leg      string `json:"-"`
	Sort     string `json:"-"`
}

func DefaultRange(now time.Time) Range {
	return Range{From: now.Add(-24 * time.Hour).Unix(), To: now.Unix() + 1, Timezone: "America/Los_Angeles", Limit: 200}
}

type Summary struct {
	Range
	RuleVersion                 string                    `json:"ruleVersion"`
	RXBytes                     int64                     `json:"rxBytes"`
	TXBytes                     int64                     `json:"txBytes"`
	NICTotalBytes               int64                     `json:"nicTotalBytes"`
	ProxyUploadBytes            int64                     `json:"proxyUploadBytes"`
	ProxyDownloadBytes          int64                     `json:"proxyDownloadBytes"`
	ProxyTotalBytes             int64                     `json:"proxyTotalBytes"`
	NetworkEgressBytes          int64                     `json:"networkEgressBytes"`
	NetworkIngressBytes         int64                     `json:"networkIngressBytes"`
	NetworkTotalBytes           int64                     `json:"networkTotalBytes"`
	ToRemoteEgressBytes         int64                     `json:"toRemoteEgressBytes"`
	ToClientEgressBytes         int64                     `json:"toClientEgressBytes"`
	UnknownDirectionEgressBytes int64                     `json:"unknownDirectionEgressBytes"`
	EgressDifferenceBytes       int64                     `json:"egressDifferenceBytes"`
	NetworkSampleCount          int64                     `json:"networkSampleCount"`
	ProxySampleCount            int64                     `json:"proxySampleCount"`
	FirstSampleAt               int64                     `json:"firstSampleAt"`
	LastSampleAt                int64                     `json:"lastSampleAt"`
	SamplingSeconds             int                       `json:"samplingSeconds"`
	Partial                     bool                      `json:"partial"`
	DestinationDetailsPartial   bool                      `json:"destinationDetailsPartial"`
	FlowBillingConfirmed        bool                      `json:"flowBillingConfirmed"`
	Disclaimer                  string                    `json:"disclaimer"`
	BillingOverview             *BillingOverview          `json:"billingOverview,omitempty"`
	CarrierCorrelation          *CarrierCorrelationReport `json:"carrierCorrelation,omitempty"`
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
	// TXKnown is collection metadata, not an inferred zero-byte reading.
	TXKnown bool `json:"-"`
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
	Date                       string `json:"date"`
	ProjectID                  string `json:"projectID"`
	SKUID                      string `json:"skuID"`
	Description                string `json:"description"`
	UsageGiB                   string `json:"usageGiB"`
	Cost                       string `json:"cost"`
	Currency                   string `json:"currency"`
	SourceSHA256               string `json:"sourceSHA256"`
	Scope                      string `json:"scope"`
	Category                   string `json:"category,omitempty"`
	AccountingTreatment        string `json:"accountingTreatment,omitempty"`
	CountsTowardStandard200GiB bool   `json:"countsTowardStandard200GiB"`
	InvoiceMonth               string `json:"invoiceMonth,omitempty"`
	Credits                    string `json:"credits,omitempty"`
	NetCost                    string `json:"netCost,omitempty"`
	UsageUnit                  string `json:"usageUnit,omitempty"`
	ExportTime                 int64  `json:"exportTime,omitempty"`
	SyncedAt                   int64  `json:"syncedAt,omitempty"`
	SyncSource                 string `json:"syncSource,omitempty"`
}

type SKUBillingBucket struct {
	SKUID                      string `json:"skuID"`
	Description                string `json:"description"`
	Category                   string `json:"category"`
	Handling                   string `json:"handling"`
	CountsTowardStandard200GiB bool   `json:"countsTowardStandard200GiB"`
	UsageGiB                   string `json:"usageGiB"`
	GrossCost                  string `json:"grossCost"`
	Credits                    string `json:"credits"`
	NetCost                    string `json:"netCost"`
	Currency                   string `json:"currency"`
	RowCount                   int    `json:"rowCount"`
}

type StandardTierQuotaReference struct {
	ReferenceLimitGiB       string `json:"referenceLimitGiB"`
	TargetVMDescription     string `json:"targetVMDescription"`
	TargetVMEgressSKU       string `json:"targetVMEgressSKU"`
	TargetVMUsedGiB         string `json:"targetVMUsedGiB"`
	TargetVMRemainingRefGiB string `json:"targetVMRemainingRefGiB"`
	ExcludedTargetSKUsGiB   string `json:"excludedTargetSKUsGiB"`
	QuotaScope              string `json:"quotaScope"`
	MeasuredScope           string `json:"measuredScope"`
	AccountWideComplete     bool   `json:"accountWideComplete"`
	Note                    string `json:"note"`
}

type BillingTotals struct {
	TargetTotalUsageGiB       string `json:"targetTotalUsageGiB"`
	StandardFreeTierUsageGiB  string `json:"standardFreeTierUsageGiB"`
	NonStandardUsageGiB       string `json:"nonStandardUsageGiB"`
	CarrierPeeringUsageGiB    string `json:"carrierPeeringUsageGiB"`
	InternalReferenceUsageGiB string `json:"internalReferenceUsageGiB"`
	TargetGrossCost           string `json:"targetGrossCost"`
	TargetCredits             string `json:"targetCredits"`
	TargetNetCost             string `json:"targetNetCost"`
	AllImportedUsageGiB       string `json:"allImportedUsageGiB"`
	AllImportedNetCost        string `json:"allImportedNetCost"`
	Currency                  string `json:"currency"`
	EstimationNote            string `json:"estimationNote"`
}

type BillingSyncStatus struct {
	Enabled               bool   `json:"enabled"`
	Mode                  string `json:"mode"`
	IntervalHours         int    `json:"intervalHours"`
	LastSyncAt            int64  `json:"lastSyncAt"`
	LastExportTime        int64  `json:"lastExportTime"`
	LastStatus            string `json:"lastStatus"`
	DataPending           bool   `json:"dataPending"`
	LagNotice             string `json:"lagNotice"`
	MaxBytesPerQuery      int64  `json:"maxBytesPerQuery"`
	MaxMonthlyBytes       int64  `json:"maxMonthlyBytes"`
	MonthlyScannedBytes   int64  `json:"monthlyScannedBytes"`
	EstimatedMonthlyUSD   string `json:"estimatedMonthlyUSD"`
	MonthlyBudgetLimitUSD string `json:"monthlyBudgetLimitUSD"`
	BudgetPaused          bool   `json:"budgetPaused"`
}

type BillingOverview struct {
	TargetSKUs            []SKUBillingBucket         `json:"targetSKUs"`
	Totals                BillingTotals              `json:"totals"`
	StandardTierReference StandardTierQuotaReference `json:"standardTierReference"`
	CarrierPeeringActual  SKUBillingBucket           `json:"carrierPeeringActual"`
	OtherSKUsCount        int                        `json:"otherSKUsCount"`
	SyncStatus            BillingSyncStatus          `json:"syncStatus"`
	AccountingNote        string                     `json:"accountingNote"`
}

type CarrierPeeringCandidate struct {
	Destination          string `json:"destination"`
	Peer                 string `json:"peer"`
	Domain               string `json:"domain"`
	User                 string `json:"user"`
	Inbound              string `json:"inbound"`
	Outbound             string `json:"outbound"`
	Protocol             string `json:"protocol"`
	Layer                string `json:"layer"`
	Leg                  string `json:"leg"`
	Business             string `json:"business"`
	EgressBytes          int64  `json:"egressBytes"`
	IngressBytes         int64  `json:"ingressBytes"`
	EgressGiB            string `json:"egressGiB"`
	WindowSharePercent   string `json:"windowSharePercent"`
	CorrelationLevel     string `json:"correlationLevel"`
	Confidence           string `json:"confidence"`
	EvidenceBasis        string `json:"evidenceBasis"`
	CollectionGap        string `json:"collectionGap"`
	SuggestedOutboundTag string `json:"suggestedOutboundTag"`
	FlowBillingConfirmed bool   `json:"flowBillingConfirmed"`
}

type RelayRouteSuggestion struct {
	Status      string         `json:"status"`
	Configured  bool           `json:"configured"`
	OutboundTag string         `json:"outboundTag"`
	Domains     []string       `json:"domains"`
	IPCIDRs     []string       `json:"ipCIDRs"`
	SingBoxRule map[string]any `json:"singBoxRule"`
	ManualOnly  bool           `json:"manualOnly"`
	Warnings    []string       `json:"warnings"`
}

type CarrierCorrelationReport struct {
	Range                    Range                     `json:"range"`
	BilledCarrierPeeringGiB  string                    `json:"billedCarrierPeeringGiB"`
	BilledCarrierPeeringCost string                    `json:"billedCarrierPeeringCost"`
	BilledCurrency           string                    `json:"billedCurrency"`
	LocalProxyCandidateGiB   string                    `json:"localProxyCandidateGiB"`
	LocalNetworkCandidateGiB string                    `json:"localNetworkCandidateGiB"`
	Candidates               []CarrierPeeringCandidate `json:"candidates"`
	RelaySuggestion          RelayRouteSuggestion      `json:"relaySuggestion"`
	Partial                  bool                      `json:"partial"`
	FlowBillingConfirmed     bool                      `json:"flowBillingConfirmed"`
	BoundaryNote             string                    `json:"boundaryNote"`
}

type RelayPeriodComparison struct {
	BeforeRange       Range  `json:"beforeRange"`
	AfterRange        Range  `json:"afterRange"`
	BeforeCarrierGiB  string `json:"beforeCarrierGiB"`
	AfterCarrierGiB   string `json:"afterCarrierGiB"`
	DeltaCarrierGiB   string `json:"deltaCarrierGiB"`
	BeforeCarrierCost string `json:"beforeCarrierCost"`
	AfterCarrierCost  string `json:"afterCarrierCost"`
	DeltaCarrierCost  string `json:"deltaCarrierCost"`
	Currency          string `json:"currency"`
	Reduced           bool   `json:"reduced"`
	VerificationNote  string `json:"verificationNote"`
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
