package trafficreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DescStandardInternetEgressOregon = "Network Standard Data Transfer Out to Internet from Oregon"
	DescInterZoneEgress              = "Network Inter Zone Data Transfer Out"
	DescInterRegionLasVegas          = "Network Inter Region Data Transfer Out from Americas to Las Vegas"
	DescInterRegionVirginia          = "Network Inter Region Data Transfer Out from Americas to Virginia"
	DescCarrierPeeringAmericas       = "Network Data Transfer Out via Carrier Peering Network - Americas Based"

	SKUCarrierPeeringAmericas       = "984A-1F27-2D1F"
	SKUInterZoneEgress              = "DE9E-AFBC-A15A"
	SKUInterRegionLasVegas          = "ACC0-9F23-6029"
	SKUInterRegionVirginia          = "C863-37DA-506E"
	SKUStandardInternetEgressOregon = "4986-FDE4-A69A"

	DefaultSyncIntervalHours       = 12
	DefaultLookbackDays            = 7
	DefaultMaxBytesPerQuery  int64 = 64 * 1024 * 1024        // 64 MiB per query
	DefaultMaxMonthlyBytes   int64 = 62 * 64 * 1024 * 1024   // 3.875 GiB/month (62 * 64 MiB, ~$0.0237 at $6.25/TiB)
	DefaultMaxStorageBytes   int64 = 10 * 1024 * 1024 * 1024 // 10 GiB BigQuery storage guardrail
	HardMaxMonthlyCostBytes  int64 = 8 * 1024 * 1024 * 1024  // < $0.05/month hard ceiling at $6.25/TiB
	StandardTierQuotaGiB           = "200"

	BillingAccountingNote = "Billing description is the primary identifier; SKU ID is secondary metadata. Totals cover the selected billing window at project level, not an individual VM. Mixed-currency monetary totals are unavailable; inspect the original per-currency bill rows. Only Oregon Standard Internet egress contributes to the 200 GiB account-wide reference."
	BillingEstimationNote = "Aggregated total (targetTotalUsageGiB) sums all 5 tracked network items for overall traffic estimation; itemized buckets keep Standard Tier Internet egress separate from Carrier Peering, Inter-zone, and Inter-region traffic so free-quota and billed amounts remain accurate."
	BillingLagNotice      = "Google Cloud Standard usage cost export data can be delayed by more than 24 hours, and initial backfill can take up to 5 days. Missing or pending windows are never treated as $0."
)

type TargetSKUSpec struct {
	SKUID                      string
	Description                string
	Category                   string
	Handling                   string
	CountsTowardStandard200GiB bool
}

var orderedTargetSKUs = []TargetSKUSpec{
	{
		SKUID:                      SKUStandardInternetEgressOregon,
		Description:                DescStandardInternetEgressOregon,
		Category:                   "standard_internet_egress",
		Handling:                   "target_vm_standard_tier",
		CountsTowardStandard200GiB: true,
	},
	{
		SKUID:                      SKUInterZoneEgress,
		Description:                DescInterZoneEgress,
		Category:                   "inter_zone_egress",
		Handling:                   "separate_reference",
		CountsTowardStandard200GiB: false,
	},
	{
		SKUID:                      SKUInterRegionLasVegas,
		Description:                DescInterRegionLasVegas,
		Category:                   "inter_region_las_vegas",
		Handling:                   "separate_reference",
		CountsTowardStandard200GiB: false,
	},
	{
		SKUID:                      SKUInterRegionVirginia,
		Description:                DescInterRegionVirginia,
		Category:                   "inter_region_virginia",
		Handling:                   "separate_reference",
		CountsTowardStandard200GiB: false,
	},
	{
		SKUID:                      SKUCarrierPeeringAmericas,
		Description:                DescCarrierPeeringAmericas,
		Category:                   "carrier_peering_egress",
		Handling:                   "billed_carrier_peering",
		CountsTowardStandard200GiB: false,
	},
}

func TargetSKUSpecs() []TargetSKUSpec {
	out := make([]TargetSKUSpec, len(orderedTargetSKUs))
	copy(out, orderedTargetSKUs)
	return out
}

// ClassifyBillingItem prioritizes the human-readable billing description as the
// primary semantic identifier, falling back to SKU ID only when the description
// is omitted or abbreviated.
func ClassifyBillingItem(skuID, description string) TargetSKUSpec {
	id := strings.TrimSpace(skuID)
	desc := strings.TrimSpace(description)
	lowerDesc := strings.ToLower(desc)

	if lowerDesc != "" {
		for _, spec := range orderedTargetSKUs {
			if strings.EqualFold(desc, spec.Description) {
				if id != "" {
					spec.SKUID = id
				}
				return spec
			}
		}
		switch {
		case strings.Contains(lowerDesc, "carrier peering"):
			spec := orderedTargetSKUs[4]
			if id != "" {
				spec.SKUID = id
			}
			return spec
		case strings.Contains(lowerDesc, "inter zone") || strings.Contains(lowerDesc, "inter-zone"):
			spec := orderedTargetSKUs[1]
			if id != "" {
				spec.SKUID = id
			}
			return spec
		case (strings.Contains(lowerDesc, "inter region") || strings.Contains(lowerDesc, "inter-region")) && strings.Contains(lowerDesc, "las vegas"):
			spec := orderedTargetSKUs[2]
			if id != "" {
				spec.SKUID = id
			}
			return spec
		case (strings.Contains(lowerDesc, "inter region") || strings.Contains(lowerDesc, "inter-region")) && strings.Contains(lowerDesc, "virginia"):
			spec := orderedTargetSKUs[3]
			if id != "" {
				spec.SKUID = id
			}
			return spec
		case (strings.Contains(lowerDesc, "standard data transfer out") || strings.Contains(lowerDesc, "standard internet egress")) && strings.Contains(lowerDesc, "oregon"):
			spec := orderedTargetSKUs[0]
			if id != "" {
				spec.SKUID = id
			}
			return spec
		}
	}

	for _, spec := range orderedTargetSKUs {
		if spec.SKUID == id {
			return spec
		}
	}
	if desc == "" {
		desc = "Non-target billing item (manual reference)"
	}
	return TargetSKUSpec{
		SKUID:                      id,
		Description:                desc,
		Category:                   "other_sku",
		Handling:                   "untracked_reference",
		CountsTowardStandard200GiB: false,
	}
}

func ClassifySKU(skuID string) TargetSKUSpec {
	return ClassifyBillingItem(skuID, "")
}

