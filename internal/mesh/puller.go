package mesh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"netlogger/internal/store"
)

// AgentRef identifies an agent to pull from.
type AgentRef struct {
	ID      string
	BaseURL string // e.g. "http://127.0.0.1:8089"
}

// AgentState is the coordinator's view of one agent's liveness.
type AgentState struct {
	Online   bool
	LastSeen time.Time
	LastErr  string
}

// Puller pulls samples from agents into the aggregated store, resiliently.
type Puller struct {
	agg    *store.Store
	client *http.Client

	mu    sync.Mutex
	state map[string]AgentState
}

// NewPuller builds a Puller writing into agg.
func NewPuller(agg *store.Store) *Puller {
	return &Puller{
		agg:    agg,
		client: &http.Client{Timeout: 5 * time.Second},
		state:  make(map[string]AgentState),
	}
}

// State returns the last-known state for agentID.
func (p *Puller) State(id string) AgentState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state[id]
}

func (p *Puller) setState(id string, st AgentState) {
	p.mu.Lock()
	p.state[id] = st
	p.mu.Unlock()
}

// PullOnce fetches everything since the stored cursor for a, upserts it
// idempotently, and advances the cursor only after successful upserts.
func (p *Puller) PullOnce(a AgentRef) (int, error) {
	cursor, err := p.agg.Cursor(a.ID)
	if err != nil {
		return 0, err
	}
	url := a.BaseURL + "/api/samples?since=" + strconv.FormatInt(cursor, 10) + "&limit=500"
	resp, err := p.client.Get(url)
	if err != nil {
		p.setState(a.ID, AgentState{Online: false, LastErr: err.Error()})
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("agent %s returned %d", a.ID, resp.StatusCode)
		p.setState(a.ID, AgentState{Online: false, LastErr: e.Error()})
		return 0, e
	}

	var samples []store.Sample
	if err := json.NewDecoder(resp.Body).Decode(&samples); err != nil {
		p.setState(a.ID, AgentState{Online: false, LastErr: err.Error()})
		return 0, err
	}

	maxSeq := cursor
	for _, sm := range samples {
		if err := p.agg.Upsert(a.ID, sm); err != nil {
			// Partial progress already persisted; report error without advancing cursor.
			return 0, err
		}
		if sm.Seq > maxSeq {
			maxSeq = sm.Seq
		}
	}
	if maxSeq > cursor {
		if err := p.agg.SetCursor(a.ID, maxSeq); err != nil {
			return len(samples), err
		}
	}
	p.setState(a.ID, AgentState{Online: true, LastSeen: time.Now()})
	return len(samples), nil
}
