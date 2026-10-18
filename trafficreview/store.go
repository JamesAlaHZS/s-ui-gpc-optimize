package trafficreview

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const applicationID = 0x53545256

type Store struct {
	db    *sql.DB
	path  string
	mu    sync.Mutex
	rules Rules
}

func sqliteURI(path, params string) string {
	return "file:" + strings.ReplaceAll(url.PathEscape(filepath.ToSlash(path)), "%2F", "/") + "?" + params
}

// Open refuses an existing unrelated database before applying any PRAGMA or
// migration. This prevents accidentally migrating the panel's original DB.
func Open(path string, rules Rules) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if rules.Version == "" {
		rules = DefaultRules()
	}
	if info, e := os.Stat(abs); e == nil && info.Size() > 0 {
		probe, e := sql.Open("sqlite3", sqliteURI(abs, "mode=ro&_busy_timeout=5000"))
		if e != nil {
			return nil, e
		}
		var id int
		e = probe.QueryRow("PRAGMA application_id").Scan(&id)
		probe.Close()
		if e != nil {
			return nil, e
		}
		if id != applicationID {
			return nil, errors.New("refusing non-traffic-review database")
		}
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite3", sqliteURI(abs, "_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: abs, rules: rules}
	schema := fmt.Sprintf(`
PRAGMA application_id=%d;
PRAGMA max_page_count=65536;
PRAGMA journal_size_limit=8388608;
CREATE TABLE IF NOT EXISTS review_meta (key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS nic_samples (at INTEGER PRIMARY KEY,iface TEXT,rx INTEGER,tx INTEGER,elapsed INTEGER,quality INTEGER);
CREATE TABLE IF NOT EXISTS nic_minutes (at INTEGER PRIMARY KEY,rx INTEGER NOT NULL,tx INTEGER NOT NULL,elapsed INTEGER NOT NULL,quality INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS traffic_minutes (at INTEGER,layer TEXT,peer TEXT,domain TEXT,user TEXT,inbound TEXT,outbound TEXT,protocol TEXT,business TEXT,confidence TEXT,leg TEXT,fee_status TEXT,fee_basis TEXT,rule_version TEXT,up INTEGER,down INTEGER,tx INTEGER,rx INTEGER,PRIMARY KEY(at,layer,peer,domain,user,inbound,outbound,protocol,business,confidence,leg,fee_status,fee_basis,rule_version));
CREATE TABLE IF NOT EXISTS review_checkpoints (id TEXT PRIMARY KEY,up INTEGER NOT NULL,down INTEGER NOT NULL,seen INTEGER NOT NULL,known INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS review_alerts (id INTEGER PRIMARY KEY AUTOINCREMENT,at INTEGER NOT NULL,kind TEXT NOT NULL,message TEXT NOT NULL,rule_version TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS review_alert_time ON review_alerts(at);
CREATE TABLE IF NOT EXISTS review_bills (date TEXT,project_id TEXT,sku_id TEXT,description TEXT,usage_gib TEXT,cost TEXT,currency TEXT,source_sha TEXT,scope TEXT NOT NULL,PRIMARY KEY(date,project_id,sku_id,currency));
`, applicationID)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Size() int64 {
	var n int64
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if f, e := os.Stat(p); e == nil {
			n += f.Size()
		}
	}
	return n
}

func meta(tx *sql.Tx, key string) string {
	var value string
	_ = tx.QueryRow("SELECT value FROM review_meta WHERE key=?", key).Scan(&value)
	return value
}
func setMeta(tx *sql.Tx, key, value string) error {
	_, e := tx.Exec("INSERT INTO review_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return e
}

func (s *Store) Alert(at int64, kind, message string) error {
	_, err := s.db.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", at, clip(kind, 64), clip(message, 1024), s.rules.Version)
	return err
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Store) Retain(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{"DELETE FROM nic_samples WHERE at<?", now - 7*86400},
		{"DELETE FROM nic_minutes WHERE at<?", now - 90*86400},
		{"DELETE FROM traffic_minutes WHERE at<?", now - 7*86400},
		{"DELETE FROM review_checkpoints WHERE seen<?", now - 7200},
		{"DELETE FROM review_alerts WHERE at<?", now - 90*86400},
	} {
		if _, err = tx.Exec(q.sql, q.arg); err != nil {
			return err
		}
	}
	// Bounded cardinality protects a small VPS from adversarial domains/peers.
	var n int64
	if err = tx.QueryRow("SELECT COUNT(*) FROM traffic_minutes").Scan(&n); err != nil {
		return err
	}
	if n > 100000 {
		if _, err = tx.Exec("DELETE FROM traffic_minutes WHERE rowid IN (SELECT rowid FROM traffic_minutes ORDER BY at LIMIT ?)", n-100000); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO review_alerts(at,kind,message,rule_version) VALUES(?,?,?,?)", now, "retention_gap", "Destination cardinality limit reached; oldest detail removed", s.rules.Version); err != nil {
			return err
		}
	}
	_, err = tx.Exec("DELETE FROM review_alerts WHERE id NOT IN (SELECT id FROM review_alerts ORDER BY id DESC LIMIT 10000)")
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, err = s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

