package appcore

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// InternetResult is a device→internet test outcome.
type InternetResult struct {
	DownMbit float64 `json:"down_mbit"`
	UpMbit   float64 `json:"up_mbit"`
	IdleMs   float64 `json:"idle_ms"`
	LoadedMs float64 `json:"loaded_ms"`
	JitterMs float64 `json:"jitter_ms"`
	LossPct  float64 `json:"loss_pct"`
	RPM      int     `json:"rpm"`
	Grade    string  `json:"grade"`
	Endpoint string  `json:"endpoint"`
	Err      string  `json:"err,omitempty"`
}

// gradeBufferbloat maps the latency added under load to a letter grade and an
// approximate RPM (round-trips per minute). Thresholds follow the common
// bufferbloat scale (added ms): A<30, B<60, C<100, D<200, else F.
func gradeBufferbloat(idleMs, loadedMs float64) (grade string, addedMs float64, rpm int) {
	addedMs = loadedMs - idleMs
	if addedMs < 0 {
		addedMs = 0
	}
	switch {
	case addedMs < 30:
		grade = "A"
	case addedMs < 60:
		grade = "B"
	case addedMs < 100:
		grade = "C"
	case addedMs < 200:
		grade = "D"
	default:
		grade = "F"
	}
	if loadedMs > 0 {
		rpm = int(60000/loadedMs + 0.5)
	}
	return grade, addedMs, rpm
}

// internetResult assembles + grades the measured pieces (pure).
func internetResult(endpoint string, down, up, idle, loaded, jitter, loss float64) InternetResult {
	grade, _, rpm := gradeBufferbloat(idle, loaded)
	return InternetResult{
		DownMbit: round1(down), UpMbit: round1(up),
		IdleMs: round1(idle), LoadedMs: round1(loaded),
		JitterMs: round1(jitter), LossPct: round1(loss),
		RPM: rpm, Grade: grade, Endpoint: endpoint,
	}
}

// internetDeps are the injectable network measurements (real impls hit LibreSpeed).
type internetDeps struct {
	endpoint string
	idle     func() float64 // a quiet-line latency sample (ms)
	download func(ctx context.Context) (mbit, loadedMs float64, err error)
	upload   func(ctx context.Context) (mbit, loadedMs float64, err error)
}

// runInternet runs the three phases (idle → download → upload) and grades the
// result. Pure orchestration over the injected measurements.
func runInternet(d internetDeps) InternetResult {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	idle := d.idle()
	down, ld, derr := d.download(ctx)
	up, lu, uerr := d.upload(ctx)
	loaded := ld
	if lu > loaded {
		loaded = lu
	}
	res := internetResult(d.endpoint, down, up, idle, loaded, 0, 0)
	if derr != nil {
		res.Err = derr.Error()
	} else if uerr != nil {
		res.Err = uerr.Error()
	} else if loaded == 0 && (down > 0 || up > 0) {
		// Every loaded-latency probe failed (plausible under exactly the
		// congestion being graded) — a defaulted "A" would be a lie.
		res.Err = "no latency samples under load — bufferbloat grade unreliable"
	}
	return res
}

// --- real measurements over LibreSpeed (manual-gated; behind the seams above) ---
//
// LibreSpeed's open servers serve large garbage.php chunks and accept looped
// empty.php uploads with NO Cloudflare-style 403 size cap or 429 rate limiting,
// so they actually saturate a fast (1 Gb) link. Accuracy still depends on PARALLEL
// streams (one TCP connection each → independent congestion windows), a discarded
// WARM-UP, and a multi-second STEADY-STATE window.

// speedServer is one LibreSpeed backend (full URLs for each sub-endpoint).
type speedServer struct {
	Name  string
	DLURL string // garbage.php — GET ?ckSize=<MB>
	ULURL string // empty.php  — POST a chunk, discarded
	Ping  string // empty.php  — GET, timed
}

