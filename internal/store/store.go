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
	Seq       int64  `json:"seq"`
	TSUnixUS  int64  `json:"ts_unix_us"`
	ProbeType string `json:"probe_type"` // "icmp" | "udp_iso" | "tcp_connect"
	SrcHost   string `json:"src_host"`
	DstHost   string `json:"dst_host"`
	Direction string `json:"direction"` // "up" | "down" | "rtt"
	RTTus     int64  `json:"rtt_us"`    // microseconds; 0 when Lost
	JitterUS  int64  `json:"jitter_us"`
	Lost      bool   `json:"lost"`
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

CREATE TABLE IF NOT EXISTS agent_samples (
  agent_id   TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  ts_unix_us INTEGER NOT NULL,
  probe_type TEXT NOT NULL,
  src_host   TEXT NOT NULL,
  dst_host   TEXT NOT NULL,
  direction  TEXT,
  rtt_us     INTEGER,
  jitter_us  INTEGER,
  lost       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_agent_ts ON agent_samples(agent_id, ts_unix_us);
CREATE TABLE IF NOT EXISTS sync_cursors (
  agent_id TEXT PRIMARY KEY,
  last_seq INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS connectivity_events (
  ts_unix_us INTEGER NOT NULL,
  agent_id   TEXT NOT NULL,
  online     INTEGER NOT NULL,
  detail     TEXT
);
CREATE INDEX IF NOT EXISTS idx_conn_agent_ts ON connectivity_events(agent_id, ts_unix_us);
`

var pragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA busy_timeout=5000",
	"PRAGMA journal_size_limit=67108864",
	"PRAGMA wal_autocheckpoint=1000",
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
// LossBuckets returns, per target (dst_host), the loss% in each fixed-width time
// bucket across [fromUS, toUS): the fraction of that bucket's probe samples that
// saw loss. Peer links use udp_iso; gateway/internet use icmp. Buckets with no
// samples are -1 (no data). The result aligns every target to the same bucket
// grid so the UI can stack them on one time axis for concurrency.
func (s *Store) LossBuckets(fromUS, toUS int64, bucketSec int) (map[string][]float64, error) {
	out := map[string][]float64{}
	if bucketSec <= 0 || toUS <= fromUS {
		return out, nil
	}
	bucketUS := int64(bucketSec) * 1_000_000
	n := int((toUS - fromUS + bucketUS - 1) / bucketUS)
	rows, err := s.db.Query(
		`SELECT dst_host, (ts_unix_us-?)/? AS bkt, 100.0*SUM(lost)/COUNT(*) AS loss
		 FROM probe_samples
		 WHERE ts_unix_us>=? AND ts_unix_us<? AND
		   (probe_type='udp_iso' OR (probe_type='icmp' AND dst_host IN ('__gateway__','__internet__')))
		 GROUP BY dst_host, bkt`, fromUS, bucketUS, fromUS, toUS)
	if err != nil {
		return nil, fmt.Errorf("loss buckets: %w", err)
	}
	defer rows.Close()
	get := func(host string) []float64 {
		a := out[host]
		if a == nil {
			a = make([]float64, n)
			for i := range a {
				a[i] = -1
			}
			out[host] = a
		}
		return a
	}
	for rows.Next() {
		var host string
		var bkt int
		var loss float64
		if err := rows.Scan(&host, &bkt, &loss); err != nil {
			return nil, err
		}
		if bkt >= 0 && bkt < n {
			get(host)[bkt] = loss
		}
	}
	return out, rows.Err()
}

// LinkStateBuckets returns a per-bucket NIC-link-flap marker for one agent: a
// bucket is 100 when a link Disconnected→Up flap (a reset) overlaps it, else -1.
// Only paired down/up events count, so a link that's simply off (a never-recovered
// disconnect, e.g. unused Wi-Fi) doesn't paint the row. This surfaces hard link
// resets even in buckets where the loss probe happened to catch 0%.
func (s *Store) LinkStateBuckets(fromUS, toUS int64, bucketSec int, agentID string) ([]float64, error) {
	if bucketSec <= 0 || toUS <= fromUS {
		return []float64{}, nil
	}
	bucketUS := int64(bucketSec) * 1_000_000
	n := int((toUS - fromUS + bucketUS - 1) / bucketUS)
	out := make([]float64, n)
	for i := range out {
		out[i] = -1
	}
	rows, err := s.db.Query(
		`SELECT ts_unix_us, online FROM connectivity_events
		 WHERE agent_id=? AND ts_unix_us < ? AND
		   (detail LIKE '%link Disconnected%' OR detail LIKE '%link Up%')
		 ORDER BY ts_unix_us`, agentID, toUS)
	if err != nil {
		return nil, fmt.Errorf("link state buckets: %w", err)
	}
	defer rows.Close()
	mark := func(downTS, upTS int64) {
		lo := (downTS - fromUS) / bucketUS
		hi := (upTS - fromUS) / bucketUS
		for b := lo; b <= hi; b++ {
			if b >= 0 && b < int64(n) {
				out[b] = 100
			}
		}
	}
	pendingDown := int64(-1)
	for rows.Next() {
		var ts int64
		var online int
		if err := rows.Scan(&ts, &online); err != nil {
			return nil, err
		}
		if online == 0 {
			pendingDown = ts
		} else if pendingDown >= 0 {
			mark(pendingDown, ts)
			pendingDown = -1
		}
	}
	return out, rows.Err()
}

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

// Upsert inserts an aggregated sample from agentID, keyed (agent_id, seq).
// A repeated (agent_id, seq) is ignored — idempotent, so retry/overlap is safe.
func (s *Store) Upsert(agentID string, sm Sample) error {
	var rtt, jitter any
	if sm.Lost {
		rtt = nil
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
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO agent_samples
		   (agent_id,seq,ts_unix_us,probe_type,src_host,dst_host,direction,rtt_us,jitter_us,lost)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		agentID, sm.Seq, sm.TSUnixUS, sm.ProbeType, sm.SrcHost, sm.DstHost, sm.Direction, rtt, jitter, lost)
	if err != nil {
		return fmt.Errorf("upsert agent sample: %w", err)
	}
	return nil
}

// Cursor returns the last durably-synced seq for agentID (0 if none yet).
func (s *Store) Cursor(agentID string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT last_seq FROM sync_cursors WHERE agent_id=?`, agentID).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read cursor: %w", err)
	}
	return seq, nil
}

