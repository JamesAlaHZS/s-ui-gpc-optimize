package trafficreview

import (
	"strings"
	"testing"
	"time"
)

func TestCarrierPeeringClassificationKeepsAmericasScope(t *testing.T) {
	for _, tc := range []struct {
		name, id, description, category string
	}{
		{"canonical description", "custom-id", DescCarrierPeeringAmericas, "carrier_peering_egress"},
		{"canonical no id", "", DescCarrierPeeringAmericas, "carrier_peering_egress"},
		{"known id only", SKUCarrierPeeringAmericas, "", "carrier_peering_egress"},
		{"legacy abbreviated description", SKUCarrierPeeringAmericas, "Carrier Peering", "carrier_peering_egress"},
		{"explicit Americas abbreviation", "custom-id", "Carrier Peering - Americas", "carrier_peering_egress"},
		{"normalized Americas description", "custom-id", "NETWORK  DATA TRANSFER OUT VIA CARRIER PEERING NETWORK — AMERICAS-BASED", "carrier_peering_egress"},
		{"Europe with conflicting Americas id", SKUCarrierPeeringAmericas, "Network Data Transfer Out via Carrier Peering Network - Europe Based", "other_sku"},
		{"Asia with conflicting Americas id", SKUCarrierPeeringAmericas, "Network Data Transfer Out via Carrier Peering Network - Asia Pacific Based", "other_sku"},
		{"Europe with conflicting Standard id", SKUStandardInternetEgressOregon, "Network Data Transfer Out via Carrier Peering Network - Europe Based", "other_sku"},
		{"unknown regional sku", "unknown-id", "Carrier Peering - Europe", "other_sku"},
		{"unscoped without known id", "unknown-id", "Carrier Peering", "other_sku"},
		{"destination alone does not establish Americas based", SKUCarrierPeeringAmericas, "Carrier Peering from Europe to Americas", "other_sku"},
		{"contradicting service description", SKUCarrierPeeringAmericas, "Cloud Storage Standard Capacity", "other_sku"},
		{"description outranks id", SKUCarrierPeeringAmericas, DescStandardInternetEgressOregon, "standard_internet_egress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := ClassifyBillingItem(tc.id, tc.description)
			if spec.Category != tc.category {
				t.Fatalf("classified %q (%q) as %q, want %q", tc.description, tc.id, spec.Category, tc.category)
			}
		})
	}
}

func TestOtherCarrierRegionsStayOutsideTrackedAmericasTotals(t *testing.T) {
	s := billStore(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	rows := []StandardExportRow{
		{Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: SKUCarrierPeeringAmericas, Description: DescCarrierPeeringAmericas, UsageAmount: "3", UsageUnit: "GiB", Cost: "0.3", Credits: "0", Currency: "USD", ExportTime: now.Unix()},
		{Date: "2026-10-08", ProjectID: "oregon-prod", SKUID: "europe-carrier", Description: strings.Replace(DescCarrierPeeringAmericas, "Americas", "Europe", 1), UsageAmount: "90", UsageUnit: "GiB", Cost: "9", Credits: "0", Currency: "USD", ExportTime: now.Unix()},
	}
	if _, err := s.UpsertStandardExportRows(rows[:1], "scope-regression", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertStandardExportRows(rows[1:], "scope-regression", now); err == nil {
		t.Fatal("automatic target sync accepted a non-Americas carrier item")
	}
	// Manual imports retain non-target rows for audit; those rows must stay
	// outside the five tracked totals rather than being silently relabeled.
	if _, err := s.ImportBill(strings.NewReader(billHeader + "2026-10-08,oregon-prod,europe-carrier," + rows[1].Description + ",90,9,USD\n")); err != nil {
		t.Fatal(err)
	}
	overview, err := s.BillingOverview(billRange(), now)
	if err != nil {
		t.Fatal(err)
	}
	if overview.CarrierPeeringActual.UsageGiB != "3" || overview.CarrierPeeringActual.NetCost != "0.3" || overview.Totals.TargetTotalUsageGiB != "3" || overview.Totals.AllImportedUsageGiB != "93" {
		t.Fatalf("other regions leaked into Americas totals: %+v", overview)
	}
}

func TestBillingSyncStatusDefaultsToOneDailyQuery(t *testing.T) {
	s := billStore(t)
	now := time.Now()
	if err := s.recordSyncRun(now.Unix(), now.Format("200601"), "sync", 0, 0, 0, 0, 0, 0, now.Unix(), "ok", "test"); err != nil {
		t.Fatal(err)
	}
	status, err := s.BillingSyncStatus(now)
	if err != nil || status.IntervalHours != 24 || status.Mode != "standard_export_daily" {
		t.Fatalf("wrong daily default: %+v, %v", status, err)
	}
	if status.MaxBytesPerQuery != 67108864 || status.MaxMonthlyBytes != 4160749568 {
		t.Fatalf("changing cadence must not alter query budgets: %+v", status)
	}
}
