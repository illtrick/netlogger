package appcore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// lossBucketsHandler serves this machine's per-link loss heatmap for a time range
// — the /api/lossbuckets endpoint peers pull to assemble the mesh-wide heatmap.
func lossBucketsHandler(heat func(from, to int64, bucket int) HeatView) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
		bucket, _ := strconv.Atoi(q.Get("bucket"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(heat(from, to, bucket))
	}
}

// fetchLossBuckets GETs a peer's /api/lossbuckets for the given absolute window.
func fetchLossBuckets(client *http.Client, baseURL string, from, to int64, bucket int) (HeatView, error) {
	url := fmt.Sprintf("%s/api/lossbuckets?from=%d&to=%d&bucket=%d", baseURL, from, to, bucket)
	resp, err := client.Get(url)
	if err != nil {
		return HeatView{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return HeatView{}, fmt.Errorf("lossbuckets: status %d", resp.StatusCode)
	}
	var v HeatView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return HeatView{}, fmt.Errorf("lossbuckets decode: %w", err)
	}
	return v, nil
}

// heatSyncLoop periodically pulls each peer's loss buckets for the window the UI
// most recently requested, caching them for LossHeatMesh to merge. The window is
// absolute and bucket-snapped, so every machine buckets against the same grid and
// rows align despite small clock drift (a sub-bucket shift at minute resolution).
func (a *App) heatSyncLoop(ctx context.Context) {
	defer a.wg.Done()
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.heatMu.Lock()
		from, to, bucket := a.heatFrom, a.heatTo, a.heatBucket
		a.heatMu.Unlock()
		if bucket == 0 { // the UI hasn't requested a heatmap yet
			continue
		}
		a.mu.Lock()
		disc := a.Discovery
		a.mu.Unlock()
		if disc == nil {
			continue
		}
		rows := map[string][]HeatRow{}
		for _, p := range disc.Peers() {
			if ctx.Err() != nil {
				return
			}
			if v, err := fetchLossBuckets(client, "http://"+p.Addr, from, to, bucket); err == nil {
				rows[p.Host] = v.Rows
			}
		}
		a.heatMu.Lock()
		a.heatPeerRows, a.heatPeerFrom, a.heatPeerBucket = rows, from, bucket
		a.heatMu.Unlock()
	}
}

// mergeHeat stacks this machine's loss rows and every peer's onto one view, each
// row prefixed with the host that measured it (self first, peers host-sorted).
func mergeHeat(selfHost string, local HeatView, peerRows map[string][]HeatRow) HeatView {
	view := HeatView{FromUnix: local.FromUnix, BucketSec: local.BucketSec, Buckets: local.Buckets}
	for _, r := range local.Rows {
		view.Rows = append(view.Rows, HeatRow{Label: selfHost + " · " + r.Label, Loss: r.Loss})
	}
	hosts := make([]string, 0, len(peerRows))
	for h := range peerRows {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		for _, r := range peerRows[h] {
			view.Rows = append(view.Rows, HeatRow{Label: h + " · " + r.Label, Loss: r.Loss})
		}
	}
	return view
}

// LossHeatMesh returns the mesh-wide loss heatmap: this machine's links plus every
// peer's, stacked on one absolute, bucket-snapped time axis. windowSec is how far
// back to show; the peer rows come from the background sync loop's cache.
func (a *App) LossHeatMesh(windowSec, bucketSec int) HeatView {
	if bucketSec <= 0 {
		bucketSec = 120
	}
	if windowSec <= 0 {
		windowSec = 24 * 3600
	}
	b := int64(bucketSec)
	toB := time.Now().Unix()/b + 1
	fromB := toB - int64(windowSec/bucketSec)
	from, to := fromB*b, toB*b

	a.heatMu.Lock()
	a.heatFrom, a.heatTo, a.heatBucket = from, to, bucketSec
	var peerRows map[string][]HeatRow
	if a.heatPeerFrom == from && a.heatPeerBucket == bucketSec {
		peerRows = a.heatPeerRows
	}
	a.heatMu.Unlock()

	return mergeHeat(a.hostName(), a.LossHeat(from, to, bucketSec), peerRows)
}
