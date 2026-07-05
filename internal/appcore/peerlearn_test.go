package appcore

import (
	"errors"
	"sync"
	"testing"
	"time"

	"netlogger/internal/discovery"
)

type learnRig struct {
	mu      sync.Mutex
	fetches []string
	added   []discovery.Peer
	fetchFn func(ip string) (discovery.Peer, error)
	l       *peerLearner
	clock   time.Time
}

func newLearnRig(fetch func(ip string) (discovery.Peer, error)) *learnRig {
	r := &learnRig{fetchFn: fetch, clock: time.Unix(1000, 0)}
	r.l = newPeerLearner(
		func(ip string) (discovery.Peer, error) {
			r.mu.Lock()
			r.fetches = append(r.fetches, ip)
			r.mu.Unlock()
			return r.fetchFn(ip)
		},
		func(p discovery.Peer) {
			r.mu.Lock()
			r.added = append(r.added, p)
			r.mu.Unlock()
		},
	)
	r.l.now = func() time.Time { return r.clock }
	r.l.selfIPs = map[string]bool{"192.168.0.187": true}
	return r
}

func (r *learnRig) waitAdds(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.added)
		r.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wanted %d adds", n)
}

func (r *learnRig) counts() (fetches, adds int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.fetches), len(r.added)
}

func TestLearnerFetchesOnceThenRefreshes(t *testing.T) {
	peer := discovery.Peer{ID: "win-node", Host: "ryzen", Addr: "192.168.0.155:8088"}
	r := newLearnRig(func(ip string) (discovery.Peer, error) { return peer, nil })

	r.l.Sight("192.168.0.155")
	r.waitAdds(t, 1)

	// Within the add throttle: no new add, no new fetch.
	r.l.Sight("192.168.0.155")
	if f, a := r.counts(); f != 1 || a != 1 {
		t.Fatalf("throttled sighting caused fetch=%d add=%d, want 1/1", f, a)
	}

	// Past the throttle: liveness refresh from cache — still exactly one fetch.
	r.clock = r.clock.Add(3 * time.Second)
	r.l.Sight("192.168.0.155")
	r.waitAdds(t, 2)
	if f, _ := r.counts(); f != 1 {
		t.Fatalf("refresh re-fetched identity: fetches=%d, want 1", f)
	}
}

func TestLearnerIgnoresSelfAndCoolsDownFailures(t *testing.T) {
	r := newLearnRig(func(ip string) (discovery.Peer, error) {
		return discovery.Peer{}, errors.New("connection refused")
	})

	r.l.Sight("192.168.0.187") // self
	r.l.Sight("192.168.0.99")  // fetch fails
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f, _ := r.counts(); f == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f, a := r.counts(); f != 1 || a != 0 {
		t.Fatalf("fetch=%d add=%d, want 1 failed fetch and 0 adds", f, a)
	}

	// Repeated sightings inside the cooldown must not re-fetch.
	r.clock = r.clock.Add(5 * time.Second)
	r.l.Sight("192.168.0.99")
	time.Sleep(50 * time.Millisecond)
	if f, _ := r.counts(); f != 1 {
		t.Fatalf("re-fetched during cooldown: %d", f)
	}

	// After the cooldown it tries again.
	r.clock = r.clock.Add(learnFetchCooldown)
	r.l.Sight("192.168.0.99")
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f, _ := r.counts(); f == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no retry after cooldown")
}
