package trafficreview

import (
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	DefaultRelayOutboundTag     = "pending_configuration"
	PendingRelayRulePlaceholder = "<PENDING_RELAY_OUTBOUND>"
	CarrierBoundaryNote         = "Google Cloud 'Network Data Transfer Out via Carrier Peering Network - Americas Based' billing totals do not identify individual domains, IPs, or connections. Local collector records provide time-window, byte-volume, user, and route correlation only; flowBillingConfirmed is always false. Relay routing defaults to pending_configuration until a relay node is explicitly decided."
)

var relayOperatorWarnings = []string{
	"Relay outbound is currently in pending_configuration (待配置) state; no production route is modified automatically.",
	"GCP VPS -> relay node traffic is still network egress; Google billing category depends on actual BGP/peering route to the relay IP and is not guaranteed to avoid Carrier Peering.",
	"Verify that client traffic actually traverses this GCP S-UI node; direct client-to-website connections never reach the GCP VPS.",
	"CDN and Anycast domains rotate IPs frequently; combine domain suffix/keyword rules with verified IP CIDRs rather than relying on a single static IP.",
	"Measure both legs (VPS -> relay/website and VPS -> client); responses returning from the relay to the client via GCP still produce VPS egress.",
	"Do not reroute all traffic at once; test a small set of high-correlation candidates first and compare before/after Carrier Peering billing totals.",
	"Account for external relay VPS/bandwidth costs separately; the <$0.05/month limit applies to additional Google Cloud BigQuery sync charges.",
}

// CarrierCorrelation analyzes local collector destinations alongside billed
// Carrier Peering totals for the window, defaulting relay routing to
// pending_configuration.
func (s *Store) CarrierCorrelation(r Range) (CarrierCorrelationReport, error) {
	return s.CarrierCorrelationWithRelay(r, "")
}

