package trafficreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockBQRunner struct {
	dryRes   BigQueryDryRunResult
	dryErr   error
	queryRes BigQueryQueryResult
	queryErr error
	queried  int
}

func (m *mockBQRunner) DryRun(_ context.Context, _ string, _ BillingSyncConfig) (BigQueryDryRunResult, error) {
	return m.dryRes, m.dryErr
}

func (m *mockBQRunner) Query(_ context.Context, _ string, _ BillingSyncConfig) (BigQueryQueryResult, error) {
	m.queried++
	return m.queryRes, m.queryErr
}

func TestFiveSKUBillingSeparationAndQuotaScope(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	sampleRows := []StandardExportRow{
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUStandardInternetEgressOregon,
			Description:  "Network Standard Data Transfer Out to Internet from Oregon",
			InvoiceMonth: "202610", UsageAmount: "69.03", UsageUnit: "GiB", Cost: "0.00", Credits: "0", Currency: "USD", ExportTime: now.Add(-2 * time.Hour).Unix(),
		},
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUInterZoneEgress,
			Description:  "Network Inter Zone Data Transfer Out",
			InvoiceMonth: "202610", UsageAmount: "0.02", UsageUnit: "GiB", Cost: "0.00", Credits: "0", Currency: "USD", ExportTime: now.Add(-2 * time.Hour).Unix(),
		},
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUInterRegionLasVegas,
			Description:  "Network Inter Region Data Transfer Out from Americas to Las Vegas",
			InvoiceMonth: "202610", UsageAmount: "0", UsageUnit: "GiB", Cost: "0.00", Credits: "0", Currency: "USD", ExportTime: now.Add(-2 * time.Hour).Unix(),
		},
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUInterRegionVirginia,
			Description:  "Network Inter Region Data Transfer Out from Americas to Virginia",
			InvoiceMonth: "202610", UsageAmount: "0", UsageUnit: "GiB", Cost: "0.00", Credits: "0", Currency: "USD", ExportTime: now.Add(-2 * time.Hour).Unix(),
		},
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUCarrierPeeringAmericas,
			Description:  "Network Data Transfer Out via Carrier Peering Network - Americas Based",
			InvoiceMonth: "202610", UsageAmount: "10.3", UsageUnit: "GiB", Cost: "0.82", Credits: "0", Currency: "USD", ExportTime: now.Add(-2 * time.Hour).Unix(),
		},
	}
	rep, err := s.UpsertStandardExportRows(sampleRows, "test_sample", now)
	if err != nil {
		t.Fatalf("UpsertStandardExportRows failed: %v", err)
	}
	if rep.Inserted != 5 || rep.Corrected != 0 {
		t.Fatalf("unexpected sync report: %+v", rep)
	}

	overview, err := s.BillingOverview(billRange(), now)
	if err != nil {
		t.Fatalf("BillingOverview failed: %v", err)
	}

	// Verify Standard Tier 200 GiB reference ONLY counts 4986-FDE4-A69A (69.03 GiB),
	// never summing 69.03 + 0.02 + 10.3 = 79.35 GiB.
	ref := overview.StandardTierReference
	if ref.TargetVMUsedGiB != "69.03" {
		t.Fatalf("TargetVMUsedGiB must be 69.03, got %s", ref.TargetVMUsedGiB)
	}
	if ref.TargetVMRemainingRefGiB != "130.97" {
		t.Fatalf("TargetVMRemainingRefGiB must be 130.97, got %s", ref.TargetVMRemainingRefGiB)
	}
	if ref.ExcludedTargetSKUsGiB != "10.32" {
		t.Fatalf("ExcludedTargetSKUsGiB must be 10.32 (10.3 + 0.02), got %s", ref.ExcludedTargetSKUsGiB)
	}
	if ref.AccountWideComplete || ref.QuotaScope != "billing_account_wide" || ref.MeasuredScope != "stored_project_oregon_month_only" {
		t.Fatalf("quota scope metadata incorrect: %+v", ref)
	}

	// Verify Carrier Peering is recorded separately as actual billed charge.
	cp := overview.CarrierPeeringActual
	if cp.Description != DescCarrierPeeringAmericas || cp.UsageGiB != "10.3" || cp.NetCost != "0.82" || cp.CountsTowardStandard200GiB {
		t.Fatalf("carrier peering bucket incorrect: %+v", cp)
	}

	// Verify aggregated grand totals exist alongside itemized buckets for volume estimation:
	// 69.03 + 0.02 + 0 + 0 + 10.3 = 79.35 GiB total target usage, $0.82 net cost.
	tot := overview.Totals
	if tot.TargetTotalUsageGiB != "79.35" || tot.StandardFreeTierUsageGiB != "69.03" || tot.NonStandardUsageGiB != "10.32" || tot.CarrierPeeringUsageGiB != "10.3" || tot.InternalReferenceUsageGiB != "0.02" || tot.TargetNetCost != "0.82" {
		t.Fatalf("aggregated totals incorrect: %+v", tot)
	}
}