func addDestination(tx *sql.Tx, at int64, d Destination, version string) error {
	if d.UpBytes < 0 || d.DownBytes < 0 || d.TXBytes < 0 || d.RXBytes < 0 {
		return errors.New("negative traffic counter")
	}
	if d.UpBytes == 0 && d.DownBytes == 0 && d.TXBytes == 0 && d.RXBytes == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO traffic_minutes VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(at,layer,peer,domain,user,inbound,outbound,protocol,business,confidence,leg,fee_status,fee_basis,rule_version) DO UPDATE SET up=up+excluded.up,down=down+excluded.down,tx=tx+excluded.tx,rx=rx+excluded.rx`,
		at/60*60, d.Layer, clip(d.Peer, 256), clip(d.Domain, 253), clip(d.User, 128), clip(d.Inbound, 128), clip(d.Outbound, 128), clip(d.Protocol, 16), d.Business, d.Confidence, d.Leg, d.FeeStatus, clip(d.FeeBasis, 600), version, d.UpBytes, d.DownBytes, d.TXBytes, d.RXBytes)
	return err
}

// RecordProxy persists cumulative watermarks and deltas atomically. Retrying a
// batch after SQLITE_BUSY cannot double-count it or reset native panel stats.
func (s *Store) RecordProxy(epoch string, records []ProxyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range records {
		if r.Up < 0 || r.Down < 0 {
			return errors.New("invalid proxy counters")
		}
		id := "proxy:" + epoch + ":" + r.ID
		var up, down int64
		err = tx.QueryRow("SELECT up,down FROM review_checkpoints WHERE id=?", id).Scan(&up, &down)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if r.Up < up || r.Down < down {
			return errors.New("proxy counter decreased within an epoch")
		}
		business, confidence := Business(r.Domain)
		if strings.EqualFold(r.Protocol, "udp") {
			// A UDP association can carry several destinations. Its initial
			// domain remains context, not proof that every datagram belongs to it.
			business, confidence = "unknown", "unknown"
		}
		fee, basis := s.rules.ClassifyPeer(r.Peer)
		d := Destination{Layer: "proxy", Peer: r.Peer, Domain: r.Domain, User: r.User, Inbound: r.Inbound, Outbound: r.Outbound, Protocol: r.Protocol, Business: business, Confidence: confidence, Leg: "unknown", FeeStatus: fee, FeeBasis: basis, UpBytes: r.Up - up, DownBytes: r.Down - down}
		if err = addDestination(tx, r.At, d, s.rules.Version); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO review_checkpoints VALUES(?,?,?,?,1) ON CONFLICT(id) DO UPDATE SET up=excluded.up,down=excluded.down,seen=excluded.seen", id, r.Up, r.Down, r.At); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Summary(r Range) (Summary, error) {
	v := Summary{Range: r, RuleVersion: s.rules.Version, SamplingSeconds: 10, Partial: true, Disclaimer: Disclaimer}
	_ = s.db.QueryRow("SELECT CAST(value AS INTEGER) FROM review_meta WHERE key='interval_seconds'").Scan(&v.SamplingSeconds)
	var first sql.NullInt64
	var quality int
	err := s.db.QueryRow("SELECT COALESCE(SUM(rx),0),COALESCE(SUM(tx),0),COALESCE(MAX(quality),0),MIN(at),COALESCE(MAX(at),0) FROM nic_minutes WHERE at>=? AND at<?", r.From/60*60, r.To).Scan(&v.RXBytes, &v.TXBytes, &quality, &first, &v.LastSampleAt)
	if err != nil {
		return v, err
	}
	err = s.db.QueryRow("SELECT COALESCE(SUM(CASE WHEN layer='proxy' THEN up ELSE 0 END),0),COALESCE(SUM(CASE WHEN layer='proxy' THEN down ELSE 0 END),0),COALESCE(SUM(CASE WHEN layer='network' THEN tx ELSE 0 END),0),COALESCE(SUM(CASE WHEN layer='network' THEN rx ELSE 0 END),0) FROM traffic_minutes WHERE at>=? AND at<?", r.From/60*60, r.To).Scan(&v.ProxyUploadBytes, &v.ProxyDownloadBytes, &v.NetworkEgressBytes, &v.NetworkIngressBytes)
	// Destination detail expires after seven days. It must never be displayed as
	// complete history merely because the interface total is still retained.
	v.Partial = quality != 0 || !first.Valid || first.Int64 > r.From+60 || r.From < time.Now().Add(-7*24*time.Hour).Unix() || v.LastSampleAt < r.To-120
	var gaps int
	if e := s.db.QueryRow("SELECT COUNT(*) FROM review_alerts WHERE at>=? AND at<? AND kind LIKE '%gap%'", r.From, r.To).Scan(&gaps); e != nil {
		return v, e
	}
	var healthJSON string
	if e := s.db.QueryRow("SELECT value FROM review_meta WHERE key='conntrack_health'").Scan(&healthJSON); e != nil {
		v.Partial = true
	} else if h, ok := decodeHealth(healthJSON).(ConntrackHealth); !ok || !h.Available || h.Degraded {
		v.Partial = true
	}
	// Classification is minute-bucketed. A non-aligned query includes a
	// partially intersecting bucket and must never claim exact coverage.
	v.Partial = v.Partial || gaps > 0 || r.From%60 != 0 || r.To%60 != 0
	var sampleAt sql.NullInt64
	if e := s.db.QueryRow("SELECT MAX(at) FROM nic_samples WHERE at>=? AND at<?", r.From, r.To).Scan(&sampleAt); e == nil && sampleAt.Valid {
		v.LastSampleAt = sampleAt.Int64
	}
	return v, err
}

func (s *Store) Series(r Range) ([]SeriesPoint, error) {
	step := int64(10)
	table := "nic_samples"
	if r.To-r.From > 6*3600 {
		step = 60
	}
	if r.To-r.From > 2*86400 {
		step = 900
	}
	if r.From < time.Now().Add(-7*24*time.Hour).Unix() {
		table = "nic_minutes"
		if step < 60 {
			step = 60
		}
	}
	rows, err := s.db.Query("SELECT at/?*?,SUM(rx),SUM(tx),SUM(rx)*1.0/MAX(SUM(elapsed),1),SUM(tx)*1.0/MAX(SUM(elapsed),1),MAX(quality) FROM "+table+" WHERE at>=? AND at<? GROUP BY at/? ORDER BY at/? LIMIT 5000", step, step, r.From, r.To, step, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		var q int
		if err = rows.Scan(&p.Time, &p.RXBytes, &p.TXBytes, &p.RXBPS, &p.TXBPS, &q); err != nil {
			return nil, err
		}
		p.Quality = "ok"
		if q != 0 {
			p.Quality = "partial"
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Destinations(r Range) ([]Destination, error) {
	rows, err := s.db.Query(`SELECT layer,peer,domain,user,inbound,outbound,protocol,business,confidence,leg,fee_status,fee_basis,rule_version,SUM(up),SUM(down),SUM(tx),SUM(rx) FROM traffic_minutes WHERE at>=? AND at<? GROUP BY layer,peer,domain,user,inbound,outbound,protocol,business,confidence,leg,fee_status,fee_basis,rule_version ORDER BY SUM(up)+SUM(down)+SUM(tx)+SUM(rx) DESC LIMIT ?`, r.From/60*60, r.To, r.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Destination{}
	for rows.Next() {
		var d Destination
		if err = rows.Scan(&d.Layer, &d.Peer, &d.Domain, &d.User, &d.Inbound, &d.Outbound, &d.Protocol, &d.Business, &d.Confidence, &d.Leg, &d.FeeStatus, &d.FeeBasis, &d.RuleVersion, &d.UpBytes, &d.DownBytes, &d.TXBytes, &d.RXBytes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Alerts(r Range) ([]Alert, error) {
	rows, err := s.db.Query("SELECT id,at,kind,message,rule_version FROM review_alerts WHERE at>=? AND at<? ORDER BY at DESC,id DESC LIMIT ?", r.From, r.To, r.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err = rows.Scan(&a.ID, &a.Time, &a.Kind, &a.Message, &a.RuleVersion); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Health(now int64) (Health, error) {
	h := Health{Enabled: true, SamplingSeconds: 10, DatabaseBytes: s.Size(), Status: "starting", Conntrack: map[string]any{"available": false, "degraded": true, "lastError": "Collector not observed"}}
	var at sql.NullInt64
	err := s.db.QueryRow("SELECT MAX(at) FROM nic_samples").Scan(&at)
	if err != nil {
		return h, err
	}
	h.LastSampleAt = at.Int64
	_ = s.db.QueryRow("SELECT CAST(value AS INTEGER) FROM review_meta WHERE key='interval_seconds'").Scan(&h.SamplingSeconds)
	_ = s.db.QueryRow("SELECT value FROM review_meta WHERE key='interface'").Scan(&h.NetworkInterface)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM review_alerts WHERE kind LIKE '%gap%' OR kind LIKE '%reset%'").Scan(&h.Gaps)
	var healthJSON string
	if s.db.QueryRow("SELECT value FROM review_meta WHERE key='conntrack_health'").Scan(&healthJSON) == nil {
		h.Conntrack = decodeHealth(healthJSON)
	}
	h.Available = at.Valid && now-at.Int64 <= 30
	if h.Available {
		h.Status = "collecting"
	} else if at.Valid {
		h.Status = "stale"
	}
	return h, nil
}