func IsAllowedTargetBillingItem(skuID, description string) bool {
	spec := ClassifyBillingItem(skuID, description)
	return spec.Category != "other_sku"
}

func IsAllowedTargetSKU(skuID string) bool {
	return IsAllowedTargetBillingItem(skuID, "")
}

func enrichBillSKU(b *Bill) {
	spec := ClassifyBillingItem(b.SKUID, b.Description)
	if strings.TrimSpace(b.Description) == "" || strings.EqualFold(b.Description, b.SKUID) {
		b.Description = spec.Description
	}
	if strings.TrimSpace(b.SKUID) == "" && spec.SKUID != "" {
		b.SKUID = spec.SKUID
	}
	b.Category = spec.Category
	b.AccountingTreatment = spec.Handling
	b.CountsTowardStandard200GiB = spec.CountsTowardStandard200GiB
	if b.Credits == "" {
		b.Credits = "0"
	}
	if b.NetCost == "" {
		b.NetCost = addDecimalStrings(b.Cost, b.Credits)
	}
}

func parseRatOrZero(v string) *big.Rat {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(v))
	if !ok || r == nil {
		return new(big.Rat)
	}
	return r
}

func formatRatDecimal(r *big.Rat, maxScale int) string {
	if r == nil || r.Sign() == 0 {
		return "0"
	}
	if maxScale <= 0 {
		maxScale = 12
	}
	s := r.FloatString(maxScale)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func addDecimalStrings(a, b string) string {
	sum := new(big.Rat).Add(parseRatOrZero(a), parseRatOrZero(b))
	return formatRatDecimal(sum, 12)
}

func subDecimalStrings(a, b string) string {
	diff := new(big.Rat).Sub(parseRatOrZero(a), parseRatOrZero(b))
	return formatRatDecimal(diff, 12)
}

func normalizeUsageToGiB(amountStr, unit string) (string, error) {
	amountStr = strings.TrimSpace(amountStr)
	if !decimal.MatchString(amountStr) {
		return "", fmt.Errorf("usage amount %q is not finite decimal text", amountStr)
	}
	amt := parseRatOrZero(amountStr)
	// Standard exports include signed usage rows for billing corrections.
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "gib", "gibibyte", "gibibytes", "gbyte":
		return formatRatDecimal(amt, 12), nil
	case "byte", "bytes":
		gib := new(big.Rat).Quo(amt, big.NewRat(1<<30, 1))
		return formatRatDecimal(gib, 12), nil
	default:
		return "", fmt.Errorf("unsupported usage unit %q", unit)
	}
}

func estimateQueryCostUSD(bytesScanned int64) *big.Rat {
	if bytesScanned <= 0 {
		return new(big.Rat)
	}
	// Public BigQuery on-demand rate: $6.25 per TiB (1 TiB = 2^40 bytes = 1,099,511,627,776 bytes).
	num := new(big.Int).Mul(big.NewInt(bytesScanned), big.NewInt(25))
	den := new(big.Int).Mul(big.NewInt(4), new(big.Int).Lsh(big.NewInt(1), 40))
	return new(big.Rat).SetFrac(num, den)
}

