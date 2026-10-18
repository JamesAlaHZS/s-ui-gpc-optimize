package trafficreview

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]{1,12})?$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// ImportBill accepts a deliberately normalized local CSV, not arbitrary cloud
// export layouts. Costs are exact decimal text, never floating-point money.
// It cannot associate a SKU total with an IP/website or allocate free quota.
func (s *Store) ImportBill(reader io.Reader) (int, error) {
	b, e := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if e != nil {
		return 0, e
	}
	if len(b) > 4<<20 {
		return 0, errors.New("bill exceeds 4 MiB")
	}
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	c := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})))
	head, e := c.Read()
	if e != nil {
		return 0, e
	}
	want := []string{"date", "project_id", "sku_id", "description", "usage_gib", "cost", "currency"}
	if len(head) != len(want) {
		return 0, errors.New("expected normalized date,project_id,sku_id,description,usage_gib,cost,currency CSV")
	}
	for i := range want {
		if head[i] != want[i] {
			return 0, errors.New("unsupported CSV header/order; normalize the bill explicitly")
		}
	}
	bills := []Bill{}
	seen := map[string]Bill{}
	for {
		row, err := c.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		if len(bills) >= 10000 {
			return 0, errors.New("too many bill rows")
		}
		if len(row) != 7 {
			return 0, errors.New("bill row must have seven columns")
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		if _, err = time.Parse("2006-01-02", row[0]); err != nil {
			return 0, err
		}
		if row[1] == "" || len(row[1]) > 128 || row[2] == "" || len(row[2]) > 128 || len(row[3]) > 512 || !currencyCode.MatchString(row[6]) {
			return 0, errors.New("invalid bill identity, description or currency")
		}
		for _, i := range []int{4, 5} {
			if len(row[i]) > 48 || !decimal.MatchString(row[i]) {
				return 0, errors.New("usage and cost must be finite decimal text")
			}
		}
		usage, _ := new(big.Rat).SetString(row[4])
		if usage.Sign() < 0 {
			return 0, errors.New("usage_gib must not be negative")
		}
		bill := Bill{Date: row[0], ProjectID: row[1], SKUID: row[2], Description: row[3], UsageGiB: row[4], Cost: row[5], Currency: row[6], SourceSHA256: hash, Scope: "project_day_sku"}
		key := bill.Date + "\x00" + bill.ProjectID + "\x00" + bill.SKUID + "\x00" + bill.Currency
		if previous, ok := seen[key]; ok {
			if !sameBill(previous, bill) {
				return 0, errors.New("conflicting duplicate bill key; aggregate source data explicitly")
			}
			continue
		}
		seen[key] = bill
		bills = append(bills, bill)
	}
	if len(bills) == 0 {
		return 0, errors.New("empty bill")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	inserted := 0
	for _, bill := range bills {
		var old Bill
		err := tx.QueryRow("SELECT usage_gib,cost,description FROM review_bills WHERE date=? AND project_id=? AND sku_id=? AND currency=?", bill.Date, bill.ProjectID, bill.SKUID, bill.Currency).Scan(&old.UsageGiB, &old.Cost, &old.Description)
		if err == nil {
			if !sameBill(old, bill) {
				return 0, fmt.Errorf("conflicting existing bill %s/%s/%s/%s; no overwrite performed", bill.Date, bill.ProjectID, bill.SKUID, bill.Currency)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		if _, e = tx.Exec("INSERT INTO review_bills VALUES(?,?,?,?,?,?,?,?,?)", bill.Date, bill.ProjectID, bill.SKUID, bill.Description, bill.UsageGiB, bill.Cost, bill.Currency, bill.SourceSHA256, bill.Scope); e != nil {
			return 0, e
		}
		inserted++
	}
	if _, e = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", time.Now().Unix(), "bill_import", fmt.Sprintf("Local CSV SHA256 %s; inserted %d project/day/SKU rows; no flow-level billing attribution", hash, inserted), s.rules.Version); e != nil {
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return inserted, nil
}

func sameBill(a, b Bill) bool {
	parse := func(v string) *big.Rat { r, _ := new(big.Rat).SetString(v); return r }
	au, bu, ac, bc := parse(a.UsageGiB), parse(b.UsageGiB), parse(a.Cost), parse(b.Cost)
	return au != nil && bu != nil && ac != nil && bc != nil && au.Cmp(bu) == 0 && ac.Cmp(bc) == 0 && a.Description == b.Description
}

func (s *Store) Bills(r Range) ([]Bill, error) {
	// Imported dates always name Pacific billing days, regardless of the
	// display timezone. A Shanghai-day query can intersect two billing days.
	loc, e := time.LoadLocation("America/Los_Angeles")
	if e != nil {
		return nil, e
	}
	from := time.Unix(r.From, 0).In(loc).Format("2006-01-02")
	to := time.Unix(r.To-1, 0).In(loc).Format("2006-01-02")
	rows, e := s.db.Query("SELECT date,project_id,sku_id,description,usage_gib,cost,currency,source_sha,scope FROM review_bills WHERE date>=? AND date<=? ORDER BY date DESC,project_id,sku_id,currency LIMIT ?", from, to, r.Limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Bill{}
	for rows.Next() {
		var b Bill
		if e = rows.Scan(&b.Date, &b.ProjectID, &b.SKUID, &b.Description, &b.UsageGiB, &b.Cost, &b.Currency, &b.SourceSHA256, &b.Scope); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
