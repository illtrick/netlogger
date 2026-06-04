package mesh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// clampUS is the maximum believable offset; beyond this the agent clock is
// treated as broken and excluded from correlation (spec §6).
const clampUS = 30_000_000 // 30s

// Offset is a measured clock offset between an agent and the coordinator.
type Offset struct {
	OffsetUS int64 // agent_clock - coordinator_clock, microseconds
	RTTus    int64 // the min round-trip delay (δ) observed
	Reliable bool  // false if |offset| exceeds the clamp
}

// HalfUncUS is the clock-uncertainty half-width contributed by this offset
// (δ/2), used to widen correlation intervals.
func (o Offset) HalfUncUS() int64 { return o.RTTus / 2 }

// MeasureOffset runs n round-trips to baseURL/api/time and returns the offset
// from the sample with the smallest delay (least queuing — most trustworthy).
func MeasureOffset(client *http.Client, baseURL string, n int) (Offset, error) {
	if n < 1 {
		n = 1
	}
	best := Offset{RTTus: 1 << 62}
	var got bool
	for i := 0; i < n; i++ {
		t1 := time.Now().UTC().UnixMicro()
		resp, err := client.Get(baseURL + "/api/time")
		t4 := time.Now().UTC().UnixMicro()
		if err != nil {
			return Offset{}, err
		}
		var tp TimePair
		derr := json.NewDecoder(resp.Body).Decode(&tp)
		resp.Body.Close()
		if derr != nil {
			return Offset{}, derr
		}
		delta := (t4 - t1) - (tp.T3UnixUS - tp.T2UnixUS)
		offset := ((tp.T2UnixUS - t1) + (tp.T3UnixUS - t4)) / 2
		if delta < best.RTTus {
			best = Offset{OffsetUS: offset, RTTus: delta}
			got = true
		}
	}
	if !got {
		return Offset{}, fmt.Errorf("no offset samples")
	}
	abs := best.OffsetUS
	if abs < 0 {
		abs = -abs
	}
	best.Reliable = abs <= clampUS
	return best, nil
}
