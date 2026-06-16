// Package nicstat reports per-adapter NIC health (link speed/status, EEE state,
// and error/discard counters) to support NIC/EEE fault diagnosis.
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
	EEE         string `json:"EEE"` // "Enabled"/"Disabled"/"" (advanced-property value)
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
