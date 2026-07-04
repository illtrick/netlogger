// Package probe implements the network probes (ICMP baseline, isochronous UDP).
package probe

import (
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// Result is the outcome of a single ICMP probe.
type Result struct {
	RTT  time.Duration
	Lost bool
}

// PingICMP sends one ICMP echo to addr and returns its RTT, or Lost=true on
// timeout. On Windows, privileged mode uses IcmpSendEcho and needs no admin.
func PingICMP(addr string, timeout time.Duration) (Result, error) {
	pinger, err := probing.NewPinger(addr)
	if err != nil {
		return Result{}, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(privilegedICMP)
	if err := pinger.Run(); err != nil {
		return Result{}, err
	}
	st := pinger.Statistics()
	if st.PacketsRecv == 0 {
		return Result{Lost: true}, nil
	}
	return Result{RTT: st.AvgRtt}, nil
}
