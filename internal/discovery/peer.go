package discovery

import (
	"sort"
	"sync"
	"time"
)

// Peer is a discovered instance on the LAN.
type Peer struct {
	ID       string
	Host     string
	Addr     string
	Version  string
	LastSeen time.Time
}

// table is the thread-safe peer table: dedup by ID, expire by TTL.
type table struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	peers map[string]Peer
}

func newTable(ttl time.Duration, now func() time.Time) *table {
	return &table{ttl: ttl, now: now, peers: make(map[string]Peer)}
}

func (t *table) upsert(p Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p.LastSeen = t.now()
	t.peers[p.ID] = p
}

func (t *table) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, id)
}

func (t *table) list() []Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := t.now().Add(-t.ttl)
	out := make([]Peer, 0, len(t.peers))
	for id, p := range t.peers {
		if p.LastSeen.Before(cutoff) {
			delete(t.peers, id)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return out
}
