// Package appcore is the in-process engine controller for the portable app: it
// opens the store, starts the bundled iperf3 server, runs a self-probe loop, and
// exposes a thread-safe Snapshot the UI renders. No service, no HTTP server.
package appcore

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"netlogger/internal/iperf"
	"netlogger/internal/probe"
	"netlogger/internal/store"
)

// Snapshot is an immutable view of engine state for the UI.
type Snapshot struct {
	DataDir        string
	DBPath         string
	Iperf3Version  string
	Iperf3ServerUp bool
	StartedUnix    int64
	Samples        int
	LastRTTms      float64
	LossPct        float64
}

// App is the single-machine engine controller.
type App struct {
	dataDir string
	dbPath  string

	// Seams (defaulted in New; overridden in tests).
	Ping       func(addr string, timeout time.Duration) (probe.Result, error)
	StartIperf func(dataDir string) (stop func(), version string)
	tick       time.Duration

	store     *store.Store
	iperfStop func()
	iperfVer  string
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopOnce  sync.Once
	startedAt time.Time

	mu        sync.Mutex
	samples   int
	lastRTTms float64
	recent    []bool // success flags, last N, for loss %
}

const recentWindow = 60

// New creates an App for the given (already-resolved) data dir.
func New(dataDir string) *App {
	return &App{
		dataDir:    dataDir,
		dbPath:     filepath.Join(dataDir, "netlogger.db"),
		Ping:       probe.PingICMP,
		StartIperf: defaultStartIperf,
		tick:       time.Second,
	}
}

func defaultStartIperf(dir string) (func(), string) {
	if err := iperf.Bootstrap(dir); err != nil {
		log.Printf("iperf3 bootstrap: %v", err)
	}
	ver := iperf.Version()
	srv := iperf.StartServer(0)
	if srv == nil {
		// No iperf3 binary available: report no server (nil stop).
		return nil, ver
	}
	return func() { srv.Stop() }, ver
}

// Start opens the store, starts iperf, and launches the probe loop.
func (a *App) Start() error {
	st, err := store.Open(a.dbPath)
	if err != nil {
		return err
	}
	stop, ver := a.StartIperf(a.dataDir)

	// Publish startup state under the lock so a concurrent Snapshot (UI
	// goroutine) sees a consistent view — Snapshot reads these under a.mu.
	a.mu.Lock()
	a.store = st
	a.iperfStop = stop
	a.iperfVer = ver
	a.startedAt = time.Now()
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go a.probeLoop(ctx)
	return nil
}

func (a *App) probeLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
	var seq int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := a.Ping("127.0.0.1", 2*time.Second)
			lost := err != nil || res.Lost
			seq++
			sm := store.Sample{
				Seq:       seq,
				TSUnixUS:  time.Now().UnixMicro(),
				ProbeType: "icmp",
				SrcHost:   "self",
				DstHost:   "self",
				Direction: "rtt",
				RTTus:     res.RTT.Microseconds(),
				Lost:      lost,
			}
			_, _ = a.store.Insert(sm)

			a.mu.Lock()
			a.samples++
			if !lost {
				a.lastRTTms = float64(res.RTT.Microseconds()) / 1000.0
			}
			a.recent = append(a.recent, !lost)
			if len(a.recent) > recentWindow {
				a.recent = a.recent[len(a.recent)-recentWindow:]
			}
			a.mu.Unlock()
		}
	}
}

// Snapshot returns an immutable copy of current engine state.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	loss := 0.0
	if n := len(a.recent); n > 0 {
		lost := 0
		for _, ok := range a.recent {
			if !ok {
				lost++
			}
		}
		loss = float64(lost) / float64(n) * 100.0
	}
	return Snapshot{
		DataDir:        a.dataDir,
		DBPath:         a.dbPath,
		Iperf3Version:  a.iperfVer,
		Iperf3ServerUp: a.iperfStop != nil,
		StartedUnix:    a.startedAt.Unix(),
		Samples:        a.samples,
		LastRTTms:      a.lastRTTms,
		LossPct:        loss,
	}
}

// Stop cancels the loop, stops iperf, and closes the store. Safe to call more
// than once; only the first call performs the teardown.
func (a *App) Stop() error {
	var err error
	a.stopOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.wg.Wait()
		if a.iperfStop != nil {
			a.iperfStop()
		}
		if a.store != nil {
			err = a.store.Close()
		}
	})
	return err
}