// SummarizeBills computes both the 5-item separated breakdown (keyed by
// human-readable billing description / category) AND the aggregated grand
// totals for convenient traffic & cost estimation.
func SummarizeBills(bills []Bill, syncStatus BillingSyncStatus) BillingOverview {
	type acc struct {
		spec     TargetSKUSpec
		usage    *big.Rat
		gross    *big.Rat
		credits  *big.Rat
		net      *big.Rat
		currency string
		count    int
	}
	byCategory := make(map[string]*acc, len(orderedTargetSKUs))
	for _, spec := range orderedTargetSKUs {
		byCategory[spec.Category] = &acc{
			spec:     spec,
			usage:    new(big.Rat),
			gross:    new(big.Rat),
			credits:  new(big.Rat),
			net:      new(big.Rat),
			currency: "USD",
		}
	}
	otherCount := 0
	allUsage := new(big.Rat)
	allNet := new(big.Rat)
	currencies := map[string]bool{}
	targetCurrencies := map[string]bool{}

	for _, b := range bills {
		u := parseRatOrZero(b.UsageGiB)
		g := parseRatOrZero(b.Cost)
		c := parseRatOrZero(b.Credits)
		var n *big.Rat
		if b.NetCost != "" {
			n = parseRatOrZero(b.NetCost)
		} else {
			n = new(big.Rat).Add(g, c)
		}
		allUsage.Add(allUsage, u)
		allNet.Add(allNet, n)
		if b.Currency != "" {
			currencies[b.Currency] = true
		}

		spec := ClassifyBillingItem(b.SKUID, b.Description)
		bucket, ok := byCategory[spec.Category]
		if !ok {
			otherCount++
			continue
		}
		bucket.usage.Add(bucket.usage, u)
		bucket.gross.Add(bucket.gross, g)
		bucket.credits.Add(bucket.credits, c)
		bucket.net.Add(bucket.net, n)
		if b.Currency != "" {
			targetCurrencies[b.Currency] = true
			if bucket.count > 0 && bucket.currency != b.Currency {
				bucket.currency = "MIXED"
			} else if bucket.count == 0 {
				bucket.currency = b.Currency
			}
		}
		bucket.count++
	}

	targetBuckets := make([]SKUBillingBucket, 0, len(orderedTargetSKUs))
	var carrierBucket SKUBillingBucket
	standardUsed := new(big.Rat)
	excludedFromStandard := new(big.Rat)
	internalRefUsed := new(big.Rat)
	targetTotalUsage := new(big.Rat)
	targetGrossTotal := new(big.Rat)
	targetCreditsTotal := new(big.Rat)
	targetNetTotal := new(big.Rat)

	for _, spec := range orderedTargetSKUs {
		a := byCategory[spec.Category]
		item := SKUBillingBucket{
			SKUID:                      spec.SKUID,
			Description:                spec.Description,
			Category:                   spec.Category,
			Handling:                   spec.Handling,
			CountsTowardStandard200GiB: spec.CountsTowardStandard200GiB,
			UsageGiB:                   formatRatDecimal(a.usage, 12),
			GrossCost:                  formatRatDecimal(a.gross, 12),
			Credits:                    formatRatDecimal(a.credits, 12),
			NetCost:                    formatRatDecimal(a.net, 12),
			Currency:                   a.currency,
			RowCount:                   a.count,
		}
		if a.currency == "MIXED" {
			item.GrossCost, item.Credits, item.NetCost = "", "", ""
		}
		targetBuckets = append(targetBuckets, item)
		targetTotalUsage.Add(targetTotalUsage, a.usage)
		targetGrossTotal.Add(targetGrossTotal, a.gross)
		targetCreditsTotal.Add(targetCreditsTotal, a.credits)
		targetNetTotal.Add(targetNetTotal, a.net)

		if spec.Category == "carrier_peering_egress" {
			carrierBucket = item
		}
		if spec.CountsTowardStandard200GiB {
			standardUsed.Add(standardUsed, a.usage)
		} else {
			excludedFromStandard.Add(excludedFromStandard, a.usage)
			if spec.Category != "carrier_peering_egress" {
				internalRefUsed.Add(internalRefUsed, a.usage)
			}
		}
	}

	quotaRat := parseRatOrZero(StandardTierQuotaGiB)
	remaining := new(big.Rat).Sub(quotaRat, standardUsed)
	if remaining.Sign() < 0 {
		remaining = new(big.Rat)
	}

	overview := BillingOverview{
		TargetSKUs: targetBuckets,
		Totals: BillingTotals{
			TargetTotalUsageGiB:       formatRatDecimal(targetTotalUsage, 12),
			StandardFreeTierUsageGiB:  formatRatDecimal(standardUsed, 12),
			NonStandardUsageGiB:       formatRatDecimal(excludedFromStandard, 12),
			CarrierPeeringUsageGiB:    carrierBucket.UsageGiB,
			InternalReferenceUsageGiB: formatRatDecimal(internalRefUsed, 12),
			TargetGrossCost:           formatRatDecimal(targetGrossTotal, 12),
			TargetCredits:             formatRatDecimal(targetCreditsTotal, 12),
			TargetNetCost:             formatRatDecimal(targetNetTotal, 12),
			AllImportedUsageGiB:       formatRatDecimal(allUsage, 12),
			AllImportedNetCost:        formatRatDecimal(allNet, 12),
			Currency:                  billingCurrency(targetCurrencies),
			EstimationNote:            BillingEstimationNote,
		},
		StandardTierReference: StandardTierQuotaReference{
			ReferenceLimitGiB:       StandardTierQuotaGiB,
			TargetVMDescription:     DescStandardInternetEgressOregon,
			TargetVMEgressSKU:       SKUStandardInternetEgressOregon,
			TargetVMUsedGiB:         formatRatDecimal(standardUsed, 12),
			TargetVMRemainingRefGiB: formatRatDecimal(remaining, 12),
			ExcludedTargetSKUsGiB:   formatRatDecimal(excludedFromStandard, 12),
			QuotaScope:              "billing_account_wide",
			MeasuredScope:           "target_oregon_vm_sku_only",
			AccountWideComplete:     false,
			Note:                    "200 GiB/month is a billing-account-level quota across all projects and regions. Only 'Network Standard Data Transfer Out to Internet from Oregon' counts toward this reference; Inter-zone, Inter-region (Las Vegas / Virginia), and Carrier Peering are excluded from the 200 GiB pool but included in the overall traffic total (targetTotalUsageGiB) for volume estimation.",
		},
		CarrierPeeringActual: carrierBucket,
		OtherSKUsCount:       otherCount,
		SyncStatus:           syncStatus,
		AccountingNote:       BillingAccountingNote,
	}
	// Monetary amounts in different currencies cannot be summed without an
	// exchange rate. Keep usage totals, suppress money totals, retain raw bills.
	if len(currencies) > 1 {
		overview.Totals.AllImportedNetCost = ""
	}
	if len(targetCurrencies) > 1 {
		overview.Totals.TargetGrossCost = ""
		overview.Totals.TargetCredits = ""
		overview.Totals.TargetNetCost = ""
	}
	return overview
}

func billingCurrency(currencies map[string]bool) string {
	if len(currencies) > 1 {
		return "MIXED"
	}
	for currency := range currencies {
		return currency
	}
	return "USD"
}

func (s *Store) BillingOverview(r Range, now time.Time) (BillingOverview, error) {
	bills, err := s.allBills(r)
	if err != nil {
		return BillingOverview{}, err
	}
	status, err := s.BillingSyncStatus(now)
	if err != nil {
		return BillingOverview{}, err
	}
	if len(bills) == 0 {
		status.DataPending = true
	}
	overview := SummarizeBills(bills, status)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return BillingOverview{}, err
	}
	day := time.Unix(r.To-1, 0).In(loc)
	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, loc)
	monthBills, err := s.allBills(Range{From: monthStart.Unix(), To: monthStart.AddDate(0, 1, 0).Unix()})
	if err != nil {
		return BillingOverview{}, err
	}
	overview.StandardTierReference = SummarizeBills(monthBills, status).StandardTierReference
	overview.StandardTierReference.MeasuredScope = "stored_project_oregon_month_only"
	overview.StandardTierReference.Note = fmt.Sprintf("%s: stored project-level Oregon Standard egress for this Pacific calendar month only. Standard export cannot isolate a VM. The 200 GiB allowance is shared across the billing account; incomplete exports and other projects/regions mean this reference is not the account's actual remaining quota.", monthStart.Format("2006-01"))
	return overview, nil
}

