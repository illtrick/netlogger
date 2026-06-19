package appcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
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
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
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

const cfBase = "https://speed.cloudflare.com"

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// cfLatencyMs times a zero-byte request — an HTTP round-trip to the endpoint.
func cfLatencyMs(client *http.Client) float64 {
	t0 := time.Now()
	resp, err := client.Get(cfBase + "/__down?bytes=0")
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return float64(time.Since(t0).Microseconds()) / 1000
}

// sampleUnderLoad runs work while sampling latency every 250ms; returns work's
// result and the median loaded latency.
func sampleUnderLoad(client *http.Client, work func() float64) (float64, float64) {
	stop := make(chan struct{})
	var mu sync.Mutex
	var samples []float64
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if m := cfLatencyMs(client); m > 0 {
					mu.Lock()
					samples = append(samples, m)
					mu.Unlock()
				}
			}
		}
	}()
	res := work()
	close(stop)
	mu.Lock()
	defer mu.Unlock()
	return res, median(samples)
}

func cfDownload(ctx context.Context, client *http.Client) (float64, float64, error) {
	const bytesN = 25_000_000
	var n int64
	var err error
	mbit, loaded := sampleUnderLoad(client, func() float64 {
		t0 := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfBase+"/__down?bytes="+strconv.Itoa(bytesN), nil)
		var resp *http.Response
		resp, err = client.Do(req)
		if err != nil {
			return 0
		}
		n, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		dt := time.Since(t0).Seconds()
		if dt <= 0 {
			return 0
		}
		return float64(n) * 8 / 1e6 / dt
	})
	return mbit, loaded, err
}

// zeroReader yields n zero bytes then EOF (the upload payload).
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
	const bytesN = 10_000_000
	var err error
	mbit, loaded := sampleUnderLoad(client, func() float64 {
		t0 := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfBase+"/__up", &zeroReader{n: bytesN})
		req.Header.Set("Content-Type", "application/octet-stream")
		var resp *http.Response
		resp, err = client.Do(req)
		if err != nil {
			return 0
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		dt := time.Since(t0).Seconds()
		if dt <= 0 {
			return 0
		}
		return float64(bytesN) * 8 / 1e6 / dt
	})
	return mbit, loaded, err
}

// defaultInternetDeps wires the real Cloudflare measurements.
func defaultInternetDeps(endpoint string) internetDeps {
	client := &http.Client{Timeout: 30 * time.Second}
	return internetDeps{
		endpoint: endpoint,
		idle: func() float64 {
			var s []float64
			for i := 0; i < 5; i++ {
				if m := cfLatencyMs(client); m > 0 {
					s = append(s, m)
				}
			}
			return median(s)
		},
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
