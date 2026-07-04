package nicstat

// macOS NIC parsing: pure functions over the text output of networksetup,
// ifconfig, and netstat. Untagged so the fixtures test on every dev OS; the
// darwin-only Collect glue lives in nicstat_darwin.go.

import (
	"strconv"
	"strings"
)

// parseHardwarePorts maps device name → human port name from
// `networksetup -listallhardwareports`. Doubles as the interface allowlist:
// devices absent from it (lo0, utun*, awdl0, …) are not physical ports.
func parseHardwarePorts(out string) map[string]string {
	ports := map[string]string{}
	var port string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Hardware Port: "); ok {
			port = v
			continue
		}
		if v, ok := strings.CutPrefix(line, "Device: "); ok && port != "" {
			ports[v] = port
			port = ""
		}
	}
	return ports
}

// parseIfconfig extracts (link speed, status) from `ifconfig -v <dev>`.
// Status maps to the Windows Get-NetAdapter vocabulary the UI and the NIC
// change-event detector already understand: "Up" / "Disconnected" / "Unknown".
func parseIfconfig(out string) (speed, status string) {
	status = "Unknown"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "media: "); ok {
			// "autoselect (1000baseT <full-duplex>)" → inner "1000baseT <full-duplex>"
			if i := strings.IndexByte(v, '('); i >= 0 {
				if j := strings.IndexByte(v[i:], ')'); j > 0 {
					v = v[i+1 : i+j]
				}
			}
			speed = mediaToSpeed(v)
		}
		if v, ok := strings.CutPrefix(line, "status: "); ok {
			switch v {
			case "active":
				status = "Up"
			case "inactive":
				status = "Disconnected"
			default:
				status = v
			}
		}
	}
	return speed, status
}

// mediaToSpeed converts an ifconfig media token to the "<n> Gbps"/"<n> Mbps"
// vocabulary the Windows collector reports; unknown tokens pass through raw.
func mediaToSpeed(media string) string {
	m := strings.ToLower(media)
	switch {
	case m == "none":
		return ""
	case strings.HasPrefix(m, "10gbase"):
		return "10 Gbps"
	case strings.HasPrefix(m, "5000base"):
		return "5 Gbps"
	case strings.HasPrefix(m, "2500base"):
		return "2.5 Gbps"
	case strings.HasPrefix(m, "1000base"):
		return "1 Gbps"
	case strings.HasPrefix(m, "100base"):
		return "100 Mbps"
	case strings.HasPrefix(m, "10base"):
		return "10 Mbps"
	}
	// Strip the duplex suffix for readability on unknown tokens.
	if i := strings.IndexByte(media, '<'); i > 0 {
		media = strings.TrimSpace(media[:i])
	}
	return media
}

// linkCounters is one interface's cumulative counters from `netstat -ibnd`.
type linkCounters struct {
	RxErrors, TxErrors, RxDiscards int64
	RxBytes, TxBytes               int64
}

// parseNetstatIB reads the `<Link#N>` row per interface from `netstat -ibnd`.
// Field layout after the <Link#N> token: [mac?] Ipkts Ierrs Ibytes Opkts
// Oerrs Obytes Coll Drop — the MAC column is absent for interfaces without
// one (lo0), so it is skipped by shape (contains ':').
func parseNetstatIB(out string) map[string]linkCounters {
	res := map[string]linkCounters{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		link := -1
		for i, tok := range f {
			if strings.HasPrefix(tok, "<Link#") {
				link = i
				break
			}
		}
		if link < 0 || len(f) < link+8 {
			continue
		}
		j := link + 1
		if j < len(f) && strings.Contains(f[j], ":") {
			j++ // skip MAC
		}
		if len(f) < j+8 {
			continue
		}
		n := func(k int) int64 {
			v, _ := strconv.ParseInt(f[j+k], 10, 64)
			return v
		}
		res[f[0]] = linkCounters{
			RxErrors:   n(1),
			RxBytes:    n(2),
			TxErrors:   n(4),
			TxBytes:    n(5),
			RxDiscards: n(7),
		}
	}
	return res
}