// libreServers is the pre-loaded pool, US-west-coast first so a west-coast user
// auto-selects a nearby server. Sourced from the official LibreSpeed list.
var libreServers = []speedServer{
	{"Los Angeles (Clouvider)", "https://la.speedtest.clouvider.net/backend/garbage.php", "https://la.speedtest.clouvider.net/backend/empty.php", "https://la.speedtest.clouvider.net/backend/empty.php"},
	{"Los Angeles (Sharktech)", "https://laxspeed.sharktech.net/backend/garbage.php", "https://laxspeed.sharktech.net/backend/empty.php", "https://laxspeed.sharktech.net/backend/empty.php"},
	{"Las Vegas (Sharktech)", "https://lasspeed.sharktech.net/backend/garbage.php", "https://lasspeed.sharktech.net/backend/empty.php", "https://lasspeed.sharktech.net/backend/empty.php"},
	{"Denver (Sharktech)", "https://denspeed.sharktech.net/backend/garbage.php", "https://denspeed.sharktech.net/backend/empty.php", "https://denspeed.sharktech.net/backend/empty.php"},
	{"Chicago (Sharktech)", "https://chispeed.sharktech.net/backend/garbage.php", "https://chispeed.sharktech.net/backend/empty.php", "https://chispeed.sharktech.net/backend/empty.php"},
	{"Atlanta (Clouvider)", "https://atl.speedtest.clouvider.net/backend/garbage.php", "https://atl.speedtest.clouvider.net/backend/empty.php", "https://atl.speedtest.clouvider.net/backend/empty.php"},
	{"New York (Clouvider)", "https://nyc.speedtest.clouvider.net/backend/garbage.php", "https://nyc.speedtest.clouvider.net/backend/empty.php", "https://nyc.speedtest.clouvider.net/backend/empty.php"},
}

const (
	lsDownStreams = 6
	lsUpStreams   = 3
	lsWarmup      = 3 * time.Second
	lsMeasure     = 10 * time.Second
	lsDownCkMB    = 100       // garbage.php ?ckSize=100 → 100MB; streams loop to sustain load
	lsUpBytes     = 4_000_000 // 4MB/POST (servers 413 above ~4–8MB); streams loop
)

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// testClient is tuned for parallel throughput: HTTP/1.1 only (one TCP connection
// per stream → independent congestion windows) with enough idle conns to reuse.
func testClient() *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     30 * time.Second,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{}, // disable HTTP/2
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}
}

// pingTimeout bounds every latency probe so a blackholed server can't stall
// server selection, the idle baseline, or the loaded-latency sampling loop.
const pingTimeout = 3 * time.Second

// latencyMs times a GET to url — an HTTP round-trip over a (kept-alive) conn,
// bounded by pingTimeout.
func latencyMs(client *http.Client, url string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return float64(time.Since(t0).Microseconds()) / 1000
}

// pickServer measures latency to every pre-loaded server IN PARALLEL and returns
// the closest reachable one (west-coast entries naturally win for a west-coast
// user; index 0 is the fallback when nothing answers).
func pickServer(client *http.Client) speedServer {
	pings := make([]float64, len(libreServers))
	var wg sync.WaitGroup
	for i, s := range libreServers {
		i, s := i, s
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := math.Inf(1)
			for j := 0; j < 2; j++ {
				if v := latencyMs(client, s.Ping); v > 0 && v < m {
					m = v
				}
			}
			pings[i] = m
		}()
	}
	wg.Wait()
	best := 0
	for i := 1; i < len(pings); i++ {
		if pings[i] < pings[best] {
			best = i
		}
	}
	return libreServers[best]
}

// idleLatency warms the connection, then takes the median of many quiet samples
// (so a single cold/TLS-setup sample can't inflate the idle baseline).
func idleLatency(client *http.Client, pingURL string) float64 {
	for i := 0; i < 3; i++ {
		_ = latencyMs(client, pingURL)
	}
	var s []float64
	for i := 0; i < 15; i++ {
		if m := latencyMs(client, pingURL); m > 0 {
			s = append(s, m)
		}
	}
	return median(s)
}

type countWriter struct{ n *int64 }

func (w countWriter) Write(p []byte) (int, error) {
	atomic.AddInt64(w.n, int64(len(p)))
	return len(p), nil
}

type countReader struct {
	r io.Reader
	n *int64
}

func (r countReader) Read(p []byte) (int, error) {
	k, err := r.r.Read(p)
	atomic.AddInt64(r.n, int64(k))
	return k, err
}

// zeroReader yields n zero bytes then EOF (the upload payload, sized per request).
type zeroReader struct{ n int }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	k := len(p)
	if k > z.n {
		k = z.n
	}
	for i := 0; i < k; i++ {
		p[i] = 0
	}
	z.n -= k
	return k, nil
}

