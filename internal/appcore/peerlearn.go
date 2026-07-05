package appcore

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"netlogger/internal/discovery"
)

// peerLearner turns the source IPs of inbound control-plane requests into
// discovered peers. A node that can reach us over HTTP is running NetLogger
// and polling us continuously — even if we can never hear its announces
// (multicast-filtering Wi-Fi APs, or peers too old to send unicast replies).
// On first sighting of an IP it fetches /api/links once for the identity;
// afterwards each sighting just refreshes the peer's liveness, so the entry
// expires naturally via discovery's TTL when the traffic stops.
type peerLearner struct {
	fetch func(ip string) (discovery.Peer, error)
	add   func(discovery.Peer)
	now   func() time.Time

	mu       sync.Mutex
	selfIPs  map[string]bool
	known    map[string]discovery.Peer // ip → fetched identity
	fetching map[string]bool
	lastTry  map[string]time.Time // failed-fetch cooldown per ip
	lastAdd  map[string]time.Time // liveness-refresh throttle per ip
}

const (
	learnFetchCooldown = 30 * time.Second // between identity fetch attempts per ip
	learnAddThrottle   = 2 * time.Second  // between liveness refreshes per ip
)

// errNotAPeer marks an /api/links responder that isn't a usable peer (no id,
// or ourselves through an unrecognized local address).
var errNotAPeer = errors.New("responder is not a peer")

func newPeerLearner(fetch func(string) (discovery.Peer, error), add func(discovery.Peer)) *peerLearner {
	return &peerLearner{
		fetch:    fetch,
		add:      add,
		now:      time.Now,
		selfIPs:  localIPSet(),
		known:    make(map[string]discovery.Peer),
		fetching: make(map[string]bool),
		lastTry:  make(map[string]time.Time),
		lastAdd:  make(map[string]time.Time),
	}
}

// Sight records that traffic arrived from ip. Cheap and non-blocking: the
// identity fetch (first sighting only) runs on its own goroutine.
func (l *peerLearner) Sight(ip string) {
	if l == nil || ip == "" {
		return
	}
	now := l.now()
	l.mu.Lock()
	if l.selfIPs[ip] {
		l.mu.Unlock()
		return
	}
	if p, ok := l.known[ip]; ok {
		if now.Sub(l.lastAdd[ip]) < learnAddThrottle {
			l.mu.Unlock()
			return
		}
		l.lastAdd[ip] = now
		l.mu.Unlock()
		l.add(p)
		return
	}
	if l.fetching[ip] || now.Sub(l.lastTry[ip]) < learnFetchCooldown {
		l.mu.Unlock()
		return
	}
	l.fetching[ip] = true
	l.lastTry[ip] = now
	l.mu.Unlock()

	go func() {
		p, err := l.fetch(ip)
		l.mu.Lock()
		l.fetching[ip] = false
		if err != nil {
			l.mu.Unlock()
			return
		}
		l.known[ip] = p
		l.lastAdd[ip] = l.now()
		l.mu.Unlock()
		l.add(p)
	}()
}

// localIPSet mirrors discovery's self-detection: our own unicast addresses.
func localIPSet() map[string]bool {
	set := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return set
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP != nil {
			set[ipnet.IP.String()] = true
		}
	}
	return set
}

// sightingHandler wraps the control-plane mux so every (host-allowed) inbound
// request feeds the learner before being served.
func sightingHandler(l *peerLearner, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l != nil {
			if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				l.Sight(ip)
			}
		}
		next.ServeHTTP(w, r)
	})
}