// CarrierCorrelationWithRelay analyzes local collector destinations and builds
// a candidate relay routing template. When relayOutboundTag is empty or
// "pending_configuration", the relay suggestion stays in pending_configuration.
func (s *Store) CarrierCorrelationWithRelay(r Range, relayOutboundTag string) (CarrierCorrelationReport, error) {
	destRange := r
	if destRange.Limit <= 0 || destRange.Limit > 500 {
		destRange.Limit = 200
	}
	destinations, err := s.Destinations(destRange)
	if err != nil {
		return CarrierCorrelationReport{}, err
	}
	bills, err := s.allBills(r)
	if err != nil {
		return CarrierCorrelationReport{}, err
	}
	overview := SummarizeBills(bills, BillingSyncStatus{})
	carrierBill := overview.CarrierPeeringActual
	hasCarrierBill := parseRatOrZero(carrierBill.UsageGiB).Sign() > 0 || parseRatOrZero(carrierBill.NetCost).Sign() > 0

	tag := strings.TrimSpace(relayOutboundTag)
	configured := tag != "" && tag != DefaultRelayOutboundTag
	status := "pending_configuration"
	effectiveTag := DefaultRelayOutboundTag
	ruleOutbound := PendingRelayRulePlaceholder
	if configured {
		status = "configured_preview"
		effectiveTag = tag
		ruleOutbound = tag
	}

	var totalProxyEgress, totalNetworkEgress int64
	for _, d := range destinations {
		if d.Layer == "proxy" && d.UpBytes > 0 {
			totalProxyEgress += d.UpBytes
		} else if d.Layer == "network" && d.TXBytes > 0 {
			totalNetworkEgress += d.TXBytes
		}
	}

	var proxyCandidateBytes, networkCandidateBytes int64
	candidates := make([]CarrierPeeringCandidate, 0, len(destinations))
	domainSet := map[string]bool{}
	cidrSet := map[string]bool{}
	domains := []string{}
	cidrs := []string{}

	for _, d := range destinations {
		var egress, ingress int64
		var layerTotal int64
		if d.Layer == "proxy" {
			egress, ingress = d.UpBytes, d.DownBytes
			layerTotal = totalProxyEgress
		} else {
			egress, ingress = d.TXBytes, d.RXBytes
			layerTotal = totalNetworkEgress
		}
		if egress <= 0 || d.FeeStatus == "standard_candidate" {
			continue
		}

		isRelayAlready := strings.Contains(strings.ToLower(d.Outbound), "relay")
		level := "observed_unclassified"
		if d.FeeStatus == "carrier_candidate" {
			level = "rule_candidate"
		} else if hasCarrierBill && !isRelayAlready {
			level = "window_correlated"
		}

		if d.Layer == "proxy" {
			proxyCandidateBytes += egress
		} else if d.Layer == "network" {
			networkCandidateBytes += egress
		}

		shareRat := new(big.Rat)
		if layerTotal > 0 {
			shareRat.SetFrac(big.NewInt(egress*100), big.NewInt(layerTotal))
		}
		egressGiBRat := big.NewRat(egress, 1<<30)

		destLabel := strings.TrimSpace(d.Domain)
		if destLabel == "" {
			destLabel = strings.TrimSpace(d.Peer)
		}
		if destLabel == "" {
			destLabel = "unknown"
		}

		basisParts := []string{}
		if d.FeeBasis != "" {
			basisParts = append(basisParts, d.FeeBasis)
		}
		if hasCarrierBill {
			basisParts = append(basisParts, fmt.Sprintf("Coincides with window '%s' (%s GiB / %s %s); correlation only, not Google per-site confirmation", DescCarrierPeeringAmericas, carrierBill.UsageGiB, carrierBill.Currency, carrierBill.NetCost))
		} else {
			basisParts = append(basisParts, "Local egress observation only; no confirmed per-site billing mapping")
		}

		gaps := []string{}
		if strings.EqualFold(d.Protocol, "udp") {
			gaps = append(gaps, "UDP association can multiplex multiple destinations")
		}
		if d.Layer == "network" && d.Domain == "" {
			gaps = append(gaps, "Kernel conntrack records peer IP only (no SNI/domain); CDN/Anycast IPs may rotate")
		}
		if d.Leg == "to_client" {
			gaps = append(gaps, "Client return leg (VPS -> client); measures return egress, do not route client IP to relay")
		}
		if len(gaps) == 0 {
			gaps = append(gaps, "No flow-to-description mapping in Google billing export")
		}

		candidates = append(candidates, CarrierPeeringCandidate{
			Destination:          destLabel,
			Peer:                 d.Peer,
			Domain:               d.Domain,
			User:                 d.User,
			Inbound:              d.Inbound,
			Outbound:             d.Outbound,
			Protocol:             d.Protocol,
			Layer:                d.Layer,
			Leg:                  d.Leg,
			Business:             d.Business,
			EgressBytes:          egress,
			IngressBytes:         ingress,
			EgressGiB:            formatRatDecimal(egressGiBRat, 6),
			WindowSharePercent:   formatRatDecimal(shareRat, 2),
			CorrelationLevel:     level,
			Confidence:           d.Confidence,
			EvidenceBasis:        strings.Join(basisParts, "; "),
			CollectionGap:        strings.Join(gaps, "; "),
			SuggestedOutboundTag: effectiveTag,
			FlowBillingConfirmed: false,
		})

		// Only remote-facing destinations (proxy layer or network to_remote leg)
		// are eligible for candidate relay routing rules; never route client IPs.
		if (d.Layer == "proxy" || d.Leg == "to_remote") && !isRelayAlready && len(domains)+len(cidrs) < 16 {
			if dom := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d.Domain), ".")); dom != "" && !domainSet[dom] {
				domainSet[dom] = true
				domains = append(domains, dom)
			} else if addr, err := netip.ParseAddr(strings.TrimSpace(d.Peer)); err == nil {
				addr = addr.Unmap()
				bits := 32
				if addr.Is6() {
					bits = 128
				}
				cidr := fmt.Sprintf("%s/%d", addr.String(), bits)
				if !cidrSet[cidr] {
					cidrSet[cidr] = true
					cidrs = append(cidrs, cidr)
				}
			}
		}
	}

	sort.Strings(domains)
	sort.Strings(cidrs)
	ruleObj := map[string]any{
		"type":     "logical",
		"mode":     "or",
		"action":   "route",
		"outbound": ruleOutbound,
		"rules":    buildSingBoxSubRules(domains, cidrs),
	}
	if len(domains)+len(cidrs) == 0 {
		ruleObj = nil
	}

	proxyGiB := big.NewRat(proxyCandidateBytes, 1<<30)
	netGiB := big.NewRat(networkCandidateBytes, 1<<30)

	return CarrierCorrelationReport{
		Range:                    r,
		BilledCarrierPeeringGiB:  carrierBill.UsageGiB,
		BilledCarrierPeeringCost: carrierBill.NetCost,
		BilledCurrency:           carrierBill.Currency,
		LocalProxyCandidateGiB:   formatRatDecimal(proxyGiB, 6),
		LocalNetworkCandidateGiB: formatRatDecimal(netGiB, 6),
		Candidates:               candidates,
		RelaySuggestion: RelayRouteSuggestion{
			Status:      status,
			Configured:  configured,
			OutboundTag: effectiveTag,
			Domains:     domains,
			IPCIDRs:     cidrs,
			SingBoxRule: ruleObj,
			ManualOnly:  true,
			Warnings:    append([]string(nil), relayOperatorWarnings...),
		},
		Partial:              true,
		FlowBillingConfirmed: false,
		BoundaryNote:         CarrierBoundaryNote,
	}, nil
}

