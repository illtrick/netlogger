//go:build darwin

package nicstat

import (
	"os/exec"
	"sort"
)

// Collect gathers per-adapter state on macOS from networksetup (names +
// physical-port filter), ifconfig (speed/status), and netstat (counters).
// EEE/power-saving properties are not exposed by macOS; Power stays empty.
func Collect() []NIC {
	ports := runParse("networksetup", []string{"-listallhardwareports"}, parseHardwarePorts)
	if len(ports) == 0 {
		return nil
	}
	counters := runParse("netstat", []string{"-ibnd"}, parseNetstatIB)

	devs := make([]string, 0, len(ports))
	for dev := range ports {
		devs = append(devs, dev)
	}
	sort.Strings(devs) // stable order for the UI and change detection

	var nics []NIC
	for _, dev := range devs {
		out, err := exec.Command("ifconfig", "-v", dev).Output()
		if err != nil {
			continue // port exists but interface is gone (e.g. unplugged adapter)
		}
		speed, status := parseIfconfig(string(out))
		n := NIC{
			Name:        dev,
			Description: ports[dev],
			LinkSpeed:   speed,
			Status:      status,
		}
		if c, ok := counters[dev]; ok {
			n.RxErrors, n.TxErrors = c.RxErrors, c.TxErrors
			n.RxDiscards = c.RxDiscards
			n.RxBytes, n.TxBytes = c.RxBytes, c.TxBytes
		}
		nics = append(nics, n)
	}
	return nics
}

// runParse runs a command and applies a pure parser, returning the zero map
// on any error (NIC diagnostics degrade, never fail the app).
func runParse[T any](name string, args []string, parse func(string) T) T {
	var zero T
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return zero
	}
	return parse(string(out))
}
