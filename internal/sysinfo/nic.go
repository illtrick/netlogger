package sysinfo

import (
	"runtime"
	"strconv"
	"strings"
)

// NIC holds error/drop counters for one network interface.
type NIC struct {
	Name      string `json:"name"`
	RxErrors  int64  `json:"rx_errors"`
	RxDropped int64  `json:"rx_dropped"`
	TxErrors  int64  `json:"tx_errors"`
	TxDropped int64  `json:"tx_dropped"`
}

// parseProcNetDev parses Linux /proc/net/dev content. Columns after the iface
// name are: rx[bytes packets errs drop ...8], tx[bytes packets errs drop ...8].
func parseProcNetDev(content string) []NIC {
	var out []NIC
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		atoi := func(i int) int64 { v, _ := strconv.ParseInt(fields[i], 10, 64); return v }
		out = append(out, NIC{
			Name:      name,
			RxErrors:  atoi(2),
			RxDropped: atoi(3),
			TxErrors:  atoi(10),
			TxDropped: atoi(11),
		})
	}
	return out
}

// NICCounters returns per-interface error/drop counters (best-effort per OS).
// Linux is parsed from /proc/net/dev; other platforms return nil for now and
// are filled in during platform bring-up.
func NICCounters() []NIC {
	if runtime.GOOS == "linux" {
		if data, err := readFile("/proc/net/dev"); err == nil {
			return parseProcNetDev(data)
		}
	}
	return nil
}