func buildSingBoxSubRules(domains, cidrs []string) []map[string]any {
	sub := []map[string]any{}
	if len(domains) > 0 {
		sub = append(sub, map[string]any{"domain": domains})
	}
	if len(cidrs) > 0 {
		sub = append(sub, map[string]any{"ip_cidr": cidrs})
	}
	return sub
}

// CompareRelayPeriods compares actual billed Carrier Peering usage and cost
// between a pre-relay window and a post-relay window.
func (s *Store) CompareRelayPeriods(beforeRange, afterRange Range) (RelayPeriodComparison, error) {
	now := time.Now()
	beforeOverview, err := s.BillingOverview(beforeRange, now)
	if err != nil {
		return RelayPeriodComparison{}, err
	}
	afterOverview, err := s.BillingOverview(afterRange, now)
	if err != nil {
		return RelayPeriodComparison{}, err
	}
	beforeCarrier := beforeOverview.CarrierPeeringActual
	afterCarrier := afterOverview.CarrierPeeringActual
	if beforeCarrier.RowCount == 0 || afterCarrier.RowCount == 0 {
		return RelayPeriodComparison{}, fmt.Errorf("both periods require imported Carrier Peering billing evidence")
	}
	if beforeCarrier.Currency == "MIXED" || beforeCarrier.Currency != afterCarrier.Currency {
		return RelayPeriodComparison{}, fmt.Errorf("cannot compare Carrier Peering costs in different currencies")
	}

	beforeGiB := parseRatOrZero(beforeCarrier.UsageGiB)
	afterGiB := parseRatOrZero(afterCarrier.UsageGiB)
	deltaGiB := new(big.Rat).Sub(afterGiB, beforeGiB)

	beforeCost := parseRatOrZero(beforeCarrier.NetCost)
	afterCost := parseRatOrZero(afterCarrier.NetCost)
	deltaCost := new(big.Rat).Sub(afterCost, beforeCost)

	currency := afterCarrier.Currency
	if currency == "" {
		currency = beforeCarrier.Currency
	}
	if currency == "" {
		currency = "USD"
	}
	reduced := deltaGiB.Sign() < 0 || (deltaGiB.Sign() == 0 && deltaCost.Sign() < 0)

	return RelayPeriodComparison{
		BeforeRange:       beforeRange,
		AfterRange:        afterRange,
		BeforeCarrierGiB:  formatRatDecimal(beforeGiB, 12),
		AfterCarrierGiB:   formatRatDecimal(afterGiB, 12),
		DeltaCarrierGiB:   formatRatDecimal(deltaGiB, 12),
		BeforeCarrierCost: formatRatDecimal(beforeCost, 12),
		AfterCarrierCost:  formatRatDecimal(afterCost, 12),
		DeltaCarrierCost:  formatRatDecimal(deltaCost, 12),
		Currency:          currency,
		Reduced:           reduced,
		VerificationNote:  "Comparison is based on billed 'Network Data Transfer Out via Carrier Peering Network - Americas Based' totals across the two windows. Ensure both windows have completed BigQuery export (>24h lag) before drawing final routing conclusions.",
	}, nil
}
