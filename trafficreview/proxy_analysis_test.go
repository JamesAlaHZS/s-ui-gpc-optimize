package trafficreview

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func proxyTestDay(t *testing.T, s *Store, day time.Time, candidate, other, unknown int64, complete bool) {
	t.Helper()
	if candidate > 0 {
		addHistoryDestination(t, s, day.Add(12*time.Hour).Unix(), Destination{Layer: "proxy", Domain: "chatgpt.com", Protocol: "tcp", UpBytes: candidate / 2, DownBytes: candidate - candidate/2})
	}
	if other > 0 {
		addHistoryDestination(t, s, day.Add(13*time.Hour).Unix(), Destination{Layer: "proxy", Domain: "example.com", Protocol: "tcp", UpBytes: other})
	}
	if unknown > 0 {
		addHistoryDestination(t, s, day.Add(14*time.Hour).Unix(), Destination{Layer: "proxy", UpBytes: unknown})
	}
	if complete {
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for at := day.Unix(); at < day.AddDate(0, 0, 1).Unix(); at += 3600 {
			if _, err = tx.Exec("INSERT INTO proxy_coverage_hours VALUES(?,3600) ON CONFLICT(at) DO UPDATE SET seconds=3600", at); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func proxyTestBill(t *testing.T, s *Store, day time.Time, project, currency, usage, net string) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO review_bills(date,project_id,sku_id,description,usage_gib,cost,currency,source_sha,scope,net_cost) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(date,project_id,sku_id,currency) DO UPDATE SET usage_gib=excluded.usage_gib,net_cost=excluded.net_cost`, day.Format("2006-01-02"), project, SKUCarrierPeeringAmericas, DescCarrierPeeringAmericas, usage, net, currency, "test", "project_day_sku", net)
	if err != nil {
		t.Fatal(err)
	}
}

func proxyTestWindow(t *testing.T) (time.Time, time.Time, Range) {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 4, 0, 0, 0, 0, loc)
	now := start.AddDate(0, 0, 20)
	return start, now, Range{From: start.Unix(), To: start.AddDate(0, 0, 10).Unix(), Limit: 500, Timezone: "America/Los_Angeles"}
}

func TestProxyAnalysisMirrorsForwardedPayloadAndUsesOnlyDomains(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for _, d := range []Destination{
		{Layer: "proxy", Domain: "www.chatgpt.com", Protocol: "tcp", UpBytes: 10, DownBytes: 90},
		{Layer: "proxy", Domain: "chatgpt.com.attacker.example", Protocol: "tcp", UpBytes: 3, DownBytes: 7},
		{Layer: "proxy", Domain: "203.0.113.1", Protocol: "tcp", UpBytes: 5},
		{Layer: "proxy", Domain: "chatgpt.com", Protocol: "udp", DownBytes: 8},
		{Layer: "proxy", Domain: "bad/domain", Protocol: "tcp", DownBytes: 2},
		{Layer: "network", Domain: "chatgpt.com", TXBytes: 999999},
	} {
		addHistoryDestination(t, s, start.Add(time.Hour).Unix(), d)
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.RemoteUploadBytes != 18 || v.RemoteDownloadBytes != 107 || v.ForwardedEgressBytes != 125 || v.ForwardedIngressBytes != 125 || v.CandidateForwardedEgressBytes != 100 || v.CandidateForwardedIngressBytes != 100 || v.CandidateUploadBytes != 10 || v.CandidateDownloadBytes != 90 || v.OtherForwardedEgressBytes != 10 || v.UnknownDomainBytes != 15 || !v.HasObservations || v.FlowBillingConfirmed {
		t.Fatalf("wrong proxy measurement %+v", v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["measurementBasis"] != "proxy_forwarded_payload" {
		t.Fatalf("missing layer semantics %s", b)
	}
}

func TestProxyCoverageDoesNotBridgeFirstTickEpochChangeOrFailedTicks(t *testing.T) {
	s := testStore(t)
	at := time.Now().Truncate(time.Hour).Unix()
	for _, tick := range []struct {
		at    int64
		epoch string
	}{{at + 3585, "a"}, {at + 3595, "a"}, {at + 3607, "a"}, {at + 3620, "b"}, {at + 3630, "b"}, {at + 3700, "b"}, {at + 3710, "b"}} {
		if err := s.RecordProxyCoverage(tick.at, 10, tick.epoch); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordProxyCoverage(at+3710, 10, "b"); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := s.db.QueryRow("SELECT seconds FROM proxy_coverage_hours WHERE at=?", at).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT seconds FROM proxy_coverage_hours WHERE at=?", at+3600).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != 15 || after != 27 {
		t.Fatalf("gap or duplicate coverage invented: %d %d", before, after)
	}
	for _, bad := range []struct {
		at       int64
		interval int
		epoch    string
	}{{0, 10, "a"}, {at, 0, "a"}, {at, 31, "a"}, {at, 10, ""}} {
		if err := s.RecordProxyCoverage(bad.at, bad.interval, bad.epoch); err == nil {
			t.Fatalf("accepted bad heartbeat %+v", bad)
		}
	}
}

func TestProxyIdleCoverageIsObservedZeroAndEligibleBaseline(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, 0, 0, 0, true)
		proxyTestBill(t, s, day, "p", "USD", "0", "0")
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if !v.HasObservations || v.Observations != 0 || v.Partial || v.ForwardedEgressBytes != 0 || v.BillingComparison.EligibleDays != 10 || v.BillingComparison.PairCount != 10 || v.BillingComparison.Reason != "constant_candidate" {
		t.Fatalf("idle observations confused with absent data %+v", v)
	}
}

func TestProxyComparisonCorrelatedButNeverConfirmsFees(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 50, 0, true)
		proxyTestBill(t, s, day, "p", "USD", fmt.Sprint(i+1), "0.01")
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	comparison := v.BillingComparison
	if comparison.Status != "correlated" || comparison.PairCount != 10 || comparison.EligibleDays != 10 || len(comparison.Scopes) != 1 || comparison.Scopes[0].CandidateCorrelation == nil || *comparison.Scopes[0].CandidateCorrelation < 0.999 || comparison.FlowBillingConfirmed || v.FlowBillingConfirmed {
		t.Fatalf("wrong comparison %+v", comparison)
	}
	// Re-read after an authoritative correction. Amounts stay exact (including
	// negative net costs); corrected usage changes the statistics immediately.
	proxyTestBill(t, s, start, "p", "USD", "1000", "-0.125")
	v, err = s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.BillingComparison.Status == "correlated" || v.BillingComparison.Scopes[0].CarrierNetCost != "-0.035" {
		t.Fatalf("correction was cached or money changed %+v", v.BillingComparison)
	}
}

func TestProxyComparisonRejectsConfoundingConstantsUnknownsAndInsufficientDays(t *testing.T) {
	for _, tc := range []struct {
		name, reason                     string
		days                             int
		candidate, other, unknown, usage func(int) int64
	}{
		{"short", "insufficient_days", 6, func(i int) int64 { return int64(i+1) * 100 }, func(int) int64 { return 10 }, func(int) int64 { return 0 }, func(i int) int64 { return int64(i + 1) }},
		{"charges without candidate", "charges_without_candidate", 10, func(int) int64 { return 0 }, func(i int) int64 { return int64(i+1) * 100 }, func(int) int64 { return 0 }, func(i int) int64 { return int64(i + 1) }},
		{"candidate constant", "constant_candidate", 10, func(int) int64 { return 100 }, func(i int) int64 { return int64(i+1) * 10 }, func(int) int64 { return 0 }, func(i int) int64 { return int64(i + 1) }},
		{"total constant", "constant_total", 10, func(i int) int64 { return int64(i+1) * 10 }, func(i int) int64 { return 1000 - int64(i+1)*10 }, func(int) int64 { return 0 }, func(i int) int64 { return int64(i + 1) }},
		{"billing constant", "constant_billing", 10, func(i int) int64 { return int64(i+1) * 100 }, func(int) int64 { return 10 }, func(int) int64 { return 0 }, func(int) int64 { return 1 }},
		{"mix", "confounded", 10, func(i int) int64 { return int64(i+1) * 100 }, func(i int) int64 { return int64(i+1) * 50 }, func(int) int64 { return 0 }, func(i int) int64 { return int64(i + 1) }},
		{"unknown domains", "unknown_domain_mix", 10, func(i int) int64 { return int64(i+1) * 100 }, func(int) int64 { return 10 }, func(int) int64 { return 1000 }, func(i int) int64 { return int64(i + 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			start, now, r := proxyTestWindow(t)
			for i := 0; i < tc.days; i++ {
				day := start.AddDate(0, 0, i)
				proxyTestDay(t, s, day, tc.candidate(i), tc.other(i), tc.unknown(i), true)
				proxyTestBill(t, s, day, "p", "USD", fmt.Sprint(tc.usage(i)), "1")
			}
			v, err := s.ProxyAnalysis(r, now)
			if err != nil || v.BillingComparison.Status == "correlated" || v.BillingComparison.Reason != tc.reason {
				t.Fatalf("unsafe conclusion %+v, %v", v.BillingComparison, err)
			}
		})
	}
}

func TestProxyComparisonUsesOtherTrackedSKUAsZeroCarrierEvidence(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 10, 0, true)
		if i == 3 || i == 6 {
			_, err := s.db.Exec(`INSERT INTO review_bills(date,project_id,sku_id,description,usage_gib,cost,currency,source_sha,scope,net_cost) VALUES(?,?,?,?,?,?,?,?,?,?)`, day.Format("2006-01-02"), "p", SKUStandardInternetEgressOregon, DescStandardInternetEgressOregon, "1", "0", "USD", "test", "project_day_sku", "0")
			if err != nil { t.Fatal(err) }
			continue
		}
		proxyTestBill(t, s, day, "p", "USD", fmt.Sprint(i+1), "0.01")
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil { t.Fatal(err) }
	c := v.BillingComparison
	if c.PairCount != 10 || len(c.Scopes) != 1 { t.Fatalf("tracked-SKU evidence days were not paired: %+v", c) }
	scope := c.Scopes[0]
	if scope.EvidenceDayCount != 10 || scope.BillingDayCount != 8 || scope.ZeroUsageDays != 2 { t.Fatalf("billing evidence counters incorrect: %+v", scope) }
	zeroDays := 0
	for _, day := range scope.Days { if day.CarrierUsageGiB == "0" { zeroDays++ } }
	if zeroDays != 2 { t.Fatalf("expected two evidenced zero-Carrier days, got %d: %+v", zeroDays, scope.Days) }
}

func TestProxyComparisonMissingBillNotZeroAndNegativeUsageExcluded(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 10, 0, true)
		if i == 3 {
			continue
		}
		usage := fmt.Sprint(i + 1)
		if i == 5 {
			usage = "-1"
		}
		proxyTestBill(t, s, day, "p", "USD", usage, "-0.1")
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	c := v.BillingComparison
	if c.PairCount != 8 || c.EligibleDays != 10 || c.Scopes[0].BillingDayCount != 9 || c.Scopes[0].CarrierNetCost != "-0.9" {
		t.Fatalf("missing/correction rows treated as measurements %+v", c)
	}
	for _, day := range c.Scopes[0].Days {
		if day.Date == start.AddDate(0, 0, 3).Format("2006-01-02") || day.Date == start.AddDate(0, 0, 5).Format("2006-01-02") {
			t.Fatalf("invalid pair retained %+v", day)
		}
	}
}

func TestProxyComparisonExcludesRecentAndUncoveredDaysAndRespectsDST(t *testing.T) {
	s := testStore(t)
	start, _, r := proxyTestWindow(t)
	now := start.AddDate(0, 0, 10).Add(12 * time.Hour)
	for i := 0; i < 11; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 10, 0, true)
		proxyTestBill(t, s, day, "p", "USD", fmt.Sprint(i+1), "1")
	}
	// March 8 is a 23-hour LA day; it remains eligible. Only an explicitly
	// missing heartbeat excludes the separate March 5 day.
	if _, err := s.db.Exec("DELETE FROM proxy_coverage_hours WHERE at=?", start.AddDate(0, 0, 1).Unix()); err != nil {
		t.Fatal(err)
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.BillingComparison.EligibleDays != 7 || v.BillingComparison.PairCount != 7 {
		t.Fatalf("recent/coverage/DST days wrong %+v", v.BillingComparison)
	}
	found := false
	for _, d := range v.BillingComparison.Scopes[0].Days {
		if d.Date == "2026-03-08" {
			found = true
		}
	}
	if !found {
		t.Fatal("23-hour DST day incorrectly excluded")
	}
	if err = s.Alert(start.AddDate(0, 0, 4).Unix()+60, "proxy_queue_gap", "test gap"); err != nil {
		t.Fatal(err)
	}
	v, err = s.ProxyAnalysis(r, now)
	if err != nil || v.BillingComparison.PairCount != 6 {
		t.Fatalf("proxy gap ignored %+v, %v", v.BillingComparison, err)
	}
}

func TestProxyComparisonAutomaticWindowIsExplicitAndDoesNotExpandTotals(t *testing.T) {
	s := testStore(t)
	start, now, _ := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 10, 0, true)
		proxyTestBill(t, s, day, "p", "USD", fmt.Sprint(i+1), "1")
	}
	r := Range{From: start.AddDate(0, 0, 9).Unix(), To: start.AddDate(0, 0, 10).Unix(), Limit: 500}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.ForwardedEgressBytes != 1010 || v.BillingComparison.PairCount != 10 || v.BillingComparison.AnalysisTo != r.To || v.BillingComparison.AnalysisFrom >= r.From {
		t.Fatalf("automatic window leaked into selected totals %+v", v)
	}
	cutoff := start.AddDate(0, 0, 5).Unix()
	r.To = cutoff
	r.From = cutoff - 24*3600
	v, err = s.ProxyAnalysis(r, now)
	if err != nil || v.BillingComparison.PairCount != 5 || v.BillingComparison.AnalysisTo != cutoff {
		t.Fatalf("comparison read after historical cutoff %+v, %v", v.BillingComparison, err)
	}
}

func TestProxyComparisonNeverAttributesSharedMeasurementToMultipleProjects(t *testing.T) {
	s := testStore(t)
	start, now, r := proxyTestWindow(t)
	for i := 0; i < 10; i++ {
		day := start.AddDate(0, 0, i)
		proxyTestDay(t, s, day, int64(i+1)*100, 50, 0, true)
		proxyTestBill(t, s, day, "p1", "USD", fmt.Sprint(i+1), "1")
		proxyTestBill(t, s, day, "p2", "EUR", fmt.Sprint(i+1), "2")
	}
	v, err := s.ProxyAnalysis(r, now)
	if err != nil {
		t.Fatal(err)
	}
	c := v.BillingComparison
	if c.Status != "inconclusive" || c.Reason != "multiple_projects" || c.PairCount != 10 || len(c.Scopes) != 2 {
		t.Fatalf("projects/currencies mixed %+v", c)
	}
	for _, scope := range c.Scopes {
		if scope.Status != "inconclusive" || scope.Reason != "multiple_projects" || scope.PairCount != 10 {
			t.Fatalf("project reused measurement as proof %+v", scope)
		}
	}
}

func TestProxyBusinessMigrationIsIdempotentAndRetentionKeepsLongTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy-history.db")
	s, err := Open(path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	at := now.AddDate(0, 0, -200).Unix()
	addHistoryDestination(t, s, at, Destination{Layer: "proxy", Domain: "chatgpt.com", Protocol: "tcp", UpBytes: 20, DownBytes: 30})
	// Reconstruct surviving legacy minute data before business migration.
	if _, err = s.db.Exec("DELETE FROM proxy_business_hours; DELETE FROM proxy_business_minutes; DELETE FROM review_meta WHERE key='proxy_business_history_v1'"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = Open(path, DefaultRules())
		if err != nil {
			t.Fatal(err)
		}
		v, e := s.ProxyAnalysis(Range{From: at, To: at + 3600, Limit: 500}, now)
		if e != nil || v.ForwardedEgressBytes != 50 || v.CandidateForwardedEgressBytes != 50 {
			t.Fatalf("backfill %d repeated/lost %+v, %v", i, v, e)
		}
		if i == 0 {
			if err = s.Retain(now.Unix()); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProxyDestinationEgressRankingIncludesBothForwardedLegs(t *testing.T) {
	s := testStore(t)
	for _, d := range []Destination{{Layer: "proxy", Peer: "download", UpBytes: 1, DownBytes: 1000}, {Layer: "proxy", Peer: "upload", UpBytes: 100, DownBytes: 1}, {Layer: "network", Peer: "host", TXBytes: 999999}} {
		addHistoryDestination(t, s, 1000, d)
	}
	r := testRange()
	r.Layer, r.Sort, r.Limit = "proxy", "egress", 1
	rows, err := s.Destinations(r)
	if err != nil || len(rows) != 1 || rows[0].Peer != "download" {
		t.Fatalf("download leg lost from ranking %+v, %v", rows, err)
	}
}
