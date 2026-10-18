package trafficreview

import "database/sql"

// Hourly totals have bounded cardinality (three rows per hour), independent of
// destination names. Minute detail may expire without erasing measured totals.
const historySchema = `
CREATE TABLE IF NOT EXISTS nic_hours (at INTEGER PRIMARY KEY,rx INTEGER NOT NULL,tx INTEGER NOT NULL,elapsed INTEGER NOT NULL,quality INTEGER NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,sample_count INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS traffic_hours (at INTEGER,layer TEXT,up INTEGER NOT NULL,down INTEGER NOT NULL,tx INTEGER NOT NULL,rx INTEGER NOT NULL,remote_tx INTEGER NOT NULL,client_tx INTEGER NOT NULL,unknown_tx INTEGER NOT NULL,sample_count INTEGER NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(at,layer));
CREATE TABLE IF NOT EXISTS review_history_gaps (at INTEGER PRIMARY KEY);
CREATE TRIGGER IF NOT EXISTS review_history_gap AFTER INSERT ON review_alerts WHEN NEW.kind LIKE '%gap%' BEGIN
INSERT INTO review_history_gaps VALUES(NEW.at/3600*3600) ON CONFLICT(at) DO NOTHING;
END;
`

// Backfill only surviving measurements, once. Reopening the database must not
// re-add minute data already incorporated by the normal collection transaction.
func (s *Store) migrateHistory() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the write lock before reading the migration marker. Two panel /
	// collector processes can open a legacy database together; a deferred read
	// followed by a write upgrade otherwise fails with SQLITE_BUSY_SNAPSHOT.
	if _, err = tx.Exec("INSERT INTO review_meta VALUES('history_migration_lock','1') ON CONFLICT(key) DO UPDATE SET value=value"); err != nil {
		return err
	}
	if meta(tx, "hourly_history_v1") == "1" {
		return nil
	}
	if _, err = tx.Exec(`UPDATE traffic_minutes SET sample_count=0 WHERE layer='network' AND tx=0;
INSERT INTO nic_hours SELECT at/3600*3600,SUM(rx),SUM(tx),SUM(elapsed),MAX(quality),MIN(at),MAX(at),COUNT(*) FROM nic_minutes GROUP BY at/3600
ON CONFLICT(at) DO NOTHING;
INSERT INTO traffic_hours SELECT at/3600*3600,layer,SUM(up),SUM(down),SUM(tx),SUM(rx),SUM(CASE WHEN leg='to_remote' THEN tx ELSE 0 END),SUM(CASE WHEN leg='to_client' THEN tx ELSE 0 END),SUM(CASE WHEN leg NOT IN ('to_remote','to_client') OR leg IS NULL THEN tx ELSE 0 END),SUM(sample_count),MIN(at),MAX(at) FROM traffic_minutes GROUP BY at/3600,layer
ON CONFLICT(at,layer) DO NOTHING;
INSERT INTO review_history_gaps SELECT DISTINCT at/3600*3600 FROM review_alerts WHERE kind LIKE '%gap%' ON CONFLICT(at) DO NOTHING;`); err != nil {
		return err
	}
	if err = setMeta(tx, "hourly_history_v1", "1"); err != nil {
		return err
	}
	return tx.Commit()
}

func addTrafficHour(tx *sql.Tx, at int64, d Destination) error {
	var remote, client, unknown int64
	switch d.Leg {
	case "to_remote":
		remote = d.TXBytes
	case "to_client":
		client = d.TXBytes
	default:
		unknown = d.TXBytes
	}
	_, err := tx.Exec(`INSERT INTO traffic_hours VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(at,layer) DO UPDATE SET up=up+excluded.up,down=down+excluded.down,tx=tx+excluded.tx,rx=rx+excluded.rx,remote_tx=remote_tx+excluded.remote_tx,client_tx=client_tx+excluded.client_tx,unknown_tx=unknown_tx+excluded.unknown_tx,sample_count=sample_count+excluded.sample_count,first_at=MIN(first_at,excluded.first_at),last_at=MAX(last_at,excluded.last_at)`,
		at/3600*3600, d.Layer, d.UpBytes, d.DownBytes, d.TXBytes, d.RXBytes, remote, client, unknown, trafficObservation(d), at, at)
	return err
}

func trafficObservation(d Destination) int64 {
	if d.Layer == "proxy" || d.TXKnown || d.TXBytes > 0 {
		return 1
	}
	return 0
}

// Full hours use durable totals; the two boundary fragments use minute data.
// These predicates are disjoint even for a range wholly inside a single hour.
func historyBounds(r Range) (int64, int64) {
	from, to := (r.From+3599)/3600*3600, r.To/3600*3600
	if to < from {
		to = from
	}
	return from, to
}

func nicWindow(r Range) (string, []any) {
	from, to := historyBounds(r)
	return `WITH nic_window AS (
SELECT at,rx,tx,elapsed,quality,first_at,last_at,sample_count FROM nic_hours WHERE at>=? AND at<?
UNION ALL
SELECT at,rx,tx,elapsed,quality,at,at,1 FROM nic_minutes WHERE at>=? AND at<? AND (at<? OR at>=?)
) `, []any{from, to, r.From / 60 * 60, r.To, from, to}
}

func trafficWindow(r Range) (string, []any) {
	from, to := historyBounds(r)
	return `WITH traffic_window AS (
SELECT at,layer,up,down,tx,rx,remote_tx,client_tx,unknown_tx,sample_count,first_at,last_at FROM traffic_hours WHERE at>=? AND at<?
UNION ALL
SELECT at,layer,up,down,tx,rx,CASE WHEN leg='to_remote' THEN tx ELSE 0 END,CASE WHEN leg='to_client' THEN tx ELSE 0 END,CASE WHEN leg NOT IN ('to_remote','to_client') OR leg IS NULL THEN tx ELSE 0 END,sample_count,at,at FROM traffic_minutes WHERE at>=? AND at<? AND (at<? OR at>=?)
) `, []any{from, to, r.From / 60 * 60, r.To, from, to}
}
