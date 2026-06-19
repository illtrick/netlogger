package appcore

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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

// internetDeps are the injectable network measurements (real impls hit Cloudflare).
type internetDeps struct {
	endpoint string
	idle     func() float64 // a quiet-line latency sample (ms)
	download func(ctx context.Context) (mbit, loadedMs float64, err error)
	upload   func(ctx context.Context) (mbit, loadedMs float64, err error)
}

// runInternet runs the three phases (idle → download → upload) and grades the
// result. Pure orchestration over the injected measurements.
func runInternet(d internetDeps) InternetResult {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	}
	return res
}

// --- real Cloudflare measurements (manual-gated; behind the seams above) ---
//
// Accuracy on fast links depends on: PARALLEL streams (one TCP connection each, so
// each gets its own congestion window — HTTP/2 multiplexing over one conn would
// cap us at a single window), a WARM-UP window discarded before measuring, and a
// multi-second STEADY-STATE measurement window.

const (
	cfBase        = "https://speed.cloudflare.com"
	cfDownStreams = 6
	cfUpStreams   = 3
	cfWarmup      = 3 * time.Second
	cfMeasure     = 10 * time.Second
	cfDownBytes   = 100_000_000 // per request; streams loop until the window ends
	cfUpBytes     = 50_000_000
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
		// Disable HTTP/2 so each request opens its own TCP connection.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}
}

// latencyMs times a tiny request — an HTTP round-trip over a (kept-alive) conn.
func latencyMs(client *http.Client) float64 {
	t0 := time.Now()
	resp, err := client.Get(cfBase + "/__down?bytes=0")
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return float64(time.Since(t0).Microseconds()) / 1000
}

// idleLatency warms the connection, then takes the median of many quiet samples
// (so a single cold/TLS-setup sample can't inflate the idle baseline).
func idleLatency(client *http.Client) float64 {
	for i := 0; i < 3; i++ {
		_ = latencyMs(client) // warm TLS + conn
	}
	var s []float64
	for i := 0; i < 15; i++ {
		if m := latencyMs(client); m > 0 {
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

// streamThroughput runs `streams` parallel transfers (each calling do, which must
// add the bytes it moves to the shared counter) for cfWarmup+cfMeasure. It returns
// the steady-state Mbit/s over the measurement window and the median latency
// sampled during it (loaded latency).
func streamThroughput(ctx context.Context, client *http.Client, streams int, do func(ctx context.Context, total *int64) error) (float64, float64, error) {
	var total int64
	var firstErr atomic.Value
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				default:
				}
				if err := do(ctx, &total); err != nil {
					if firstErr.Load() == nil {
						firstErr.Store(err)
					}
					select {
					case <-done:
						return
					case <-ctx.Done():
						return
					case <-time.After(150 * time.Millisecond):
					}
				}
			}
		}()
	}

	// warm-up (discarded), then measure steady-state while sampling loaded latency.
	select {
	case <-time.After(cfWarmup):
	case <-ctx.Done():
	}
	b0 := atomic.LoadInt64(&total)
	t0 := time.Now()
	var lat []float64
	deadline := time.Now().Add(cfMeasure)
	for time.Now().Before(deadline) {
		if m := latencyMs(client); m > 0 {
			lat = append(lat, m)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			deadline = time.Now()
		}
	}
	b1 := atomic.LoadInt64(&total)
	dt := time.Since(t0).Seconds()
	close(done)
	wg.Wait()

	if b1-b0 == 0 {
		if e, ok := firstErr.Load().(error); ok {
			return 0, 0, e
		}
		return 0, median(lat), nil
	}
	mbit := float64(b1-b0) * 8 / 1e6 / dt
	return mbit, median(lat), nil
}

func cfDownload(ctx context.Context, client *http.Client) (float64, float64, error) {
	return streamThroughput(ctx, client, cfDownStreams, func(ctx context.Context, total *int64) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/__down?bytes=%d", cfBase, cfDownBytes), nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(countWriter{total}, resp.Body)
		resp.Body.Close()
		return nil
	})
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

func cfUpload(ctx context.Context, client *http.Client) (float64, float64, error) {
	return streamThroughput(ctx, client, cfUpStreams, func(ctx context.Context, total *int64) error {
		body := countReader{r: &zeroReader{n: cfUpBytes}, n: total}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfBase+"/__up", body)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = int64(cfUpBytes)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	})
}

// defaultInternetDeps wires the real Cloudflare measurements.
func defaultInternetDeps(endpoint string) internetDeps {
	client := testClient()
	return internetDeps{
		endpoint: endpoint,
		idle:     func() float64 { return idleLatency(client) },
		download: func(ctx context.Context) (float64, float64, error) { return cfDownload(ctx, client) },
		upload:   func(ctx context.Context) (float64, float64, error) { return cfUpload(ctx, client) },
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
	if endpoint == "" {
		endpoint = "Cloudflare"
	}
	if node.ID == a.nodeID || node.ID == "" {
		return a.runLocalInternet(endpoint)
	}
	out, err := a.FetchInternet("http://"+node.Addr, endpoint)
	if err != nil {
		return InternetResult{Endpoint: endpoint, Err: err.Error()}
	}
	return out
}
