package trafficreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func reviewSyncConfig() BillingSyncConfig {
	return BillingSyncConfig{QueryProjectID: "my-gcp-proj", DatasetID: "billing_export", TableID: "gcp_billing_export_v1_012345_6789AB_CDEF01", TargetProjectID: "my-gcp-proj", UseADC: true, Now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
}

func TestBillingQueryIncludesWholePacificDayAcrossDST(t *testing.T) {
	for _, tc := range []struct{ now, start string }{
		{"2026-10-18T19:30:00Z", "2026-10-11T07:00:00Z"},
		{"2026-11-04T19:30:00Z", "2026-10-28T07:00:00Z"},
		{"2026-03-12T19:30:00Z", "2026-03-05T08:00:00Z"},
	} {
		cfg := reviewSyncConfig()
		cfg.Now, _ = time.Parse(time.RFC3339, tc.now)
		q, err := BuildStandardExportQuery(cfg)
		if err != nil || !strings.Contains(q, "usage_start_time >= TIMESTAMP('"+tc.start+"')") {
			t.Fatalf("partial first billing day: %s, %v", q, err)
		}
	}
}

func TestBillingTotalsDoNotAddCurrencies(t *testing.T) {
	rows := []Bill{
		{SKUID: SKUCarrierPeeringAmericas, UsageGiB: "2", Cost: "1", Currency: "USD"},
		{SKUID: SKUCarrierPeeringAmericas, UsageGiB: "3", Cost: "2", Currency: "EUR"},
	}
	o := SummarizeBills(rows, BillingSyncStatus{})
	if o.Totals.Currency != "MIXED" || o.Totals.TargetNetCost != "" || o.Totals.AllImportedNetCost != "" || o.CarrierPeeringActual.NetCost != "" || o.Totals.TargetTotalUsageGiB != "5" {
		t.Fatalf("mixed currency money must be unavailable: %+v", o)
	}
}

func TestBillingOverviewUsesAllRowsAndCalendarMonth(t *testing.T) {
	s := billStore(t)
	rows := []StandardExportRow{}
	for i := 0; i < 501; i++ {
		rows = append(rows, StandardExportRow{Date: "2026-10-08", ProjectID: fmt.Sprintf("proj-%d", i), SKUID: SKUStandardInternetEgressOregon, UsageAmount: "1", UsageUnit: "GiB", Cost: "0", Currency: "USD"})
	}
	rows = append(rows, StandardExportRow{Date: "2026-10-01", ProjectID: "proj-0", SKUID: SKUStandardInternetEgressOregon, UsageAmount: "10", UsageUnit: "GiB", Cost: "0", Currency: "USD"})
	if _, err := s.UpsertStandardExportRows(rows, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	o, err := s.BillingOverview(billRange(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if o.Totals.TargetTotalUsageGiB != "501" || o.StandardTierReference.TargetVMUsedGiB != "511" {
		t.Fatalf("list limit or selected day corrupted totals/month reference: %+v", o)
	}
}

func TestFailedAndMalformedBillingQueriesStillConsumeBudget(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			s := billStore(t)
			cfg := reviewSyncConfig()
			cfg.MaxMonthlyBytes = DefaultMaxBytesPerQuery
			m := &mockBQRunner{dryRes: BigQueryDryRunResult{EstimatedBytes: 1}, queryErr: errors.New("response lost after execution")}
			if malformed {
				m.queryErr = nil
				m.queryRes = BigQueryQueryResult{BilledBytes: DefaultMaxBytesPerQuery, Rows: []StandardExportRow{{Date: "invalid"}}}
			}
			if _, err := s.SyncStandardBilling(context.Background(), cfg, m); err == nil {
				t.Fatal("expected failed sync")
			}
			if _, err := s.SyncStandardBilling(context.Background(), cfg, m); err == nil || m.queried != 1 {
				t.Fatalf("failed execution escaped budget: %v, calls=%d", err, m.queried)
			}
			st, err := s.BillingSyncStatus(cfg.Now)
			if err != nil || st.MonthlyScannedBytes != DefaultMaxBytesPerQuery || !st.DataPending {
				t.Fatalf("wrong failed-query status: %+v %v", st, err)
			}
		})
	}
}

func TestConcurrentBillingReservationsAcrossStoreInstances(t *testing.T) {
	s := billStore(t)
	other, err := Open(s.path, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	cfg := reviewSyncConfig()
	cfg.MaxMonthlyBytes = DefaultMaxBytesPerQuery
	cfg.applyDefaults()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, st := range []*Store{s, other} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			_, _, err := st.reserveBillingQuery(cfg.Now.Unix(), "202610", cfg, 0, 0)
			results <- err
		}(st)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected one reservation, got %d", succeeded)
	}
}

