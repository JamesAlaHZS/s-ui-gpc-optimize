package trafficreview

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const billHeader = "date,project_id,sku_id,description,usage_gib,cost,currency\n"

func billStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "review.db"), DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func billRange() Range {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC).Unix()
	return Range{From: from, To: from + 86400, Timezone: "UTC", Limit: 200}
}

func TestImportBillExactDecimalBOMDuplicatesAndCurrency(t *testing.T) {
	s := billStore(t)
	row := "2026-10-08,project-1,984A-1F27-2D1F,Carrier Peering,3.82,0.306000000001,USD\n"
	n, err := s.ImportBill(strings.NewReader("\ufeff" + billHeader + row + row))
	if err != nil || n != 1 {
		t.Fatalf("BOM/duplicate import: n=%d err=%v", n, err)
	}
	if n, err = s.ImportBill(strings.NewReader(billHeader + row)); err != nil || n != 0 {
		t.Fatalf("idempotent reimport: n=%d err=%v", n, err)
	}
	if n, err = s.ImportBill(strings.NewReader(billHeader + "2026-10-08,project-1,984A-1F27-2D1F,Carrier Peering,3.82,0.306000000001,EUR\n")); err != nil || n != 1 {
		t.Fatalf("separate currency: n=%d err=%v", n, err)
	}
	bills, err := s.Bills(billRange())
	if err != nil || len(bills) != 2 {
		t.Fatalf("bills: %+v, err=%v", bills, err)
	}
	byCurrency := map[string]Bill{}
	for _, bill := range bills {
		byCurrency[bill.Currency] = bill
	}
	if byCurrency["USD"].Cost != "0.306000000001" || byCurrency["EUR"].Cost != "0.306000000001" || byCurrency["USD"].UsageGiB != "3.82" || byCurrency["USD"].Scope != "project_day_sku" {
		t.Fatalf("decimal/currency mixed or rounded: %+v", bills)
	}
}

func TestImportBillConflictRollsBackWholeCSV(t *testing.T) {
	s := billStore(t)
	original := "2026-10-08,p,sku-a,Original,1.0,0.306,USD\n"
	if _, err := s.ImportBill(strings.NewReader(billHeader + original)); err != nil {
		t.Fatal(err)
	}
	conflict := billHeader + "2026-10-08,p,sku-b,New,2.0,0.20,USD\n" + "2026-10-08,p,sku-a,Original,1.0,0.999,USD\n"
	if n, err := s.ImportBill(strings.NewReader(conflict)); err == nil || n != 0 {
		t.Fatalf("conflict must fail atomically: n=%d err=%v", n, err)
	}
	bills, err := s.Bills(billRange())
	if err != nil || len(bills) != 1 || bills[0].SKUID != "sku-a" || bills[0].Cost != "0.306" {
		t.Fatalf("conflict partially committed: %+v, err=%v", bills, err)
	}
	withinCSV := billHeader + original + "2026-10-08,p,sku-a,Original,1.0,0.310,USD\n"
	if n, err := s.ImportBill(strings.NewReader(withinCSV)); err == nil || n != 0 {
		t.Fatalf("conflicting duplicate accepted: n=%d err=%v", n, err)
	}
}

func TestImportBillRejectsUnnormalizedFormats(t *testing.T) {
	s := billStore(t)
	bad := []string{
		"Date,project_id,sku_id,description,usage_gib,cost,currency\n2026-10-08,p,s,d,1,1,USD\n",
		billHeader + "2026-10-08,p,s,d,1e3,1,USD\n",
		billHeader + "2026-10-08,p,s,d,1,NaN,USD\n",
		billHeader + "2026-10-08,p,s,d,-1,1,USD\n",
		billHeader + "2026-10-08,p,s,d,1,1,usd\n",
		billHeader + "2026-10-08,p,s,d,1,1,USD,extra\n",
		billHeader + "2026-10-08,p,s,d,1,0.0000000000001,USD\n",
		billHeader,
	}
	for i, csv := range bad {
		if n, err := s.ImportBill(strings.NewReader(csv)); err == nil || n != 0 {
			t.Errorf("invalid CSV #%d accepted: n=%d err=%v", i, n, err)
		}
	}
	if bills, err := s.Bills(billRange()); err != nil || len(bills) != 0 {
		t.Fatalf("bad input wrote data: %+v, err=%v", bills, err)
	}
}

func TestImportBillCannotPromoteFlowFeeStatus(t *testing.T) {
	s := billStore(t)
	at := billRange().From + 3600
	record := ProxyRecord{ID: "1", Peer: "91.108.23.100", Domain: "telegram.org", At: at, Up: 300, Down: 400}
	if err := s.RecordProxy("epoch", []ProxyRecord{record}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportBill(strings.NewReader(billHeader + "2026-10-08,p,984A-1F27-2D1F,Carrier Peering,3.82,0.306,USD\n")); err != nil {
		t.Fatal(err)
	}
	destinations, err := s.Destinations(billRange())
	if err != nil || len(destinations) != 1 {
		t.Fatalf("flow query: %+v, err=%v", destinations, err)
	}
	if destinations[0].FeeStatus != "unknown" || destinations[0].Business != "Telegram" || destinations[0].UpBytes != 300 {
		t.Fatalf("bill assigned cost status to flow: %+v", destinations[0])
	}
}

func TestBillImportProtectsUnrelatedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	db, err := sql.Open("sqlite3", sqliteURI(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel VALUES('keep');"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path, DefaultRules()); err == nil {
		s.Close()
		t.Fatal("unrelated DB accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("unrelated DB changed")
	}
}
