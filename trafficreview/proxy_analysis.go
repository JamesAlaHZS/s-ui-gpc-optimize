package trafficreview

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const proxyAnalysisSchema = `
CREATE TABLE IF NOT EXISTS proxy_business_hours (at INTEGER,business TEXT,up INTEGER NOT NULL,down INTEGER NOT NULL,observations INTEGER NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(at,business));
CREATE TABLE IF NOT EXISTS proxy_business_minutes (at INTEGER,business TEXT,up INTEGER NOT NULL,down INTEGER NOT NULL,observations INTEGER NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(at,business));
CREATE TABLE IF NOT EXISTS proxy_coverage_hours (at INTEGER PRIMARY KEY,seconds INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS proxy_history_gaps (at INTEGER PRIMARY KEY);
CREATE TRIGGER IF NOT EXISTS proxy_history_gap AFTER INSERT ON review_alerts WHEN substr(NEW.kind,1,6)='proxy_' AND NEW.kind LIKE '%gap%' BEGIN
INSERT INTO proxy_history_gaps VALUES(NEW.at/3600*3600) ON CONFLICT(at) DO NOTHING;
END;
`

type ProxyAnalysis struct {
	RemoteUploadBytes              int64                  `json:"remoteUploadBytes"`
	RemoteDownloadBytes            int64                  `json:"remoteDownloadBytes"`
	ForwardedEgressBytes           int64                  `json:"forwardedEgressBytes"`
	ForwardedIngressBytes          int64                  `json:"forwardedIngressBytes"`
	CandidateUploadBytes           int64                  `json:"candidateUploadBytes"`
	CandidateDownloadBytes         int64                  `json:"candidateDownloadBytes"`
	CandidateForwardedEgressBytes  int64                  `json:"candidateForwardedEgressBytes"`
	CandidateForwardedIngressBytes int64                  `json:"candidateForwardedIngressBytes"`
	OtherForwardedEgressBytes      int64                  `json:"otherForwardedEgressBytes"`
	UnknownDomainBytes             int64                  `json:"unknownDomainBytes"`
	Observations                   int64                  `json:"observations"`
	HasObservations                bool                   `json:"hasObservations"`
	FirstSampleAt                  int64                  `json:"firstSampleAt"`
	LastSampleAt                   int64                  `json:"lastSampleAt"`
	Partial                        bool                   `json:"partial"`
	MeasurementBasis               string                 `json:"measurementBasis"`
	FlowBillingConfirmed           bool                   `json:"flowBillingConfirmed"`
	BillingComparison              ProxyBillingComparison `json:"billingComparison"`
}

type ProxyBillingDay struct {
	Date            string `json:"date"`
	CandidateBytes  int64  `json:"candidateBytes"`
	OtherBytes      int64  `json:"otherBytes"`
	AllBytes        int64  `json:"allBytes"`
	UnknownBytes    int64  `json:"unknownBytes"`
	CarrierUsageGiB string `json:"carrierUsageGiB"`
	CarrierNetCost  string `json:"carrierNetCost"`
}

type ProxyBillingScope struct {
	ProjectID              string            `json:"projectID"`
	Currency               string            `json:"currency"`
	SKUIDs                 []string          `json:"skuIDs"`
	Status                 string            `json:"status"`
	Reason                 string            `json:"reason"`
	PairCount              int               `json:"pairCount"`
	EligibleDays           int               `json:"eligibleDays"`
	CandidateCorrelation   *float64          `json:"candidateCorrelation"`
	BaselineCorrelation    *float64          `json:"baselineCorrelation"`
	ConfoundingCorrelation *float64          `json:"confoundingCorrelation"`
	CarrierUsageGiB        string            `json:"carrierUsageGiB"`
	CarrierNetCost         string            `json:"carrierNetCost"`
	BillingDayCount        int               `json:"billingDayCount"`
	Days                   []ProxyBillingDay `json:"days"`
}

