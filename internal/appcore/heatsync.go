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
		case <-a.heatKick:
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

// MachineRow is one machine's health timeline: per bucket, the worst severity
// across all of that machine's links/NIC (Sev: loss%, 100 = NIC reset, -1 = no
// data) plus a human Detail of what failed (empty when clean).
type MachineRow struct {
	Host   string
	Sev    []float64
	Detail []string
}

// MeshHeat is the per-machine heatmap: one row per machine on a shared time axis.
type MeshHeat struct {
	FromUnix  int64
	BucketSec int
	Buckets   int
	Machines  []MachineRow
}

// collapseMachine folds a machine's per-link rows into one row: each bucket takes
// the worst severity across its links, and a detail string naming the problems.
func collapseMachine(host string, rows []HeatRow, buckets int) MachineRow {
	m := MachineRow{Host: host, Sev: make([]float64, buckets), Detail: make([]string, buckets)}
	for i := range m.Sev {
		m.Sev[i] = -1
	}
	for _, r := range rows {
		for b := 0; b < buckets && b < len(r.Loss); b++ {
			v := r.Loss[b]
			if v < 0 {
				continue // this link has no data in this bucket
			}
			if m.Sev[b] < 0 {
				m.Sev[b] = 0 // at least one link reporting → clean unless a worse value follows
			}
			if v > m.Sev[b] {
				m.Sev[b] = v
			}
			if v > 0 {
				pct := strconv.Itoa(int(v+0.5)) + "%"
				var piece string
				switch r.Label {
				case "NIC link":
					piece = "NIC link reset"
				case "Gateway":
					piece = "gateway loss " + pct
				case "Internet":
					piece = "internet loss " + pct
				default:
					piece = "loss → " + r.Label + " " + pct
				}
				if m.Detail[b] != "" {
					m.Detail[b] += ", "
				}
				m.Detail[b] += piece
			}
		}
	}
	return m
}

// heatWindow snaps the requested window to an absolute bucket grid, records it for
// the sync loop, and returns the local link rows plus any matching cached peer rows.
func (a *App) heatWindow(windowSec, bucketSec int) (from int64, bucket int, local HeatView, peerRows map[string][]HeatRow) {
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
	changed := a.heatFrom != from || a.heatBucket != bucketSec
	a.heatFrom, a.heatTo, a.heatBucket = from, to, bucketSec
	if a.heatPeerFrom == from && a.heatPeerBucket == bucketSec {
		peerRows = a.heatPeerRows
	}
	a.heatMu.Unlock()
	if changed { // window moved (scroll boundary or zoom) → pull peers now, don't wait for the tick
		select {
		case a.heatKick <- struct{}{}:
		default:
		}
	}
	return from, bucketSec, a.LossHeat(from, to, bucketSec), peerRows
}

// LossHeatByMachine returns the mesh heatmap as one collapsed row per machine
// (this machine first, peers host-sorted) on one absolute, bucket-snapped axis.
func (a *App) LossHeatByMachine(windowSec, bucketSec int) MeshHeat {
	from, bucket, local, peerRows := a.heatWindow(windowSec, bucketSec)
	mh := MeshHeat{FromUnix: from, BucketSec: bucket, Buckets: local.Buckets}
	mh.Machines = append(mh.Machines, collapseMachine(a.hostName(), local.Rows, local.Buckets))
	hosts := make([]string, 0, len(peerRows))
	for h := range peerRows {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		mh.Machines = append(mh.Machines, collapseMachine(h, peerRows[h], local.Buckets))
	}
	return mh
}