func TestDescriptionDrivenClassificationWithoutRelyingOnSKUID(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	// Rows with empty or custom SKU IDs must still be classified accurately by description.
	rows := []StandardExportRow{
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: "",
			Description:  "Network Standard Data Transfer Out to Internet from Oregon",
			InvoiceMonth: "202610", UsageAmount: "69.03", UsageUnit: "GiB", Cost: "0.00", Credits: "0", Currency: "USD", ExportTime: now.Unix(),
		},
		{
			Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: "CHANGED-BY-PROVIDER",
			Description:  "Network Data Transfer Out via Carrier Peering Network - Americas Based",
			InvoiceMonth: "202610", UsageAmount: "10.3", UsageUnit: "GiB", Cost: "0.82", Credits: "0", Currency: "USD", ExportTime: now.Unix(),
		},
	}
	rep, err := s.UpsertStandardExportRows(rows, "desc_priority_test", now)
	if err != nil || rep.Inserted != 2 {
		t.Fatalf("description-driven sync failed: rep=%+v err=%v", rep, err)
	}
	overview, err := s.BillingOverview(billRange(), now)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Totals.TargetTotalUsageGiB != "79.33" || overview.StandardTierReference.TargetVMUsedGiB != "69.03" || overview.CarrierPeeringActual.UsageGiB != "10.3" {
		t.Fatalf("description-driven overview mismatch: %+v", overview)
	}
}

func TestStandardExportQueryAndConfigBoundaries(t *testing.T) {
	baseCfg := BillingSyncConfig{
		QueryProjectID:   "my-gcp-proj",
		DatasetProjectID: "my-gcp-proj",
		DatasetID:        "billing_export",
		TableID:          "gcp_billing_export_v1_012345_6789AB_CDEF01",
		TargetProjectID:  "my-gcp-proj",
		LookbackDays:     7,
		UseADC:           true,
		Now:              time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
	}
	q, err := BuildStandardExportQuery(baseCfg)
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, sku := range []string{
		SKUCarrierPeeringAmericas,
		SKUInterZoneEgress,
		SKUInterRegionLasVegas,
		SKUInterRegionVirginia,
		SKUStandardInternetEgressOregon,
	} {
		if !strings.Contains(q, "'"+sku+"'") {
			t.Fatalf("query missing target SKU %s: %s", sku, q)
		}
	}

	// Reject Detailed usage export, Pricing export, and FOCUS tables.
	for _, badTable := range []string{
		"gcp_billing_export_resource_v1_012345_6789AB_CDEF01",
		"cloud_pricing_export",
		"focus_billing_export_v1",
		"arbitrary_table",
	} {
		cfg := baseCfg
		cfg.TableID = badTable
		if _, err := ValidateBillingSyncConfig(cfg); err == nil {
			t.Fatalf("accepted forbidden table %s", badTable)
		}
	}

	// Reject long-lived service account JSON private keys.
	withKey := baseCfg
	withKey.ServiceAccountKeyJSON = `{"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----"}`
	if _, err := ValidateBillingSyncConfig(withKey); err == nil {
		t.Fatal("accepted long-lived service account JSON key")
	}
	withFile := baseCfg
	withFile.CredentialsFile = "/etc/s-ui/sa-key.json"
	if _, err := ValidateBillingSyncConfig(withFile); err == nil {
		t.Fatal("accepted static credentials file")
	}

	// Reject >64 MiB per query or >=$0.05/month scan budget.
	overQuery := baseCfg
	overQuery.MaxBytesPerQuery = 65 * 1024 * 1024
	if _, err := ValidateBillingSyncConfig(overQuery); err == nil {
		t.Fatal("accepted >64 MiB per-query limit")
	}
	overMonth := baseCfg
	overMonth.MaxMonthlyBytes = 9 * 1024 * 1024 * 1024
	if _, err := ValidateBillingSyncConfig(overMonth); err == nil {
		t.Fatal("accepted monthly scan limit exceeding $0.05/mo guardrail")
	}
}

