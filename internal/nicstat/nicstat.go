// Package nicstat reports per-adapter NIC health (link speed/status,
// power-saving/EEE properties, and error/discard counters) to support NIC fault
// diagnosis.
package nicstat

import (
	"bytes"
	"encoding/json"
)

// NIC is one network adapter's current state + cumulative counters.
type NIC struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	LinkSpeed   string `json:"LinkSpeed"`
	Status      string `json:"Status"`
	RxErrors    int64  `json:"RxErrors"`
	RxDiscards  int64  `json:"RxDiscards"`
	TxErrors    int64  `json:"TxErrors"`
	TxDiscards  int64  `json:"TxDiscards"`
	RxBytes     int64  `json:"RxBytes"`
	TxBytes     int64  `json:"TxBytes"`
	// Power is every power-saving advanced property the adapter exposes
	// (EEE, Green Ethernet, Gigabit Lite, …), joined as "Name=Value; Name=Value".
	// A single field avoids PowerShell's single-element-array JSON quirk; empty
	// when the adapter reports no such properties (e.g. Wi-Fi).
	Power string `json:"Power"`
	// Detail is a per-link human summary the platform can fill: on macOS,
	// Wi-Fi radio state ("802.11ax · ch 40 (5 GHz, 160 MHz) · RSSI −45 dBm…")
	// or wired duplex. Optional and additive: older peers ignore it.
	Detail string `json:"Detail,omitempty"`
}

// parseNICs decodes the PowerShell JSON, tolerating both a JSON array (multiple
// adapters) and a bare object (a single adapter — ConvertTo-Json's quirk).
func parseNICs(data []byte) ([]NIC, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	if data[0] == '[' {
		var nics []NIC
		if err := json.Unmarshal(data, &nics); err != nil {
			return nil, err
		}
		return nics, nil
	}
	var one NIC
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, err
	}
	return []NIC{one}, nil
}
