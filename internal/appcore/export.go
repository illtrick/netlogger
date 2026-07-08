package appcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"netlogger/internal/store"
)

// ExportBundle is a self-contained diagnostic snapshot for off-box analysis.
//
// Build / BuildWarning / Peers[].Build let an analyst verify every machine ran
// the same binary. MeshEvents is the merged, host-tagged timeline (this machine
// + every peer's pulled events) so cross-machine correlation works from a single
// export; Events remains this node's full local connectivity log from the store.
type ExportBundle struct {
	GeneratedUnix    int64             `json:"generated_unix"`
	NodeID           string            `json:"node_id"`
	Host             string            `json:"host"`
	Build            string            `json:"build"`
	BuildWarning     string            `json:"build_warning,omitempty"`
	SessionUptimeSec int64             `json:"session_uptime_sec"`
	GatewayIP        string            `json:"gateway_ip"`
	InternetIP       string            `json:"internet_ip"`
	Peers            []PeerInfo        `json:"peers"`
	Matrix           []MatrixCell      `json:"matrix"`
	MeshEvents       []MergedEvent     `json:"mesh_events"`
	Events           []store.ConnEvent `json:"events"`
	NICs             []NICInfo         `json:"nics"`
	SampleCount      int               `json:"sample_count"`
	// Recent test history (newest first) so an analyst sees throughput trends.
	InternetTests []store.TestResult `json:"internet_tests,omitempty"`
	SweepTests    []store.TestResult `json:"sweep_tests,omitempty"`
	StressTests   []store.TestResult `json:"stress_tests,omitempty"`
}

// Export builds a bundle from the current snapshot + store. unixNow is injected
// (the app passes time.Now().Unix()) so tests are deterministic.
func (a *App) Export(unixNow int64) ExportBundle {
	snap := a.Snapshot()
	var cells []MatrixCell
	for _, src := range snap.Matrix.Nodes {
		for _, dst := range snap.Matrix.Nodes {
			if c, ok := snap.Matrix.Cell(src.ID, dst.ID); ok {
				cells = append(cells, c)
			}
		}
	}
	var events []store.ConnEvent
	var count int
	if a.store != nil {
		events, _ = a.store.ConnectivityEvents(a.NodeID())
		if ss, err := a.store.Since(0, 1000000); err == nil {
			count = len(ss)
		}
	}
	return ExportBundle{
		GeneratedUnix:    unixNow,
		NodeID:           a.NodeID(),
		Host:             a.hostName(),
		Build:            snap.Build,
		BuildWarning:     snap.BuildWarning,
		SessionUptimeSec: snap.SessionUptimeSec,
		GatewayIP:        snap.GatewayIP,
		InternetIP:       snap.InternetIP,
		Peers:            snap.Peers,
		Matrix:           cells,
		MeshEvents:       snap.Events,
		Events:           events,
		NICs:             snap.NICs,
		SampleCount:      count,
		InternetTests:    a.TestHistory("internet", 50),
		SweepTests:       a.TestHistory("sweep", 50),
		StressTests:      a.TestHistory("stress", 50),
	}
}

// WriteExport writes b as indented JSON to dir, returning the file path.
func WriteExport(dir string, b ExportBundle) (string, error) {
	name := fmt.Sprintf("netlogger-export-%d.json", b.GeneratedUnix)
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