func TestStandardExportIdempotencyLateArrivalAndCorrections(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	// Disallowed SKU must fail cleanly.
	if _, err := s.UpsertStandardExportRows([]StandardExportRow{{
		Date: "2026-10-08", ProjectID: "p1", SKUID: "UNRELATED-SKU", UsageAmount: "1", UsageUnit: "GiB", Cost: "0.10", Currency: "USD",
	}}, "test", now); err == nil {
		t.Fatal("accepted non-target SKU in automatic sync")
	}

	// Initial poll with byte unit and exact decimal cost + credit.
	initial := []StandardExportRow{
		{
			Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas,
			InvoiceMonth: "202610", UsageAmount: "10737418240", UsageUnit: "bytes", // exact 10 GiB
			Cost: "0.850000000001", Credits: "-0.030000000000", Currency: "USD", ExportTime: 1700001000,
		},
	}
	r1, err := s.UpsertStandardExportRows(initial, "bq_standard_export", now)
	if err != nil || r1.Inserted != 1 || r1.Corrected != 0 {
		t.Fatalf("initial sync failed: %+v, err=%v", r1, err)
	}

	// Duplicate 12-hour poll with identical totals must not double-count.
	r2, err := s.UpsertStandardExportRows(initial, "bq_standard_export", now.Add(12*time.Hour))
	if err != nil || r2.Inserted != 0 || r2.Corrected != 0 || r2.Unchanged != 1 {
		t.Fatalf("idempotent poll failed: %+v, err=%v", r2, err)
	}

	// Stale export snapshot with older ExportTime must be ignored.
	stale := []StandardExportRow{
		{
			Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas,
			InvoiceMonth: "202610", UsageAmount: "5", UsageUnit: "GiB",
			Cost: "0.40", Credits: "0", Currency: "USD", ExportTime: 1700000500,
		},
	}
	r3, err := s.UpsertStandardExportRows(stale, "bq_standard_export", now.Add(13*time.Hour))
	if err != nil || r3.StaleIgnored != 1 || r3.Corrected != 0 {
		t.Fatalf("stale snapshot was not ignored: %+v, err=%v", r3, err)
	}

	// Late-arriving billing update (>24h lag) with newer ExportTime replaces the daily total
	// rather than adding 10.3 on top of 10.0, and logs a bill_correction alert.
	corrected := []StandardExportRow{
		{
			Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas,
			InvoiceMonth: "202610", UsageAmount: "10.3", UsageUnit: "GiB",
			Cost: "0.85", Credits: "-0.03", Currency: "USD", ExportTime: 1700090000,
		},
	}
	r4, err := s.UpsertStandardExportRows(corrected, "bq_standard_export", now.Add(36*time.Hour))
	if err != nil || r4.Corrected != 1 || r4.Inserted != 0 {
		t.Fatalf("late correction failed: %+v, err=%v", r4, err)
	}

	bills, err := s.Bills(billRange())
	if err != nil || len(bills) != 1 {
		t.Fatalf("expected 1 bill row after correction, got %+v (err=%v)", bills, err)
	}
	if bills[0].UsageGiB != "10.3" || bills[0].Cost != "0.85" || bills[0].Credits != "-0.03" || bills[0].NetCost != "0.82" {
		t.Fatalf("unexpected corrected bill values: %+v", bills[0])
	}

	alerts, err := s.Alerts(Range{From: now.Unix() - 60, To: now.Add(48 * time.Hour).Unix(), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	foundCorrection := false
	for _, a := range alerts {
		if a.Kind == "bill_correction" && strings.Contains(a.Message, "10 -> 10.3 GiB") {
			foundCorrection = true
		}
	}
	if !foundCorrection {
		t.Fatalf("expected bill_correction alert in %+v", alerts)
	}
}

func TestBigQueryDryRunAndBudgetCircuitBreaker(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	baseCfg := BillingSyncConfig{
		QueryProjectID:   "my-gcp-proj",
		DatasetProjectID: "my-gcp-proj",
		DatasetID:        "billing_export",
		TableID:          "gcp_billing_export_v1_012345_6789AB_CDEF01",
		TargetProjectID:  "my-gcp-proj",
		LookbackDays:     7,
		UseADC:           true,
		Now:              now,
	}

	// 1. Dry-run only within budget succeeds without calling Query.
	dryCfg := baseCfg
	dryCfg.DryRunOnly = true
	runner := &mockBQRunner{
		dryRes: BigQueryDryRunResult{
			EstimatedBytes:      8 * 1024 * 1024, // 8 MiB
			DatasetStorageBytes: 512 * 1024 * 1024,
		},
	}
	rep, err := s.SyncStandardBilling(context.Background(), dryCfg, runner)
	if err != nil || rep.Status != "dry_run_ok" || runner.queried != 0 {
		t.Fatalf("dry-run failed: rep=%+v queried=%d err=%v", rep, runner.queried, err)
	}

	// 2. Dry-run exceeding 64 MiB per-query limit pauses sync and never executes Query.
	overPerQueryRunner := &mockBQRunner{
		dryRes: BigQueryDryRunResult{
			EstimatedBytes:      65 * 1024 * 1024, // 65 MiB > 64 MiB
			DatasetStorageBytes: 512 * 1024 * 1024,
		},
	}
	if _, err := s.SyncStandardBilling(context.Background(), baseCfg, overPerQueryRunner); err == nil || overPerQueryRunner.queried != 0 {
		t.Fatalf("expected >64 MiB dry-run to be rejected before Query; queried=%d err=%v", overPerQueryRunner.queried, err)
	}
	st, err := s.BillingSyncStatus(now)
	if err != nil || !st.BudgetPaused {
		t.Fatalf("expected BudgetPaused=true after over-limit dry run: %+v err=%v", st, err)
	}

	// 3. Dataset storage exceeding 10 GiB guardrail is rejected before Query.
	overStorageRunner := &mockBQRunner{
		dryRes: BigQueryDryRunResult{
			EstimatedBytes:      4 * 1024 * 1024,
			DatasetStorageBytes: 11 * 1024 * 1024 * 1024, // 11 GiB > 10 GiB
		},
	}
	if _, err := s.SyncStandardBilling(context.Background(), baseCfg, overStorageRunner); err == nil || overStorageRunner.queried != 0 {
		t.Fatalf("expected >10 GiB storage to be rejected; queried=%d err=%v", overStorageRunner.queried, err)
	}

	// 4. Existing monthly usage + dry-run exceeding 3.875 GiB monthly cap is rejected.
	overMonthlyRunner := &mockBQRunner{
		dryRes: BigQueryDryRunResult{
			EstimatedBytes:              32 * 1024 * 1024,
			DatasetStorageBytes:         100 * 1024 * 1024,
			ExistingAccountMonthlyBytes: DefaultMaxMonthlyBytes - 16*1024*1024,
		},
	}
	if _, err := s.SyncStandardBilling(context.Background(), baseCfg, overMonthlyRunner); err == nil || overMonthlyRunner.queried != 0 {
		t.Fatalf("expected monthly budget overflow to be rejected; queried=%d err=%v", overMonthlyRunner.queried, err)
	}

	// 5. Normal sync within budget executes Query and clears BudgetPaused.
	okRunner := &mockBQRunner{
		dryRes: BigQueryDryRunResult{
			EstimatedBytes:      12 * 1024 * 1024,
			DatasetStorageBytes: 256 * 1024 * 1024,
		},
		queryRes: BigQueryQueryResult{
			BilledBytes: 12 * 1024 * 1024,
			Rows: []StandardExportRow{
				{
					Date: "2026-10-08", ProjectID: "my-gcp-proj", SKUID: SKUStandardInternetEgressOregon,
					InvoiceMonth: "202610", UsageAmount: "69.03", UsageUnit: "GiB",
					Cost: "0", Credits: "0", Currency: "USD", ExportTime: now.Add(-1 * time.Hour).Unix(),
				},
			},
		},
	}
	rep, err = s.SyncStandardBilling(context.Background(), baseCfg, okRunner)
	if err != nil || rep.Inserted != 1 || okRunner.queried != 1 {
		t.Fatalf("normal sync failed: rep=%+v queried=%d err=%v", rep, okRunner.queried, err)
	}
	st, err = s.BillingSyncStatus(now)
	if err != nil || st.BudgetPaused || st.DataPending || st.MonthlyScannedBytes != 12*1024*1024 {
		t.Fatalf("unexpected sync status after valid sync: %+v err=%v", st, err)
	}
}

func TestADCBigQueryClientEndToEndMockHTTP(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var sawDryRun, sawLiveQuery bool
	var liveMaxBytesBilled string

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "missing Metadata-Flavor", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "adc-short-lived-token"})
	})
	mux.HandleFunc("/bigquery/v2/projects/my-gcp-proj/datasets/billing_export/tables/gcp_billing_export_v1_012345_6789AB_CDEF01", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer adc-short-lived-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"numBytes": "104857600"}) // 100 MiB
	})
	mux.HandleFunc("/bigquery/v2/projects/my-gcp-proj/queries", func(w http.ResponseWriter, r *http.Request) {
		var req bqQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.DryRun {
			sawDryRun = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jobComplete":         true,
				"totalBytesProcessed": "10485760",
			})
			return
		}
		sawLiveQuery = true
		liveMaxBytesBilled = req.MaximumBytesBilled
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobComplete":         true,
			"totalBytesProcessed": "10485760",
			"totalBytesBilled":    "10485760",
			"rows": []map[string]any{
				{
					"f": []map[string]any{
						{"v": "2026-10-08"},
						{"v": "my-gcp-proj"},
						{"v": SKUCarrierPeeringAmericas},
						{"v": "Network Data Transfer Out via Carrier Peering Network - Americas Based"},
						{"v": "202610"},
						{"v": "GiB"},
						{"v": "10.3"},
						{"v": "0.82"},
						{"v": "0"},
						{"v": "USD"},
						{"v": "1760780000"},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &ADCBigQueryClient{
		HTTPClient:       srv.Client(),
		MetadataTokenURL: srv.URL + "/token",
		BigQueryBaseURL:  srv.URL + "/bigquery/v2",
	}
	cfg := BillingSyncConfig{
		QueryProjectID:   "my-gcp-proj",
		DatasetProjectID: "my-gcp-proj",
		DatasetID:        "billing_export",
		TableID:          "gcp_billing_export_v1_012345_6789AB_CDEF01",
		TargetProjectID:  "my-gcp-proj",
		UseADC:           true,
		Now:              now,
	}
	rep, err := s.SyncStandardBilling(context.Background(), cfg, client)
	if err != nil {
		t.Fatalf("ADCBigQueryClient sync failed: %v", err)
	}
	if !sawDryRun || !sawLiveQuery || liveMaxBytesBilled != "67108864" || rep.Inserted != 1 {
		t.Fatalf("unexpected ADC execution: dry=%v live=%v maxBytesBilled=%s rep=%+v", sawDryRun, sawLiveQuery, liveMaxBytesBilled, rep)
	}
}

func TestCarrierPeeringCorrelationAndRelayComparison(t *testing.T) {
	s := billStore(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	day1Start := time.Date(2026, 10, 8, 0, 0, 0, 0, loc)
	day2Start := day1Start.AddDate(0, 0, 1)
	r := Range{From: day1Start.Unix(), To: day2Start.Unix(), Timezone: "America/Los_Angeles", Limit: 200}
	afterRange := Range{From: day2Start.Unix(), To: day2Start.AddDate(0, 0, 1).Unix(), Timezone: "America/Los_Angeles", Limit: 200}
	at := r.From + 1800

	// Record proxy remote traffic and a network client-return leg (to_client).
	if err := s.RecordProxy("epoch-1", []ProxyRecord{
		{ID: "p1", Peer: "198.51.100.25", Domain: "video.example.com", User: "alice", Inbound: "vless-in", Outbound: "direct", Protocol: "tcp", At: at, Up: 4 * (1 << 30), Down: 1 << 20},
	}); err != nil {
		t.Fatal(err)
	}
	local := map[string]bool{"10.138.0.3": true}
	if err := s.RecordConntrack("epoch-1", []ConntrackRecord{
		// Remote leg: 10.138.0.3 -> 198.51.100.25
		{
			At: at, Event: "new", Protocol: "tcp", RawID: "101",
			Original:       FlowTuple{SourceIP: "10.138.0.3", DestinationIP: "198.51.100.25", SourcePort: 45000, DestinationPort: 443},
			Reply:          FlowTuple{SourceIP: "198.51.100.25", DestinationIP: "10.138.0.3", SourcePort: 443, DestinationPort: 45000},
			OrigBytesKnown: true, ReplyBytesKnown: true, OrigBytes: 4 * (1 << 30), ReplyBytes: 1 << 20,
		},
		// Client leg: 203.0.113.88 (client) -> 10.138.0.3 (VPS)
		{
			At: at, Event: "new", Protocol: "tcp", RawID: "102",
			Original:       FlowTuple{SourceIP: "203.0.113.88", DestinationIP: "10.138.0.3", SourcePort: 55000, DestinationPort: 443},
			Reply:          FlowTuple{SourceIP: "10.138.0.3", DestinationIP: "203.0.113.88", SourcePort: 443, DestinationPort: 55000},
			OrigBytesKnown: true, ReplyBytesKnown: true, OrigBytes: 1 << 20, ReplyBytes: 4 * (1 << 30),
		},
	}, local); err != nil {
		t.Fatal(err)
	}

	// Sync Day 1 (pre-relay: 2026-10-08) and Day 2 (post-relay: 2026-10-09) Carrier Peering bills.
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.UpsertStandardExportRows([]StandardExportRow{
		{Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas, InvoiceMonth: "202610", UsageAmount: "10.3", UsageUnit: "GiB", Cost: "0.82", Credits: "0", Currency: "USD", ExportTime: now.Unix()},
		{Date: "2026-10-09", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas, InvoiceMonth: "202610", UsageAmount: "2.1", UsageUnit: "GiB", Cost: "0.17", Credits: "0", Currency: "USD", ExportTime: now.Unix()},
	}, "test", now); err != nil {
		t.Fatal(err)
	}

	corr, err := s.CarrierCorrelation(r)
	if err != nil {
		t.Fatalf("CarrierCorrelation failed: %v", err)
	}
	if corr.FlowBillingConfirmed {
		t.Fatal("CarrierCorrelation must never confirm per-flow billing")
	}
	if corr.BilledCarrierPeeringGiB != "10.3" || corr.BilledCarrierPeeringCost != "0.82" {
		t.Fatalf("unexpected billed Carrier Peering in correlation report: %+v", corr)
	}
	if len(corr.Candidates) == 0 {
		t.Fatal("expected candidate destinations")
	}
	// Ensure client IP 203.0.113.88 from to_client leg is NEVER placed into RelaySuggestion.IPCIDRs.
	for _, cidr := range corr.RelaySuggestion.IPCIDRs {
		if strings.HasPrefix(cidr, "203.0.113.88/") {
			t.Fatalf("client IP from to_client leg leaked into relay routing suggestion: %+v", corr.RelaySuggestion)
		}
	}
	if len(corr.RelaySuggestion.Domains) != 1 || corr.RelaySuggestion.Domains[0] != "video.example.com" {
		t.Fatalf("expected video.example.com in relay suggestion domains, got %+v", corr.RelaySuggestion.Domains)
	}
	// Verify default relay status is pending_configuration (待配置).
	if corr.RelaySuggestion.Configured || corr.RelaySuggestion.Status != "pending_configuration" || corr.RelaySuggestion.OutboundTag != "pending_configuration" {
		t.Fatalf("expected relay suggestion to default to pending_configuration, got %+v", corr.RelaySuggestion)
	}
	if corr.RelaySuggestion.SingBoxRule["outbound"] != PendingRelayRulePlaceholder {
		t.Fatalf("expected placeholder outbound in pending state, got %+v", corr.RelaySuggestion.SingBoxRule)
	}

	// Verify explicit relay outbound tag configuration works when later supplied.
	corrConfigured, err := s.CarrierCorrelationWithRelay(r, "custom-relay-out")
	if err != nil {
		t.Fatal(err)
	}
	if !corrConfigured.RelaySuggestion.Configured || corrConfigured.RelaySuggestion.Status != "configured_preview" || corrConfigured.RelaySuggestion.OutboundTag != "custom-relay-out" {
		t.Fatalf("expected configured_preview when relay tag is supplied, got %+v", corrConfigured.RelaySuggestion)
	}

	cmp, err := s.CompareRelayPeriods(r, afterRange)
	if err != nil {
		t.Fatalf("CompareRelayPeriods failed: %v", err)
	}
	if !cmp.Reduced || cmp.DeltaCarrierGiB != "-8.2" || cmp.DeltaCarrierCost != "-0.65" {
		t.Fatalf("unexpected relay period comparison: %+v", cmp)
	}
}
