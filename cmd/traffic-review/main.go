// traffic-review is a local-only collector, normalized bill importer,
// bounded BigQuery Standard usage export syncer, and candidate relay analyzer.
// It does not link or start the proxy/panel, and opens no listening network port.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alireza0/s-ui/trafficreview"
)

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: traffic-review collect|import-bill|sync-bill|analyze-candidates|health --db PATH [options]")
	}
	cmd := os.Args[1]
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	db := f.String("db", trafficreview.DefaultDB(), "separate traffic-review SQLite path")
	rulesPath := f.String("rules", "", "local operator candidate rule JSON")
	iface := f.String("interface", "ens4", "public network interface")
	interval := f.Duration("interval", 10*time.Second, "sample period")
	ct := f.Bool("conntrack", false, "collect kernel connection events; requires installed CLI and capability")
	file := f.String("file", "", "normalized local bill CSV")
	duration := f.Duration("duration", 0, "optional bounded collection/test duration")

	// sync-bill flags
	mockExport := f.String("mock-export", "", "local Standard usage export JSON snapshot for offline testing")
	queryProject := f.String("query-project", "", "GCP project ID for BigQuery job execution (roles/bigquery.jobUser)")
	datasetProject := f.String("dataset-project", "", "GCP project ID hosting the billing export dataset")
	dataset := f.String("dataset", "", "BigQuery billing export dataset ID (roles/bigquery.dataViewer)")
	table := f.String("table", "", "Standard usage cost export table gcp_billing_export_v1_<BILLING_ACCOUNT_ID>")
	targetProject := f.String("target-project", "", "optional target GCP project ID filter")
	lookbackDays := f.Int("lookback-days", trafficreview.DefaultLookbackDays, "recomputable lookback window in days (1..31)")
	maxBytes := f.Int64("max-bytes", trafficreview.DefaultMaxBytesPerQuery, "per-query maximum bytes billed/scanned (<=64 MiB)")
	maxMonthlyBytes := f.Int64("max-monthly-bytes", trafficreview.DefaultMaxMonthlyBytes, "monthly maximum bytes scanned (<$0.05/mo)")
	maxStorageBytes := f.Int64("max-storage-bytes", trafficreview.DefaultMaxStorageBytes, "maximum billing dataset storage bytes")
	existingMonthlyBytes := f.Int64("existing-monthly-bytes", 0, "existing account-wide monthly BigQuery scanned bytes")
	dryRun := f.Bool("dry-run", false, "perform BigQuery dry run and budget check only without executing live query")
	useADC := f.Bool("adc", true, "use VM Application Default Credentials (no long-lived JSON keys)")

	// analyze-candidates flags
	dateFlag := f.String("date", "", "optional billing day YYYY-MM-DD")
	tzFlag := f.String("timezone", "America/Los_Angeles", "query timezone (America/Los_Angeles|Asia/Shanghai|UTC)")
	relayOutbound := f.String("relay-outbound", "", "optional relay outbound tag; defaults to pending_configuration")
	beforeDate := f.String("before-date", "", "optional pre-relay billing day YYYY-MM-DD for SKU comparison")
	afterDate := f.String("after-date", "", "optional post-relay billing day YYYY-MM-DD for SKU comparison")

	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *db == "" {
		return fmt.Errorf("--db is required")
	}
	switch cmd {
	case "collect", "import-bill", "sync-bill", "analyze-candidates", "health":
	default:
		return fmt.Errorf("unknown command")
	}
	rules, e := trafficreview.LoadRules(*rulesPath)
	if e != nil {
		return e
	}
	s, e := trafficreview.Open(*db, rules)
	if e != nil {
		return e
	}
	defer s.Close()
	switch cmd {
	case "collect":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if *duration < 0 {
			return fmt.Errorf("duration cannot be negative")
		}
		if *duration > 0 {
			var stop context.CancelFunc
			ctx, stop = context.WithTimeout(ctx, *duration)
			defer stop()
		}
		return s.Collect(ctx, trafficreview.CollectorOptions{Interface: *iface, Interval: *interval, Conntrack: *ct})
	case "import-bill":
		if *file == "" {
			return fmt.Errorf("--file is required")
		}
		fh, e := os.Open(*file)
		if e != nil {
			return e
		}
		defer fh.Close()
		n, e := s.ImportBill(fh)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"inserted": n, "scope": "project_day_sku", "flowBillingConfirmed": false})
	case "sync-bill":
		if *mockExport != "" {
			fh, e := os.Open(*mockExport)
			if e != nil {
				return e
			}
			defer fh.Close()
			rep, e := s.SyncStandardExportJSON(fh, time.Now())
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(rep)
		}
		cfg := trafficreview.BillingSyncConfig{
			QueryProjectID:              *queryProject,
			DatasetProjectID:            *datasetProject,
			DatasetID:                   *dataset,
			TableID:                     *table,
			TargetProjectID:             *targetProject,
			LookbackDays:                *lookbackDays,
			MaxBytesPerQuery:            *maxBytes,
			MaxMonthlyBytes:             *maxMonthlyBytes,
			MaxDatasetStorageBytes:      *maxStorageBytes,
			ExistingAccountMonthlyBytes: *existingMonthlyBytes,
			DryRunOnly:                  *dryRun,
			UseADC:                      *useADC,
			Now:                         time.Now(),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		rep, e := s.SyncStandardBilling(ctx, cfg, trafficreview.NewADCBigQueryClient())
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(rep)
	case "analyze-candidates":
		q := url.Values{"timezone": {*tzFlag}}
		if *dateFlag != "" {
			q.Set("date", *dateFlag)
		}
		r, e := trafficreview.ParseRange(q, time.Now())
		if e != nil {
			return e
		}
		overview, e := s.BillingOverview(r, time.Now())
		if e != nil {
			return e
		}
		corr, e := s.CarrierCorrelationWithRelay(r, *relayOutbound)
		if e != nil {
			return e
		}
		out := map[string]any{
			"billingOverview":      overview,
			"carrierCorrelation":   corr,
			"flowBillingConfirmed": false,
		}
		if *beforeDate != "" && *afterDate != "" {
			bq := url.Values{"timezone": {*tzFlag}, "date": {*beforeDate}}
			aq := url.Values{"timezone": {*tzFlag}, "date": {*afterDate}}
			br, e1 := trafficreview.ParseRange(bq, time.Now())
			ar, e2 := trafficreview.ParseRange(aq, time.Now())
			if e1 != nil {
				return e1
			}
			if e2 != nil {
				return e2
			}
			cmp, e := s.CompareRelayPeriods(br, ar)
			if e != nil {
				return e
			}
			out["relayComparison"] = cmp
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	case "health":
		h, e := s.Health(time.Now().Unix())
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(h)
	}
	return nil
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