type ProxyBillingComparison struct {
	AnalysisFrom         int64               `json:"analysisFrom"`
	AnalysisTo           int64               `json:"analysisTo"`
	Status               string              `json:"status"`
	Reason               string              `json:"reason"`
	PairCount            int                 `json:"pairCount"`
	EligibleDays         int                 `json:"eligibleDays"`
	MinMatchedDays       int                 `json:"minMatchedDays"`
	ExcludedRecentDays   int                 `json:"excludedRecentDays"`
	Timezone             string              `json:"timezone"`
	Scope                string              `json:"scope"`
	FlowBillingConfirmed bool                `json:"flowBillingConfirmed"`
	Scopes               []ProxyBillingScope `json:"scopes"`
}

// These are domain hypotheses. An IP address, UDP association, or missing
// domain does not identify a website, even if it resembles a shared provider.
func proxyBusiness(domain, protocol string) string {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if d == "" || strings.EqualFold(protocol, "udp") || len(d) > 253 {
		return "unknown"
	}
	if _, err := netip.ParseAddr(strings.Trim(d, "[]")); err == nil {
		return "unknown"
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "unknown"
		}
		for _, r := range label {
			if r < 'a' || r > 'z' {
				if r < '0' || r > '9' {
					if r != '-' {
						return "unknown"
					}
				}
			}
		}
	}
	if business, _ := Business(d); business == "OpenAI" {
		return "chatgpt"
	}
	return "other"
}