func (s *Store) BillingSyncStatus(now time.Time) (BillingSyncStatus, error) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return BillingSyncStatus{}, err
	}
	month := now.In(loc).Format("200601")
	status := BillingSyncStatus{
		Enabled:               false,
		Mode:                  "manual_csv_fallback",
		IntervalHours:         DefaultSyncIntervalHours,
		LastStatus:            "pending_initial_sync",
		DataPending:           true,
		LagNotice:             BillingLagNotice,
		MaxBytesPerQuery:      DefaultMaxBytesPerQuery,
		MaxMonthlyBytes:       DefaultMaxMonthlyBytes,
		EstimatedMonthlyUSD:   "0",
		MonthlyBudgetLimitUSD: "0.05",
	}
	var scanned int64
	if err := s.db.QueryRow("SELECT COALESCE(SUM(MAX(estimated_bytes,billed_bytes)),0) FROM review_billing_sync_runs WHERE month=? AND mode='sync'", month).Scan(&scanned); err != nil {
		return BillingSyncStatus{}, err
	}
	status.MonthlyScannedBytes = scanned
	status.EstimatedMonthlyUSD = formatRatDecimal(estimateQueryCostUSD(scanned), 6)

	var paused string
	if s.db.QueryRow("SELECT value FROM review_meta WHERE key='billing_budget_paused'").Scan(&paused) == nil && paused == "true" {
		status.BudgetPaused = true
	}
	var lastAt, lastExport sql.NullInt64
	var lastMode, lastRunStatus sql.NullString
	err = s.db.QueryRow("SELECT at,mode,max_export_time,status FROM review_billing_sync_runs ORDER BY id DESC LIMIT 1").Scan(&lastAt, &lastMode, &lastExport, &lastRunStatus)
	if err == nil && lastAt.Valid {
		status.Enabled = lastMode.String != "mock"
		status.Mode = "standard_export_12h"
		if lastMode.String == "mock" {
			status.Mode = "offline_snapshot"
		}
		status.LastSyncAt = lastAt.Int64
		status.LastExportTime = lastExport.Int64
		status.LastStatus = lastRunStatus.String
	} else {
		var csvSyncAt, maxExport sql.NullInt64
		_ = s.db.QueryRow("SELECT MAX(synced_at),MAX(export_time) FROM review_bills").Scan(&csvSyncAt, &maxExport)
		if csvSyncAt.Valid && csvSyncAt.Int64 > 0 {
			status.LastSyncAt = csvSyncAt.Int64
			status.LastExportTime = maxExport.Int64
			status.LastStatus = "csv_imported"
		}
	}
	if (status.LastStatus == "ok" || status.LastStatus == "unchanged" || status.LastStatus == "corrected") && status.LastSyncAt > 0 && status.LastExportTime > 0 && now.Unix()-status.LastSyncAt <= 24*3600 && now.Unix()-status.LastExportTime <= 24*3600 && !status.BudgetPaused {
		status.DataPending = false
	}
	return status, nil
}

var (
	gcpProjectRe = regexp.MustCompile(`^[a-z][a-z0-9-]{4,29}$`)
	gcpDatasetRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)
	stdTableRe   = regexp.MustCompile(`^gcp_billing_export_v1_[A-Za-z0-9_]{6,64}$`)
	invoiceRe    = regexp.MustCompile(`^[0-9]{6}$`)
)

type BillingSyncConfig struct {
	QueryProjectID              string
	DatasetProjectID            string
	DatasetID                   string
	TableID                     string
	TargetProjectID             string
	LookbackDays                int
	MaxBytesPerQuery            int64
	MaxMonthlyBytes             int64
	MaxDatasetStorageBytes      int64
	DatasetStorageBytes         int64
	ExistingAccountMonthlyBytes int64
	DryRunOnly                  bool
	UseADC                      bool
	ServiceAccountKeyJSON       string
	CredentialsFile             string
	Now                         time.Time
}

func (c *BillingSyncConfig) applyDefaults() {
	if c.DatasetProjectID == "" {
		c.DatasetProjectID = c.QueryProjectID
	}
	if c.LookbackDays <= 0 {
		c.LookbackDays = DefaultLookbackDays
	}
	if c.MaxBytesPerQuery <= 0 {
		c.MaxBytesPerQuery = DefaultMaxBytesPerQuery
	}
	if c.MaxMonthlyBytes <= 0 {
		c.MaxMonthlyBytes = DefaultMaxMonthlyBytes
	}
	if c.MaxDatasetStorageBytes <= 0 {
		c.MaxDatasetStorageBytes = DefaultMaxStorageBytes
	}
	if c.Now.IsZero() {
		c.Now = time.Now()
	}
}

func ValidateBillingSyncConfig(c BillingSyncConfig) (BillingSyncConfig, error) {
	c.applyDefaults()
	if strings.TrimSpace(c.ServiceAccountKeyJSON) != "" || strings.TrimSpace(c.CredentialsFile) != "" {
		return c, errors.New("refusing long-lived service account key; use VM ADC with roles/bigquery.jobUser and roles/bigquery.dataViewer")
	}
	if !c.UseADC {
		return c, errors.New("ADC authentication is required for BigQuery billing sync")
	}
	if !gcpProjectRe.MatchString(c.QueryProjectID) || !gcpProjectRe.MatchString(c.DatasetProjectID) {
		return c, errors.New("invalid GCP project ID")
	}
	if c.TargetProjectID != "" && !gcpProjectRe.MatchString(c.TargetProjectID) {
		return c, errors.New("invalid target project ID")
	}
	if !gcpDatasetRe.MatchString(c.DatasetID) {
		return c, errors.New("invalid BigQuery dataset ID")
	}
	lowerTable := strings.ToLower(strings.TrimSpace(c.TableID))
	if strings.Contains(lowerTable, "resource_v1") || strings.Contains(lowerTable, "pricing") || strings.Contains(lowerTable, "focus") {
		return c, errors.New("forbidden billing export table: only Standard usage cost export (gcp_billing_export_v1_<BILLING_ACCOUNT_ID>) is allowed")
	}
	if !stdTableRe.MatchString(c.TableID) {
		return c, errors.New("table must be Standard usage cost export gcp_billing_export_v1_<BILLING_ACCOUNT_ID>")
	}
	if c.LookbackDays < 1 || c.LookbackDays > 31 {
		return c, errors.New("lookback days must be between 1 and 31")
	}
	if c.MaxBytesPerQuery > DefaultMaxBytesPerQuery {
		return c, fmt.Errorf("maxBytesPerQuery %d exceeds 64 MiB per-query guardrail (%d)", c.MaxBytesPerQuery, DefaultMaxBytesPerQuery)
	}
	if c.MaxMonthlyBytes > HardMaxMonthlyCostBytes {
		return c, fmt.Errorf("maxMonthlyBytes %d exceeds the <$0.05/month guardrail", c.MaxMonthlyBytes)
	}
	if c.ExistingAccountMonthlyBytes < 0 {
		return c, errors.New("existing monthly bytes must not be negative")
	}
	limitUSD, _ := new(big.Rat).SetString("0.05")
	if estimateQueryCostUSD(c.MaxMonthlyBytes).Cmp(limitUSD) >= 0 {
		return c, errors.New("configured monthly scan budget could reach or exceed $0.05/month")
	}
	return c, nil
}

