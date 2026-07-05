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
				// Keep the vocabulary closed: the change-event detector compares
				// Status strings between polls, so a raw driver token would fire
				// spurious link-flap events.
				status = "Unknown"
			}
		}
	}
	return speed, status
}

// mediaToSpeed converts an ifconfig media token to the "<n> Gbps"/"<n> Mbps"
// vocabulary the Windows collector reports. Tokens that name no rate at all —
// bare "autoselect" (Wi-Fi with no resolved wired rate) and "<unknown type>" —
// map to "" so the Adapters speed column never shows a non-speed; other
// unknown tokens pass through (duplex suffix stripped) for diagnostic value.
func mediaToSpeed(media string) string {
	// Strip a trailing "<full-duplex>"-style option group first so tokens
	// like "autoselect <full-duplex>" (idle Thunderbolt ports) classify the
	// same as their bare forms. "<unknown type>" starts with '<', so only a
	// suffix at i > 0 is an option group.
	if i := strings.IndexByte(media, '<'); i > 0 {
		media = strings.TrimSpace(media[:i])
	}
	m := strings.ToLower(media)
	switch {
	case m == "none", m == "autoselect", m == "<unknown type>":
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
	// Unknown wired token: pass through (duplex already stripped) for
	// diagnostic value.
	return media
}

// linkCounters is one interface's cumulative counters from `netstat -ibnd`.
// TxDiscards carries the Drop column; netstat exposes no rx-side drop counter,
// so RxDiscards has no darwin source (Windows populates it from
// ReceivedDiscardedPackets).
type linkCounters struct {
	RxErrors, TxErrors, TxDiscards int64
	RxBytes, TxBytes               int64
}

// parseNetstatIB reads the `<Link#N>` row per interface from `netstat -ibnd`.
// Field layout after the <Link#N> token: [mac?] Ipkts Ierrs Ibytes Opkts
// Oerrs Obytes Coll Drop — the MAC column is absent for interfaces without
// one (lo0), so it is skipped by shape (contains ':'). Drop is BSD's
// output-queue drop counter (if_snd drops), i.e. TX-side.
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
			TxDiscards: n(7),
		}
	}
	return res
}

// splitIfconfigSections splits `ifconfig -a -v` output into one text block per
// interface, keyed by device name. Section headers are unindented
// "name: flags=..." lines; continuation lines are indented. Lets Collect run
// one ifconfig for all interfaces instead of one per device.
func splitIfconfigSections(out string) map[string]string {
	res := map[string]string{}
	var name string
	var buf strings.Builder
	flush := func() {
		if name != "" {
			res[name] = buf.String()
		}
		buf.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			if i := strings.IndexByte(line, ':'); i > 0 {
				flush()
				name = line[:i]
			}
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	flush()
	return res
}