func addProxyBusiness(tx *sql.Tx, at int64, d Destination) error {
	group := proxyBusiness(d.Domain, d.Protocol)
	for _, table := range []struct {
		name string
		step int64
	}{{"proxy_business_hours", 3600}, {"proxy_business_minutes", 60}} {
		if _, err := tx.Exec("INSERT INTO "+table.name+` VALUES(?,?,?,?,1,?,?) ON CONFLICT(at,business) DO UPDATE SET up=up+excluded.up,down=down+excluded.down,observations=observations+1,first_at=MIN(first_at,excluded.first_at),last_at=MAX(last_at,excluded.last_at)`, at/table.step*table.step, group, d.UpBytes, d.DownBytes, at, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateProxyHistory() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO review_meta VALUES('proxy_history_migration_lock','1') ON CONFLICT(key) DO UPDATE SET value=value"); err != nil {
		return err
	}
	if meta(tx, "proxy_business_history_v1") == "1" {
		return nil
	}
	rows, err := tx.Query("SELECT at,domain,protocol,up,down,sample_count FROM traffic_minutes WHERE layer='proxy'")
	if err != nil {
		return err
	}
	type key struct {
		at    int64
		group string
	}
	type total struct{ up, down, count, first, last int64 }
	minutes := map[key]total{}
	for rows.Next() {
		var at, up, down, count int64
		var domain, protocol string
		if err = rows.Scan(&at, &domain, &protocol, &up, &down, &count); err != nil {
			rows.Close()
			return err
		}
		k := key{at, proxyBusiness(domain, protocol)}
		v := minutes[k]
		v.up, v.down, v.count, v.first, v.last = v.up+up, v.down+down, v.count+count, at, at
		minutes[k] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	hours := map[key]total{}
	for k, v := range minutes {
		if _, err = tx.Exec("INSERT INTO proxy_business_minutes VALUES(?,?,?,?,?,?,?) ON CONFLICT(at,business) DO NOTHING", k.at, k.group, v.up, v.down, v.count, v.first, v.last); err != nil {
			return err
		}
		h := key{k.at / 3600 * 3600, k.group}
		value, exists := hours[h]
		value.up, value.down, value.count = value.up+v.up, value.down+v.down, value.count+v.count
		if !exists || v.first < value.first {
			value.first = v.first
		}
		if v.last > value.last {
			value.last = v.last
		}
		hours[h] = value
	}
	for k, v := range hours {
		if _, err = tx.Exec("INSERT INTO proxy_business_hours VALUES(?,?,?,?,?,?,?) ON CONFLICT(at,business) DO NOTHING", k.at, k.group, v.up, v.down, v.count, v.first, v.last); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO proxy_history_gaps SELECT DISTINCT at/3600*3600 FROM review_alerts WHERE substr(kind,1,6)='proxy_' AND kind LIKE '%gap%' ON CONFLICT(at) DO NOTHING"); err != nil {
		return err
	}
	if err = setMeta(tx, "proxy_business_history_v1", "1"); err != nil {
		return err
	}
	return tx.Commit()
}

// Coverage is a successful collection heartbeat, including idle zero-session
// ticks. Flow presence cannot establish that the rest of an hour was observed.
// A new epoch, first tick, failed tick (caller changes epoch), or long pause
// establishes a new baseline; none of those gaps is backfilled as complete.
func (s *Store) RecordProxyCoverage(at int64, interval int, epoch string) error {
	if at <= 0 || interval < 1 || interval > 30 || epoch == "" || len(epoch) > 512 {
		return errors.New("invalid proxy coverage heartbeat")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous struct {
		At    int64  `json:"at"`
		Epoch string `json:"epoch"`
	}
	if raw := meta(tx, "proxy_coverage_checkpoint"); raw != "" {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
	}
	if at <= previous.At {
		return nil
	}
	if previous.Epoch == epoch && at-previous.At <= int64(interval*3) && at-previous.At <= 30 {
		for start := previous.At; start < at; {
			hour := start / 3600 * 3600
			end := hour + 3600
			if end > at {
				end = at
			}
			if _, err = tx.Exec("INSERT INTO proxy_coverage_hours VALUES(?,?) ON CONFLICT(at) DO UPDATE SET seconds=MIN(3600,seconds+excluded.seconds)", hour, end-start); err != nil {
				return err
			}
			start = end
		}
	}
	current, _ := json.Marshal(struct {
		At    int64  `json:"at"`
		Epoch string `json:"epoch"`
	}{at, epoch})
	if err = setMeta(tx, "proxy_coverage_checkpoint", string(current)); err != nil {
		return err
	}
	return tx.Commit()
}

func proxyBusinessWindow(r Range) (string, []any) {
	from, to := historyBounds(r)
	return `WITH business_window AS (
SELECT * FROM proxy_business_hours WHERE at>=? AND at<? UNION ALL
SELECT * FROM proxy_business_minutes WHERE at>=? AND at<? AND(at<? OR at>=?)
) `, []any{from, to, r.From / 60 * 60, r.To, from, to}
}

func proxyTotalWindow(r Range) (string, []any) {
	from, to := historyBounds(r)
	return `WITH proxy_window AS (
SELECT up,down,sample_count AS observations,first_at,last_at FROM traffic_hours WHERE layer='proxy' AND at>=? AND at<? UNION ALL
SELECT up,down,observations,first_at,last_at FROM proxy_business_minutes WHERE at>=? AND at<? AND(at<? OR at>=?)
) `, []any{from, to, r.From / 60 * 60, r.To, from, to}
}

func (s *Store) ProxyAnalysis(r Range, now time.Time) (ProxyAnalysis, error) {
	v := ProxyAnalysis{MeasurementBasis: "proxy_forwarded_payload", Partial: true}
	query, args := proxyTotalWindow(r)
	err := s.db.QueryRow(query+"SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0),COALESCE(SUM(observations),0),COALESCE(MIN(first_at),0),COALESCE(MAX(last_at),0) FROM proxy_window", args...).Scan(&v.RemoteUploadBytes, &v.RemoteDownloadBytes, &v.Observations, &v.FirstSampleAt, &v.LastSampleAt)
	if err != nil {
		return v, err
	}
	query, args = proxyBusinessWindow(r)
	var known int64
	err = s.db.QueryRow(query+"SELECT COALESCE(SUM(CASE WHEN business='chatgpt' THEN up ELSE 0 END),0),COALESCE(SUM(CASE WHEN business='chatgpt' THEN down ELSE 0 END),0),COALESCE(SUM(CASE WHEN business='other' THEN up+down ELSE 0 END),0),COALESCE(SUM(up+down),0) FROM business_window", args...).Scan(&v.CandidateUploadBytes, &v.CandidateDownloadBytes, &v.OtherForwardedEgressBytes, &known)
	if err != nil {
		return v, err
	}
	v.ForwardedEgressBytes = v.RemoteUploadBytes + v.RemoteDownloadBytes
	v.ForwardedIngressBytes = v.ForwardedEgressBytes
	v.CandidateForwardedEgressBytes = v.CandidateUploadBytes + v.CandidateDownloadBytes
	v.CandidateForwardedIngressBytes = v.CandidateForwardedEgressBytes
	v.UnknownDomainBytes = v.ForwardedEgressBytes - v.CandidateForwardedEgressBytes - v.OtherForwardedEgressBytes
	if v.UnknownDomainBytes < 0 {
		v.UnknownDomainBytes = 0
	}
	coverage, gaps, err := s.proxyCoverage(r)
	if err != nil {
		return v, err
	}
	v.HasObservations = v.Observations > 0
	for at, seconds := range coverage {
		if seconds == 3600 || seconds > 0 && at >= r.From && at+3600 <= r.To {
			v.HasObservations = true
			if v.FirstSampleAt == 0 || at < v.FirstSampleAt {
				v.FirstSampleAt = at
			}
			if at+3599 > v.LastSampleAt {
				v.LastSampleAt = at + 3599
				if v.LastSampleAt >= r.To {
					v.LastSampleAt = r.To - 1
				}
			}
		}
	}
	var checkpoint string
	if s.db.QueryRow("SELECT value FROM review_meta WHERE key='proxy_coverage_checkpoint'").Scan(&checkpoint) == nil {
		var tick struct {
			At int64 `json:"at"`
		}
		if json.Unmarshal([]byte(checkpoint), &tick) == nil && tick.At >= r.From && tick.At < r.To {
			v.HasObservations = true
			if v.FirstSampleAt == 0 {
				v.FirstSampleAt = tick.At
			}
			if tick.At > v.LastSampleAt {
				v.LastSampleAt = tick.At
			}
		}
	}
	v.Partial = !v.HasObservations || known != v.ForwardedEgressBytes || r.From%60 != 0 || r.To%60 != 0 || !coveredProxyRange(r, coverage, gaps)
	v.BillingComparison, err = s.proxyBillingComparison(r, now)
	return v, err
}

func (s *Store) proxyCoverage(r Range) (map[int64]int64, map[int64]bool, error) {
	coverage, gaps := map[int64]int64{}, map[int64]bool{}
	rows, err := s.db.Query("SELECT at,seconds FROM proxy_coverage_hours WHERE at>=? AND at<?", r.From/3600*3600, r.To)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var at, n int64
		if err = rows.Scan(&at, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		coverage[at] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query("SELECT at FROM proxy_history_gaps WHERE at>=? AND at<?", r.From/3600*3600, r.To)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var at int64
		if err = rows.Scan(&at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		gaps[at] = true
	}
	err = rows.Err()
	rows.Close()
	return coverage, gaps, err
}

func coveredProxyRange(r Range, coverage map[int64]int64, gaps map[int64]bool) bool {
	if r.To <= r.From {
		return false
	}
	for hour := r.From / 3600 * 3600; hour < r.To; hour += 3600 {
		if coverage[hour] < 3600 || gaps[hour] {
			return false
		}
	}
	return true
}

type proxyDayTotals struct {
	candidate, other, all, unknown int64
	businessKnown                  int64
}

func (s *Store) proxyBillingComparison(r Range, now time.Time) (ProxyBillingComparison, error) {
	out := ProxyBillingComparison{Status: "collecting", Reason: "no_proxy_data", MinMatchedDays: 7, ExcludedRecentDays: 3, Timezone: "America/Los_Angeles", Scope: "project_day_carrier_sku", Scopes: []ProxyBillingScope{}}
	loc, err := time.LoadLocation(out.Timezone)
	if err != nil {
		return out, err
	}
	// A default 24-hour screen still accumulates an automatic comparison over
	// prior complete days. Its separate, explicit window never changes the
	// selected-range proxy totals and never reads past the selected cutoff.
	if r.To > now.Unix() {
		r.To = now.Unix()
	}
	startDay := time.Unix(r.From, 0).In(loc)
	startDay = time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, loc)
	if startDay.Unix() < r.From {
		startDay = startDay.AddDate(0, 0, 1)
	}
	if startDay.AddDate(0, 0, 7).Unix() > r.To {
		end := time.Unix(r.To, 0).In(loc)
		r.From = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -35).Unix()
	}
	out.AnalysisFrom, out.AnalysisTo = r.From, r.To
	coverage, gaps, err := s.proxyCoverage(r)
	if err != nil {
		return out, err
	}
	dayTotals := map[string]proxyDayTotals{}
	rows, err := s.db.Query("SELECT at,up+down FROM traffic_hours WHERE layer='proxy' AND at>=? AND at<?", r.From/3600*3600, r.To)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var at, n int64
		if err = rows.Scan(&at, &n); err != nil {
			rows.Close()
			return out, err
		}
		day := time.Unix(at, 0).In(loc).Format("2006-01-02")
		v := dayTotals[day]
		v.all += n
		dayTotals[day] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.db.Query("SELECT at,business,up+down FROM proxy_business_hours WHERE at>=? AND at<?", r.From/3600*3600, r.To)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var at, n int64
		var group string
		if err = rows.Scan(&at, &group, &n); err != nil {
			rows.Close()
			return out, err
		}
		day := time.Unix(at, 0).In(loc).Format("2006-01-02")
		v := dayTotals[day]
		v.businessKnown += n
		if group == "chatgpt" {
			v.candidate += n
		}
		if group == "other" {
			v.other += n
		}
		dayTotals[day] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for day, v := range dayTotals {
		v.unknown = v.all - v.candidate - v.other
		dayTotals[day] = v
	}
	first := time.Unix(r.From, 0).In(loc)
	start := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	if start.Unix() < r.From {
		start = start.AddDate(0, 0, 1)
	}
	today := now.In(loc)
	matureBefore := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -2)
	eligible := map[string]bool{}
	for day := start; day.Before(matureBefore) && day.AddDate(0, 0, 1).Unix() <= r.To; day = day.AddDate(0, 0, 1) {
		name := day.Format("2006-01-02")
		v := dayTotals[name]
		if v.businessKnown == v.all && coveredProxyRange(Range{From: day.Unix(), To: day.AddDate(0, 0, 1).Unix()}, coverage, gaps) {
			eligible[name] = true
			out.EligibleDays++
		}
	}
	if len(dayTotals) > 0 {
		out.Reason = "no_billing_data"
	}
	bills, err := s.allBills(r)
	if err != nil {
		return out, err
	}
	type scopeData struct {
		value ProxyBillingScope
		byDay map[string]ProxyBillingDay
		skus  map[string]bool
	}
	scopes := map[string]*scopeData{}
	for _, b := range bills {
		if ClassifyBillingItem(b.SKUID, b.Description).Category != "carrier_peering_egress" {
			continue
		}
		key := b.ProjectID + "\x00" + b.Currency
		data := scopes[key]
		if data == nil {
			data = &scopeData{value: ProxyBillingScope{ProjectID: b.ProjectID, Currency: b.Currency, SKUIDs: []string{}, CarrierUsageGiB: "0", CarrierNetCost: "0", Days: []ProxyBillingDay{}, EligibleDays: out.EligibleDays}, byDay: map[string]ProxyBillingDay{}, skus: map[string]bool{}}
			scopes[key] = data
		}
		data.skus[b.SKUID] = true
		data.value.CarrierUsageGiB = addDecimalStrings(data.value.CarrierUsageGiB, b.UsageGiB)
		data.value.CarrierNetCost = addDecimalStrings(data.value.CarrierNetCost, b.NetCost)
		v := data.byDay[b.Date]
		v.Date = b.Date
		v.CarrierUsageGiB = addDecimalStrings(v.CarrierUsageGiB, b.UsageGiB)
		v.CarrierNetCost = addDecimalStrings(v.CarrierNetCost, b.NetCost)
		data.byDay[b.Date] = v
	}
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairedDates := map[string]bool{}
	for _, key := range keys {
		data := scopes[key]
		data.value.BillingDayCount = len(data.byDay)
		for sku := range data.skus {
			data.value.SKUIDs = append(data.value.SKUIDs, sku)
		}
		sort.Strings(data.value.SKUIDs)
		dates := make([]string, 0, len(data.byDay))
		for day := range data.byDay {
			dates = append(dates, day)
		}
		sort.Strings(dates)
		for _, day := range dates {
			if !eligible[day] || parseRatOrZero(data.byDay[day].CarrierUsageGiB).Sign() < 0 {
				continue
			}
			b := data.byDay[day]
			local := dayTotals[day]
			b.CandidateBytes = local.candidate
			b.AllBytes = local.all
			b.OtherBytes = local.all - local.candidate
			b.UnknownBytes = local.unknown
			data.value.Days = append(data.value.Days, b)
			pairedDates[day] = true
		}
		analyzeProxyScope(&data.value)
		out.Scopes = append(out.Scopes, data.value)
	}
	out.PairCount = len(pairedDates)
	projects := map[string]bool{}
	for _, scope := range out.Scopes {
		projects[scope.ProjectID] = true
	}
	if len(projects) > 1 {
		out.Status = "inconclusive"
		out.Reason = "multiple_projects"
		for i := range out.Scopes {
			out.Scopes[i].Status = "inconclusive"
			out.Scopes[i].Reason = "multiple_projects"
		}
	} else if len(out.Scopes) == 1 {
		out.Status = out.Scopes[0].Status
		out.Reason = out.Scopes[0].Reason
	} else if len(out.Scopes) > 1 {
		out.Status = "inconclusive"
		out.Reason = "multiple_scopes"
	} else if len(dayTotals) > 0 && out.EligibleDays == 0 {
		out.Reason = "incomplete_coverage"
	}
	return out, nil
}

func analyzeProxyScope(v *ProxyBillingScope) {
	v.PairCount = len(v.Days)
	v.Status = "insufficient_data"
	v.Reason = "insufficient_days"
	if v.EligibleDays == 0 {
		v.Status = "collecting"
		v.Reason = "incomplete_coverage"
		return
	}
	if v.PairCount < 7 {
		return
	}
	candidate, all, other, billed := []float64{}, []float64{}, []float64{}, []float64{}
	var unknown, total int64
	for _, d := range v.Days {
		candidate = append(candidate, float64(d.CandidateBytes))
		all = append(all, float64(d.AllBytes))
		other = append(other, float64(d.OtherBytes))
		b, _ := parseRatOrZero(d.CarrierUsageGiB).Float64()
		billed = append(billed, b)
		unknown += d.UnknownBytes
		total += d.AllBytes
	}
	v.CandidateCorrelation = pearson(candidate, billed)
	v.BaselineCorrelation = pearson(all, billed)
	v.ConfoundingCorrelation = pearson(candidate, other)
	if !varying(candidate) {
		v.Status = "inconclusive"
		v.Reason = "constant_candidate"
		if total > 0 && unknown == 0 {
			var candidateTotal, billedTotal float64
			for i := range candidate {
				candidateTotal += candidate[i]
				billedTotal += billed[i]
			}
			if candidateTotal == 0 && billedTotal > 0 {
				v.Reason = "charges_without_candidate"
			}
		}
		return
	}
	if !varying(all) {
		v.Status = "inconclusive"
		v.Reason = "constant_total"
		return
	}
	if !varying(billed) {
		v.Status = "inconclusive"
		v.Reason = "constant_billing"
		return
	}
	v.Status = "inconclusive"
	v.Reason = "weak_correlation"
	if total > 0 && float64(unknown)/float64(total) > 0.1 {
		v.Reason = "unknown_domain_mix"
		return
	}
	if v.ConfoundingCorrelation != nil && math.Abs(*v.ConfoundingCorrelation) >= 0.8 {
		v.Reason = "confounded"
		return
	}
	if v.BaselineCorrelation != nil && v.CandidateCorrelation != nil && *v.BaselineCorrelation > *v.CandidateCorrelation+0.1 {
		v.Reason = "confounded"
		return
	}
	if v.CandidateCorrelation != nil && *v.CandidateCorrelation >= 0.8 {
		v.Status = "correlated"
		v.Reason = "correlated"
	}
}

func varying(values []float64) bool {
	if len(values) < 2 {
		return false
	}
	for _, v := range values[1:] {
		if v != values[0] {
			return true
		}
	}
	return false
}

func pearson(x, y []float64) *float64 {
	if len(x) != len(y) || len(x) < 2 || !varying(x) || !varying(y) {
		return nil
	}
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	var xx, yy, xy float64
	for i := range x {
		a, b := x[i]-mx, y[i]-my
		xx += a * a
		yy += b * b
		xy += a * b
	}
	if xx == 0 || yy == 0 {
		return nil
	}
	v := xy / math.Sqrt(xx*yy)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	v = math.Max(-1, math.Min(1, v))
	return &v
}