// BuildStandardExportQuery constructs a bounded BigQuery Standard SQL query
// matching the 5 target billing descriptions (and SKU IDs as fallback).
func BuildStandardExportQuery(c BillingSyncConfig) (string, error) {
	cfg, err := ValidateBillingSyncConfig(c)
	if err != nil {
		return "", err
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return "", err
	}
	endLA := cfg.Now.In(loc)
	// Rows replace daily totals. Always re-query the whole first billing day,
	// including across Pacific daylight-saving transitions.
	startLA := time.Date(endLA.Year(), endLA.Month(), endLA.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -cfg.LookbackDays)
	descLiterals := make([]string, 0, len(orderedTargetSKUs))
	skuLiterals := make([]string, 0, len(orderedTargetSKUs))
	for _, spec := range orderedTargetSKUs {
		descLiterals = append(descLiterals, "'"+spec.Description+"'")
		skuLiterals = append(skuLiterals, "'"+spec.SKUID+"'")
	}
	projectFilter := ""
	if cfg.TargetProjectID != "" {
		projectFilter = fmt.Sprintf("\n  AND project.id = '%s'", cfg.TargetProjectID)
	}
	tableRef := fmt.Sprintf("`%s.%s.%s`", cfg.DatasetProjectID, cfg.DatasetID, cfg.TableID)
	query := fmt.Sprintf(`SELECT
  FORMAT_TIMESTAMP('%%Y-%%m-%%d', usage_start_time, 'America/Los_Angeles') AS billing_date,
  IFNULL(project.id, 'unassigned') AS project_id,
  IFNULL(sku.id, '') AS sku_id,
  sku.description AS description,
  IFNULL(invoice.month, FORMAT_TIMESTAMP('%%Y%%m', usage_start_time, 'America/Los_Angeles')) AS invoice_month,
  usage.unit AS usage_unit,
  CAST(SUM(CAST(usage.amount AS NUMERIC)) AS STRING) AS usage_amount,
  CAST(SUM(CAST(cost AS NUMERIC)) AS STRING) AS gross_cost,
  CAST(SUM(IFNULL((SELECT SUM(CAST(cr.amount AS NUMERIC)) FROM UNNEST(credits) AS cr), 0)) AS STRING) AS credits_amount,
  currency AS currency,
  CAST(UNIX_SECONDS(MAX(export_time)) AS INT64) AS max_export_time
FROM %s
WHERE _PARTITIONTIME >= TIMESTAMP('%s')
  AND usage_start_time >= TIMESTAMP('%s')
  AND usage_start_time < TIMESTAMP('%s')
  AND (sku.description IN (%s) OR sku.id IN (%s))%s
GROUP BY billing_date, project_id, sku_id, description, invoice_month, usage_unit, currency
ORDER BY billing_date DESC, description`,
		tableRef,
		startLA.UTC().Truncate(24*time.Hour).Format(time.RFC3339),
		startLA.UTC().Format(time.RFC3339),
		endLA.UTC().Format(time.RFC3339),
		strings.Join(descLiterals, ", "),
		strings.Join(skuLiterals, ", "),
		projectFilter,
	)
	return query, nil
}

type StandardExportRow struct {
	Date         string `json:"date"`
	ProjectID    string `json:"projectID"`
	SKUID        string `json:"skuID"`
	Description  string `json:"description"`
	InvoiceMonth string `json:"invoiceMonth"`
	UsageAmount  string `json:"usageAmount"`
	UsageUnit    string `json:"usageUnit"`
	Cost         string `json:"cost"`
	Credits      string `json:"credits"`
	Currency     string `json:"currency"`
	ExportTime   int64  `json:"exportTime"`
}

type BigQueryDryRunResult struct {
	EstimatedBytes              int64
	DatasetStorageBytes         int64
	ExistingAccountMonthlyBytes int64
}

type BigQueryQueryResult struct {
	BilledBytes int64
	Rows        []StandardExportRow
}

type BigQueryRunner interface {
	DryRun(ctx context.Context, query string, cfg BillingSyncConfig) (BigQueryDryRunResult, error)
	Query(ctx context.Context, query string, cfg BillingSyncConfig) (BigQueryQueryResult, error)
}

type BillingSyncReport struct {
	Mode                string `json:"mode"`
	DryRunEstimatedByte int64  `json:"dryRunEstimatedBytes"`
	BilledBytes         int64  `json:"billedBytes"`
	MonthlyScannedBytes int64  `json:"monthlyScannedBytes"`
	EstimatedMonthlyUSD string `json:"estimatedMonthlyUSD"`
	DatasetStorageBytes int64  `json:"datasetStorageBytes"`
	Inserted            int    `json:"inserted"`
	Corrected           int    `json:"corrected"`
	Unchanged           int    `json:"unchanged"`
	StaleIgnored        int    `json:"staleIgnored"`
	MaxExportTime       int64  `json:"maxExportTime"`
	Status              string `json:"status"`
}

func (s *Store) recordSyncRun(at int64, month, mode string, estBytes, billedBytes, storageBytes, existingBytes int64, upserted, corrected int, maxExport int64, status, msg string) error {
	_, err := s.db.Exec(
		"INSERT INTO review_billing_sync_runs(at,month,mode,estimated_bytes,billed_bytes,dataset_storage_bytes,existing_monthly_bytes,rows_upserted,rows_corrected,max_export_time,status,message) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
		at, month, mode, estBytes, billedBytes, storageBytes, existingBytes, upserted, corrected, maxExport, clip(status, 64), clip(msg, 512),
	)
	return err
}

func (s *Store) pauseBudget(at int64, month, mode string, estBytes, storageBytes, existingBytes int64, reason string) error {
	_ = s.recordSyncRun(at, month, mode, estBytes, 0, storageBytes, existingBytes, 0, 0, 0, "rejected_budget", reason)
	_, _ = s.db.Exec("INSERT INTO review_meta(key,value) VALUES('billing_budget_paused','true') ON CONFLICT(key) DO UPDATE SET value='true'")
	return s.Alert(at, "billing_budget_exceeded", reason)
}