// streamThroughput runs `streams` parallel transfers (each calling do, which adds
// the bytes it moves to the shared counter) for lsWarmup+lsMeasure, sampling
// latency at pingURL during the measurement window. Returns steady-state Mbit/s
// and the median loaded latency. In-flight transfers are cancelled the moment the
// window ends (a stream mid-100MB-chunk on a slow link would otherwise block the
// phase for minutes and burn the shared ctx budget).
func streamThroughput(ctx context.Context, client *http.Client, streams int, pingURL string, do func(ctx context.Context, total *int64) error) (float64, float64, error) {
	phaseCtx, cancelPhase := context.WithCancel(ctx)
	defer cancelPhase()

	var total, completions int64
	var errMu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if phaseCtx.Err() != nil {
					return
				}
				err := do(phaseCtx, &total)
				switch {
				case phaseCtx.Err() != nil:
					return // cancelled mid-transfer at window end — not a real failure
				case err != nil:
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					select {
					case <-phaseCtx.Done():
						return
					case <-time.After(150 * time.Millisecond):
					}
				default:
					atomic.AddInt64(&completions, 1)
				}
			}
		}()
	}

	select {
	case <-time.After(lsWarmup):
	case <-phaseCtx.Done():
	}
	b0 := atomic.LoadInt64(&total)
	t0 := time.Now()
	var lat []float64
	deadline := time.Now().Add(lsMeasure)
	for time.Now().Before(deadline) && phaseCtx.Err() == nil {
		if m := latencyMs(client, pingURL); m > 0 {
			lat = append(lat, m)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-phaseCtx.Done():
			deadline = time.Now()
		}
	}
	b1 := atomic.LoadInt64(&total)
	dt := time.Since(t0).Seconds()
	cancelPhase() // abort in-flight chunks immediately; measured bytes are already counted
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	// No request ever completed cleanly → surface the failure even if some bytes
	// dribbled in mid-flight (a near-zero reading with no error would mislead).
	if atomic.LoadInt64(&completions) == 0 && err != nil {
		return 0, median(lat), err
	}
	if b1-b0 == 0 || dt <= 0 {
		return 0, median(lat), err
	}
	return float64(b1-b0) * 8 / 1e6 / dt, median(lat), nil
}

func lsDownload(ctx context.Context, client *http.Client, srv speedServer) (float64, float64, error) {
	url := fmt.Sprintf("%s?ckSize=%d", srv.DLURL, lsDownCkMB)
	return streamThroughput(ctx, client, lsDownStreams, srv.Ping, func(ctx context.Context, total *int64) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, resp.Body)
			return fmt.Errorf("download status %d", resp.StatusCode)
		}
		_, _ = io.Copy(countWriter{total}, resp.Body)
		return nil
	})
}

func lsUpload(ctx context.Context, client *http.Client, srv speedServer) (float64, float64, error) {
	return streamThroughput(ctx, client, lsUpStreams, srv.Ping, func(ctx context.Context, total *int64) error {
		body := countReader{r: &zeroReader{n: lsUpBytes}, n: total}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.ULURL, body)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = int64(lsUpBytes)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("upload status %d", resp.StatusCode)
		}
		return nil
	})
}

// defaultInternetDeps auto-selects the nearest LibreSpeed server and wires the
// real measurements against it.
func defaultInternetDeps(_ string) internetDeps {
	client := testClient()
	srv := pickServer(client)
	return internetDeps{
		endpoint: "LibreSpeed · " + srv.Name,
		idle:     func() float64 { return idleLatency(client, srv.Ping) },
		download: func(ctx context.Context) (float64, float64, error) { return lsDownload(ctx, client, srv) },
		upload:   func(ctx context.Context) (float64, float64, error) { return lsUpload(ctx, client, srv) },
	}
}

// --- endpoint + orchestration ---

// internetHandler runs a local internet test and returns the result.
func internetHandler(do func(endpoint string) InternetResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Endpoint string `json:"endpoint"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(do(req.Endpoint))
	}
}

func postInternet(client *http.Client, baseURL, endpoint string) (InternetResult, error) {
	body, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	resp, err := client.Post(baseURL+"/api/internet", "application/json", bytes.NewReader(body))
	if err != nil {
		return InternetResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return InternetResult{}, fmt.Errorf("internet: status %d", resp.StatusCode)
	}
	var out InternetResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return InternetResult{}, err
	}
	return out, nil
}

// runLocalInternet logs the test, opens a heatmap window, runs the local
// measurement, and closes the window. Used by InternetTest and the endpoint.
func (a *App) runLocalInternet(endpoint string) InternetResult {
	a.recordTestEvent("internet test (" + endpoint + ")")
	done := a.markTestSpan("internet test", 0)
	res := a.localInternet(endpoint)
	done()
	return res
}

// InternetTest runs the internet test on `node` (locally if it is this device,
// else over the peer's control plane) and returns the graded result.
func (a *App) InternetTest(node PeerInfo, endpoint string) InternetResult {
	if node.ID == a.nodeID || node.ID == "" {
		return a.runLocalInternet(endpoint)
	}
	out, err := a.FetchInternet("http://"+node.Addr, endpoint)
	if err != nil {
		return InternetResult{Endpoint: endpoint, Err: err.Error()}
	}
	return out
}