func TestStandardExportSignedUsageCorrection(t *testing.T) {
	s := billStore(t)
	rows := []StandardExportRow{
		{Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas, UsageAmount: "10", UsageUnit: "GiB", Cost: "1", Currency: "USD"},
		{Date: "2026-10-08", ProjectID: "p1", SKUID: SKUCarrierPeeringAmericas, UsageAmount: "-2", UsageUnit: "GiB", Cost: "-0.2", Currency: "USD"},
	}
	if _, err := s.UpsertStandardExportRows(rows, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	o, err := s.BillingOverview(billRange(), time.Now())
	if err != nil || o.CarrierPeeringActual.UsageGiB != "8" || o.CarrierPeeringActual.NetCost != "0.8" {
		t.Fatalf("signed correction rejected: %+v %v", o, err)
	}
}

func TestADCBigQueryWaitsAndPaginatesWithoutPartialReplacement(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			gets := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					fmt.Fprint(w, `{"access_token":"test-token"}`)
					return
				}
				if r.Method == http.MethodPost {
					fmt.Fprint(w, `{"jobComplete":false,"jobReference":{"projectId":"my-gcp-proj","jobId":"job1","location":"US"}}`)
					return
				}
				gets++
				if r.URL.Query().Get("location") != "US" {
					t.Error("lost job location")
				}
				row := func(date string) map[string]any {
					cells := []map[string]string{}
					for _, v := range []string{date, "my-gcp-proj", SKUCarrierPeeringAmericas, DescCarrierPeeringAmericas, "202610", "GiB", "1", "0.1", "0", "USD", "1792296000"} {
						cells = append(cells, map[string]string{"v": v})
					}
					return map[string]any{"f": cells}
				}
				out := map[string]any{"jobComplete": true, "totalRows": "2", "totalBytesBilled": "10485760"}
				if r.URL.Query().Get("pageToken") == "next" {
					if broken {
						http.Error(w, "page unavailable", 500)
						return
					}
					out["rows"] = []any{row("2026-10-09")}
				} else {
					out["pageToken"] = "next"
					out["rows"] = []any{row("2026-10-08")}
				}
				json.NewEncoder(w).Encode(out)
			}))
			defer srv.Close()
			c := &ADCBigQueryClient{HTTPClient: srv.Client(), MetadataTokenURL: srv.URL + "/token", BigQueryBaseURL: srv.URL}
			cfg := reviewSyncConfig()
			cfg.MaxBytesPerQuery = DefaultMaxBytesPerQuery
			result, err := c.Query(context.Background(), "SELECT test", cfg)
			if broken {
				if err == nil || len(result.Rows) != 0 {
					t.Fatal("partial result accepted")
				}
				return
			}
			if err != nil || len(result.Rows) != 2 || gets != 2 {
				t.Fatalf("pagination failed: %+v %v, gets=%d", result, err, gets)
			}
		})
	}
}

func TestRelayComparisonRejectsMissingBilling(t *testing.T) {
	s := billStore(t)
	if _, err := s.CompareRelayPeriods(billRange(), billRange()); err == nil {
		t.Fatal("missing billing treated as zero")
	}
}
