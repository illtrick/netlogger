// Package store is the local, append-mostly SQLite (WAL) sample store.
// It is the agent's source of truth; the coordinator later syncs from it.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Sample is one probe measurement. A lost probe has Lost=true and RTTus=0
// (persisted as NULL — never a sentinel, per spec §8).
type Sample struct {
	Seq       int64
	TSUnixUS  int64
	ProbeType string // "icmp" | "udp_iso" | "tcp_connect"
	SrcHost   string
	DstHost   string
	Direction string // "up" | "down" | "rtt"
	RTTus     int64  // microseconds; 0 when Lost
	JitterUS  int64
	Lost      bool
}

// Store wraps the SQLite database.
type Store struct{ db *sql.DB }

// DB exposes the underlying handle (used by tests and the sync layer).
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS probe_samples (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  ts_unix_us INTEGER NOT NULL,
  probe_type TEXT NOT NULL,
  src_host   TEXT NOT NULL,
  dst_host   TEXT NOT NULL,
  direction  TEXT,
  rtt_us     INTEGER,
  jitter_us  INTEGER,
  lost       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_probe_ts ON probe_samples(ts_unix_us);
CREATE INDEX IF NOT EXISTS idx_probe_target_ts ON probe_samples(dst_host, ts_unix_us);
`

var pragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA busy_timeout=5000",
	"PRAGMA journal_size_limit=67108864",
	"PRAGMA temp_store=MEMORY",
	"PRAGMA auto_vacuum=INCREMENTAL",
}

// Open opens (creating if needed) the database at path with the spec PRAGMAs
// and schema applied.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Insert writes one sample and returns its assigned seq.
func (s *Store) Insert(sm Sample) (int64, error) {
	var rtt, jitter any
	if sm.Lost {
		rtt = nil // NULL for loss
	} else {
		rtt = sm.RTTus
	}
	if sm.JitterUS != 0 {
		jitter = sm.JitterUS
	}
	lost := 0
	if sm.Lost {
		lost = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO probe_samples (ts_unix_us,probe_type,src_host,dst_host,direction,rtt_us,jitter_us,lost)
		 VALUES (?,?,?,?,?,?,?,?)`,
		sm.TSUnixUS, sm.ProbeType, sm.SrcHost, sm.DstHost, sm.Direction, rtt, jitter, lost)
	if err != nil {
		return 0, fmt.Errorf("insert sample: %w", err)
	}
	return res.LastInsertId()
}

// Since returns up to limit samples with seq greater than afterSeq, in order.
func (s *Store) Since(afterSeq int64, limit int) ([]Sample, error) {
	rows, err := s.db.Query(
		`SELECT seq,ts_unix_us,probe_type,src_host,dst_host,
		        COALESCE(direction,''),COALESCE(rtt_us,0),COALESCE(jitter_us,0),lost
		 FROM probe_samples WHERE seq > ? ORDER BY seq LIMIT ?`,
		afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("query since: %w", err)
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var sm Sample
		var lostInt int
		if err := rows.Scan(&sm.Seq, &sm.TSUnixUS, &sm.ProbeType, &sm.SrcHost,
			&sm.DstHost, &sm.Direction, &sm.RTTus, &sm.JitterUS, &lostInt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		sm.Lost = lostInt == 1
		out = append(out, sm)
	}
	return out, rows.Err()
}
