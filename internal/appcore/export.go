package appcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"netlogger/internal/store"
	"netlogger/internal/sysinfo"
)

// ExportBundle is a self-contained diagnostic snapshot for off-box analysis.
type ExportBundle struct {
	GeneratedUnix    int64             `json:"generated_unix"`
	NodeID           string            `json:"node_id"`
	Host             string            `json:"host"`
	SessionUptimeSec int64             `json:"session_uptime_sec"`
	GatewayIP        string            `json:"gateway_ip"`
	InternetIP       string            `json:"internet_ip"`
	Peers            []PeerInfo        `json:"peers"`
	Matrix           []MatrixCell      `json:"matrix"`
	Events           []store.ConnEvent `json:"events"`
	NICs             []sysinfo.NIC     `json:"nics"`
	SampleCount      int               `json:"sample_count"`
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
		SessionUptimeSec: snap.SessionUptimeSec,
		GatewayIP:        snap.GatewayIP,
		InternetIP:       snap.InternetIP,
		Peers:            snap.Peers,
		Matrix:           cells,
		Events:           events,
		NICs:             sysinfo.NICCounters(),
		SampleCount:      count,
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