// SetCursor stores the last durably-synced seq for agentID.
func (s *Store) SetCursor(agentID string, seq int64) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_cursors (agent_id,last_seq) VALUES (?,?)
		 ON CONFLICT(agent_id) DO UPDATE SET last_seq=excluded.last_seq`,
		agentID, seq)
	if err != nil {
		return fmt.Errorf("set cursor: %w", err)
	}
	return nil
}

// ConnEvent is a coordinator-observed agent connectivity transition.
type ConnEvent struct {
	TSUnixUS int64  `json:"ts_unix_us"`
	AgentID  string `json:"agent_id"`
	Online   bool   `json:"online"`
	Detail   string `json:"detail"`
}

// InsertConnectivityEvent records an online/offline transition for agentID.
func (s *Store) InsertConnectivityEvent(tsUnixUS int64, agentID string, online bool, detail string) error {
	o := 0
	if online {
		o = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO connectivity_events (ts_unix_us,agent_id,online,detail) VALUES (?,?,?,?)`,
		tsUnixUS, agentID, o, detail)
	if err != nil {
		return fmt.Errorf("insert connectivity event: %w", err)
	}
	return nil
}

// ConnectivityEvents returns agentID's connectivity transitions, oldest first.
func (s *Store) ConnectivityEvents(agentID string) ([]ConnEvent, error) {
	rows, err := s.db.Query(
		`SELECT ts_unix_us,agent_id,online,COALESCE(detail,'') FROM connectivity_events
		 WHERE agent_id=? ORDER BY ts_unix_us`, agentID)
	if err != nil {
		return nil, fmt.Errorf("query connectivity events: %w", err)
	}
	defer rows.Close()
	var out []ConnEvent
	for rows.Next() {
		var e ConnEvent
		var on int
		if err := rows.Scan(&e.TSUnixUS, &e.AgentID, &on, &e.Detail); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		e.Online = on == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountAgentSamples returns the number of aggregated rows for agentID.
func (s *Store) CountAgentSamples(agentID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_samples WHERE agent_id=?`, agentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count agent samples: %w", err)
	}
	return n, nil
}

// AgentSamplesAll returns all aggregated rows for agentID ordered by seq.
func (s *Store) AgentSamplesAll(agentID string) ([]Sample, error) {
	rows, err := s.db.Query(
		`SELECT seq,ts_unix_us,probe_type,src_host,dst_host,
		        COALESCE(direction,''),COALESCE(rtt_us,0),COALESCE(jitter_us,0),lost
		 FROM agent_samples WHERE agent_id=? ORDER BY seq`, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent samples all: %w", err)
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