// SyncStandardBilling enforces preflight storage, existing BigQuery usage,
// dry-run scan limits (64 MiB/query), and monthly scan limits (3.875 GiB/mo,
// < $0.05/mo) before querying only the 5 allowed target items.
func (s *Store) SyncStandardBilling(ctx context.Context, cfg BillingSyncConfig, runner BigQueryRunner) (BillingSyncReport, error) {
	validCfg, err := ValidateBillingSyncConfig(cfg)
	if err != nil {
		return BillingSyncReport{}, err
	}
	if runner == nil {
		return BillingSyncReport{}, errors.New("BigQuery runner is required")
	}
	query, err := BuildStandardExportQuery(validCfg)
	if err != nil {
		return BillingSyncReport{}, err
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return BillingSyncReport{}, err
	}
	nowUnix := validCfg.Now.Unix()
	month := validCfg.Now.In(loc).Format("200601")

	dry, err := runner.DryRun(ctx, query, validCfg)
	if err != nil {
		_ = s.recordSyncRun(nowUnix, month, "dry_run", 0, 0, 0, 0, 0, 0, 0, "error", err.Error())
		return BillingSyncReport{}, err
	}
	storageBytes := dry.DatasetStorageBytes
	if validCfg.DatasetStorageBytes > storageBytes {
		storageBytes = validCfg.DatasetStorageBytes
	}
	existingMonthly := dry.ExistingAccountMonthlyBytes
	if validCfg.ExistingAccountMonthlyBytes > existingMonthly {
		existingMonthly = validCfg.ExistingAccountMonthlyBytes
	}
	if storageBytes < 0 || existingMonthly < 0 {
		return BillingSyncReport{}, errors.New("invalid negative BigQuery storage or existing usage statistics")
	}

	var localMonthly int64
	if err := s.db.QueryRow("SELECT COALESCE(SUM(MAX(estimated_bytes,billed_bytes)),0) FROM review_billing_sync_runs WHERE month=? AND mode='sync'", month).Scan(&localMonthly); err != nil {
		return BillingSyncReport{}, err
	}

	if storageBytes > validCfg.MaxDatasetStorageBytes {
		msg := fmt.Sprintf("BigQuery dataset storage %d bytes exceeds limit %d bytes; sync paused without increasing budget", storageBytes, validCfg.MaxDatasetStorageBytes)
		_ = s.pauseBudget(nowUnix, month, "dry_run", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		return BillingSyncReport{Status: "rejected_budget", DatasetStorageBytes: storageBytes}, errors.New(msg)
	}
	if dry.EstimatedBytes < 0 || dry.EstimatedBytes > validCfg.MaxBytesPerQuery {
		msg := fmt.Sprintf("BigQuery dry run estimated %d bytes, exceeding per-query limit %d bytes (64 MiB); sync paused without increasing budget", dry.EstimatedBytes, validCfg.MaxBytesPerQuery)
		_ = s.pauseBudget(nowUnix, month, "dry_run", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		return BillingSyncReport{Status: "rejected_budget", DryRunEstimatedByte: dry.EstimatedBytes, DatasetStorageBytes: storageBytes}, errors.New(msg)
	}
	if existingMonthly > validCfg.MaxMonthlyBytes {
		msg := "Existing BigQuery usage exceeds configured monthly query budget"
		_ = s.pauseBudget(nowUnix, month, "dry_run", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		return BillingSyncReport{Status: "rejected_budget"}, errors.New(msg)
	}
	// Reserve the maximum permitted charge, not just a dry-run estimate. The
	// table may grow before execution and BigQuery also has minimum billing.
	projectedMonthly := localMonthly + existingMonthly + validCfg.MaxBytesPerQuery
	if projectedMonthly > validCfg.MaxMonthlyBytes {
		msg := fmt.Sprintf("Projected monthly BigQuery scan %d bytes (local=%d, existing=%d, dry_run=%d) exceeds monthly limit %d bytes (<$0.05/mo); sync paused", projectedMonthly, localMonthly, existingMonthly, dry.EstimatedBytes, validCfg.MaxMonthlyBytes)
		_ = s.pauseBudget(nowUnix, month, "dry_run", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		return BillingSyncReport{Status: "rejected_budget", DryRunEstimatedByte: dry.EstimatedBytes, MonthlyScannedBytes: projectedMonthly, DatasetStorageBytes: storageBytes}, errors.New(msg)
	}
	limitUSD, _ := new(big.Rat).SetString("0.05")
	if estimateQueryCostUSD(projectedMonthly).Cmp(limitUSD) >= 0 {
		msg := fmt.Sprintf("Projected monthly BigQuery cost $%s reaches or exceeds $0.05/month; sync paused", formatRatDecimal(estimateQueryCostUSD(projectedMonthly), 6))
		_ = s.pauseBudget(nowUnix, month, "dry_run", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		return BillingSyncReport{Status: "rejected_budget", DryRunEstimatedByte: dry.EstimatedBytes, MonthlyScannedBytes: projectedMonthly}, errors.New(msg)
	}

	if validCfg.DryRunOnly {
		_ = s.recordSyncRun(nowUnix, month, "dry_run", dry.EstimatedBytes, 0, storageBytes, existingMonthly, 0, 0, 0, "dry_run_ok", "Dry run within 64 MiB and <$0.05/month budget")
		return BillingSyncReport{
			Mode:                "dry_run",
			DryRunEstimatedByte: dry.EstimatedBytes,
			MonthlyScannedBytes: localMonthly + existingMonthly,
			EstimatedMonthlyUSD: formatRatDecimal(estimateQueryCostUSD(localMonthly+existingMonthly+dry.EstimatedBytes), 6),
			DatasetStorageBytes: storageBytes,
			Status:              "dry_run_ok",
		}, nil
	}

	runID, localMonthly, err := s.reserveBillingQuery(nowUnix, month, validCfg, storageBytes, existingMonthly)
	if err != nil {
		return BillingSyncReport{}, err
	}
	finishRun := func(estimated, billed int64, report BillingSyncReport, status, message string) error {
		_, err := s.db.Exec("UPDATE review_billing_sync_runs SET estimated_bytes=?,billed_bytes=?,rows_upserted=?,rows_corrected=?,max_export_time=?,status=?,message=? WHERE id=?", estimated, billed, report.Inserted+report.Corrected, report.Corrected, report.MaxExportTime, status, clip(message, 512), runID)
		return err
	}
	res, err := runner.Query(ctx, query, validCfg)
	if err != nil {
		// A timeout or transport failure does not prove the server did not bill.
		_ = finishRun(validCfg.MaxBytesPerQuery, res.BilledBytes, BillingSyncReport{}, "error", err.Error())
		return BillingSyncReport{}, err
	}
	if res.BilledBytes > validCfg.MaxBytesPerQuery {
		msg := fmt.Sprintf("BigQuery billed %d bytes exceeding maximumBytesBilled %d", res.BilledBytes, validCfg.MaxBytesPerQuery)
		_ = s.pauseBudget(nowUnix, month, "sync", dry.EstimatedBytes, storageBytes, existingMonthly, msg)
		_ = finishRun(validCfg.MaxBytesPerQuery, res.BilledBytes, BillingSyncReport{}, "rejected_budget", msg)
		return BillingSyncReport{Status: "rejected_budget", BilledBytes: res.BilledBytes}, errors.New(msg)
	}
	// The ADC runner substitutes the reserved maximum when billing statistics
	// are absent; a reported zero means an actual zero-byte/cache-hit charge.
	scannedThisRun := res.BilledBytes
	if scannedThisRun < 0 {
		return BillingSyncReport{}, errors.New("invalid negative BigQuery billed bytes")
	}

	report, err := s.UpsertStandardExportRows(res.Rows, "bq_standard_export", validCfg.Now)
	if err != nil {
		_ = finishRun(scannedThisRun, res.BilledBytes, BillingSyncReport{}, "error", err.Error())
		return BillingSyncReport{}, err
	}
	totalMonthlyAfter := localMonthly + existingMonthly + scannedThisRun
	report.Mode = "sync"
	report.DryRunEstimatedByte = dry.EstimatedBytes
	report.BilledBytes = res.BilledBytes
	report.MonthlyScannedBytes = totalMonthlyAfter
	report.EstimatedMonthlyUSD = formatRatDecimal(estimateQueryCostUSD(totalMonthlyAfter), 6)
	report.DatasetStorageBytes = storageBytes

	_, _ = s.db.Exec("INSERT INTO review_meta(key,value) VALUES('billing_budget_paused','false') ON CONFLICT(key) DO UPDATE SET value='false'")
	if err := finishRun(scannedThisRun, res.BilledBytes, report, report.Status, fmt.Sprintf("Synced %d inserted, %d corrected, %d unchanged rows", report.Inserted, report.Corrected, report.Unchanged)); err != nil {
		return report, err
	}
	return report, nil
}

// The write at the start serializes reservations across collector/CLI processes
// sharing this SQLite file. A crash leaves its full reservation in the budget.
func (s *Store) reserveBillingQuery(at int64, month string, cfg BillingSyncConfig, storage, existing int64) (int64, int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := setMeta(tx, "billing_reservation_lock", month); err != nil {
		return 0, 0, err
	}
	var used int64
	if err := tx.QueryRow("SELECT COALESCE(SUM(MAX(estimated_bytes,billed_bytes)),0) FROM review_billing_sync_runs WHERE month=? AND mode='sync'", month).Scan(&used); err != nil {
		return 0, 0, err
	}
	if existing < 0 || used > cfg.MaxMonthlyBytes-cfg.MaxBytesPerQuery-existing {
		return 0, used, errors.New("monthly BigQuery budget has insufficient unreserved bytes")
	}
	res, err := tx.Exec("INSERT INTO review_billing_sync_runs(at,month,mode,estimated_bytes,billed_bytes,dataset_storage_bytes,existing_monthly_bytes,rows_upserted,rows_corrected,max_export_time,status,message) VALUES(?,?,'sync',?,0,?,?,0,0,0,'reserved','Query charge reserved before execution')", at, month, cfg.MaxBytesPerQuery, storage, existing)
	if err != nil {
		return 0, used, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, used, err
	}
	return id, used, tx.Commit()
}

// SyncStandardExportJSON loads simulated or exported Standard usage cost rows
// in JSON format for local verification without enabling live GCP export.
func (s *Store) SyncStandardExportJSON(reader io.Reader, now time.Time) (BillingSyncReport, error) {
	b, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if err != nil {
		return BillingSyncReport{}, err
	}
	if len(b) > 4<<20 {
		return BillingSyncReport{}, errors.New("standard export JSON exceeds 4 MiB")
	}
	var rows []StandardExportRow
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rows); err != nil {
		return BillingSyncReport{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	report, err := s.UpsertStandardExportRows(rows, "standard_export_json", now)
	if err != nil {
		return BillingSyncReport{}, err
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err == nil {
		month := now.In(loc).Format("200601")
		_ = s.recordSyncRun(now.Unix(), month, "mock", 0, 0, 0, 0, report.Inserted+report.Corrected, report.Corrected, report.MaxExportTime, report.Status, "Imported Standard usage export JSON snapshot")
	}
	report.Mode = "mock"
	return report, nil
}

// UpsertStandardExportRows idempotently merges Standard usage cost export rows
// for the 5 allowed target items (matched by description or SKU ID), handling
// duplicate polls, multi-row daily aggregation, late-arriving usage, and
// historical cost corrections.
func (s *Store) UpsertStandardExportRows(rows []StandardExportRow, source string, now time.Time) (BillingSyncReport, error) {
	if len(rows) > 10000 {
		return BillingSyncReport{}, errors.New("too many standard export rows")
	}
	if now.IsZero() {
		now = time.Now()
	}
	type aggRow struct {
		date         string
		projectID    string
		skuID        string
		description  string
		invoiceMonth string
		usageGiB     *big.Rat
		grossCost    *big.Rat
		credits      *big.Rat
		currency     string
		exportTime   int64
	}
	grouped := map[string]*aggRow{}
	keys := []string{}

	for _, r := range rows {
		date := strings.TrimSpace(r.Date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return BillingSyncReport{}, fmt.Errorf("invalid billing date %q: %w", date, err)
		}
		projectID := strings.TrimSpace(r.ProjectID)
		if projectID == "" || len(projectID) > 128 {
			return BillingSyncReport{}, errors.New("invalid projectID in standard export row")
		}
		if !IsAllowedTargetBillingItem(r.SKUID, r.Description) {
			return BillingSyncReport{}, fmt.Errorf("disallowed billing item (sku=%q description=%q): automatic sync only accepts the 5 target network items", r.SKUID, r.Description)
		}
		spec := ClassifyBillingItem(r.SKUID, r.Description)
		skuID := strings.TrimSpace(r.SKUID)
		if skuID == "" {
			skuID = spec.SKUID
		}
		desc := strings.TrimSpace(r.Description)
		if desc == "" {
			desc = spec.Description
		}
		if len(desc) > 512 {
			return BillingSyncReport{}, errors.New("billing description exceeds 512 chars")
		}
		invMonth := strings.TrimSpace(r.InvoiceMonth)
		if invMonth == "" {
			invMonth = strings.ReplaceAll(date[:7], "-", "")
		}
		if !invoiceRe.MatchString(invMonth) {
			return BillingSyncReport{}, fmt.Errorf("invalid invoice.month %q", invMonth)
		}
		currency := strings.TrimSpace(r.Currency)
		if !currencyCode.MatchString(currency) {
			return BillingSyncReport{}, fmt.Errorf("invalid currency %q", currency)
		}
		unit := r.UsageUnit
		if strings.TrimSpace(unit) == "" {
			unit = "GiB"
		}
		usageGiBStr, err := normalizeUsageToGiB(r.UsageAmount, unit)
		if err != nil {
			return BillingSyncReport{}, err
		}
		costStr := strings.TrimSpace(r.Cost)
		if !decimal.MatchString(costStr) {
			return BillingSyncReport{}, fmt.Errorf("cost %q must be finite decimal text", costStr)
		}
		credStr := strings.TrimSpace(r.Credits)
		if credStr == "" {
			credStr = "0"
		}
		if !decimal.MatchString(credStr) {
			return BillingSyncReport{}, fmt.Errorf("credits %q must be finite decimal text", credStr)
		}
		key := date + "\x00" + projectID + "\x00" + skuID + "\x00" + currency
		entry, exists := grouped[key]
		if !exists {
			entry = &aggRow{
				date:         date,
				projectID:    projectID,
				skuID:        skuID,
				description:  desc,
				invoiceMonth: invMonth,
				usageGiB:     new(big.Rat),
				grossCost:    new(big.Rat),
				credits:      new(big.Rat),
				currency:     currency,
				exportTime:   r.ExportTime,
			}
			grouped[key] = entry
			keys = append(keys, key)
		}
		entry.usageGiB.Add(entry.usageGiB, parseRatOrZero(usageGiBStr))
		entry.grossCost.Add(entry.grossCost, parseRatOrZero(costStr))
		entry.credits.Add(entry.credits, parseRatOrZero(credStr))
		if r.ExportTime > entry.exportTime {
			entry.exportTime = r.ExportTime
		}
	}
	sort.Strings(keys)

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return BillingSyncReport{}, err
	}
	defer tx.Rollback()

	report := BillingSyncReport{Status: "unchanged"}
	nowUnix := now.Unix()
	for _, key := range keys {
		g := grouped[key]
		usageStr := formatRatDecimal(g.usageGiB, 12)
		grossStr := formatRatDecimal(g.grossCost, 12)
		credStr := formatRatDecimal(g.credits, 12)
		netRat := new(big.Rat).Add(g.grossCost, g.credits)
		netStr := formatRatDecimal(netRat, 12)
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%d", source, g.date, g.projectID, g.skuID, usageStr, grossStr, credStr, g.currency, g.exportTime)))
		shaHex := hex.EncodeToString(digest[:])

		if g.exportTime > report.MaxExportTime {
			report.MaxExportTime = g.exportTime
		}

		var oldUsage, oldCost, oldCredits, oldNet, oldDesc string
		var oldExport int64
		err := tx.QueryRow(
			"SELECT usage_gib,cost,credits,net_cost,description,export_time FROM review_bills WHERE date=? AND project_id=? AND sku_id=? AND currency=?",
			g.date, g.projectID, g.skuID, g.currency,
		).Scan(&oldUsage, &oldCost, &oldCredits, &oldNet, &oldDesc, &oldExport)
		if err == sql.ErrNoRows {
			if _, err = tx.Exec(
				"INSERT INTO review_bills(date,project_id,sku_id,description,usage_gib,cost,currency,source_sha,scope,invoice_month,credits,net_cost,usage_unit,export_time,synced_at,sync_source) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				g.date, g.projectID, g.skuID, g.description, usageStr, grossStr, g.currency, shaHex, "project_day_sku", g.invoiceMonth, credStr, netStr, "GiB", g.exportTime, nowUnix, source,
			); err != nil {
				return BillingSyncReport{}, err
			}
			report.Inserted++
			continue
		}
		if err != nil {
			return BillingSyncReport{}, err
		}
		if g.exportTime > 0 && oldExport > 0 && g.exportTime < oldExport {
			report.StaleIgnored++
			continue
		}
		if oldNet == "" {
			oldNet = addDecimalStrings(oldCost, oldCredits)
		}
		sameValues := parseRatOrZero(oldUsage).Cmp(g.usageGiB) == 0 &&
			parseRatOrZero(oldCost).Cmp(g.grossCost) == 0 &&
			parseRatOrZero(oldCredits).Cmp(g.credits) == 0 &&
			parseRatOrZero(oldNet).Cmp(netRat) == 0
		if sameValues {
			newExport := oldExport
			if g.exportTime > newExport {
				newExport = g.exportTime
			}
			if _, err = tx.Exec(
				"UPDATE review_bills SET export_time=?,synced_at=?,invoice_month=CASE WHEN ?<>'' THEN ? ELSE invoice_month END WHERE date=? AND project_id=? AND sku_id=? AND currency=?",
				newExport, nowUnix, g.invoiceMonth, g.invoiceMonth, g.date, g.projectID, g.skuID, g.currency,
			); err != nil {
				return BillingSyncReport{}, err
			}
			report.Unchanged++
			continue
		}
		if _, err = tx.Exec(
			"UPDATE review_bills SET description=?,usage_gib=?,cost=?,source_sha=?,invoice_month=?,credits=?,net_cost=?,usage_unit='GiB',export_time=?,synced_at=?,sync_source=? WHERE date=? AND project_id=? AND sku_id=? AND currency=?",
			g.description, usageStr, grossStr, shaHex, g.invoiceMonth, credStr, netStr, g.exportTime, nowUnix, source, g.date, g.projectID, g.skuID, g.currency,
		); err != nil {
			return BillingSyncReport{}, err
		}
		alertMsg := fmt.Sprintf("Billing update/correction for %s/%s (%s, SKU %s, %s): usage %s -> %s GiB, gross %s -> %s, net %s -> %s (export_time=%d)",
			g.date, g.projectID, g.description, g.skuID, g.currency, oldUsage, usageStr, oldCost, grossStr, oldNet, netStr, g.exportTime)
		if _, err = tx.Exec(
			"INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)",
			nowUnix, "bill_correction", clip(alertMsg, 1024), s.rules.Version,
		); err != nil {
			return BillingSyncReport{}, err
		}
		report.Corrected++
	}

	if report.Corrected > 0 {
		report.Status = "corrected"
	} else if report.Inserted > 0 {
		report.Status = "ok"
	}
	if report.Inserted > 0 || report.Corrected > 0 {
		msg := fmt.Sprintf("Standard export sync (%s): %d inserted, %d corrected, %d unchanged; 5-description filter enforced; flowBillingConfirmed=false",
			source, report.Inserted, report.Corrected, report.Unchanged)
		if _, err = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", nowUnix, "bill_sync", clip(msg, 1024), s.rules.Version); err != nil {
			return BillingSyncReport{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return BillingSyncReport{}, err
	}
	return report, nil
}

// ParseExportTimestamp accepts either Unix seconds or RFC3339 timestamps.
func ParseExportTimestamp(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
		return sec, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}
