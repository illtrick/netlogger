package netmodel

// Tier is one row of the connector-less topology.
type Tier struct {
	Name    string
	Devices []Device
}

func tierIndex(t DeviceType) int {
	switch t {
	case TypeInternet:
		return 0
	case TypeModem, TypeRouter:
		return 1
	case TypeSwitch, TypeAP:
		return 2
	default:
		return 3
	}
}

// Tiers groups devices into the four display rows, preserving config order.
func Tiers(c Config) []Tier {
	names := []string{"Internet", "Gateway", "Switches", "Devices"}
	tiers := make([]Tier, len(names))
	for i, n := range names {
		tiers[i].Name = n
	}
	for _, d := range c.Devices {
		i := tierIndex(d.Type)
		tiers[i].Devices = append(tiers[i].Devices, d)
	}
	return tiers
}

// Target is a device link the engine should ping.
type Target struct {
	DeviceID string
	Name     string
	IP       string
}

// MonitorTargets returns every interface flagged monitor with a non-empty IP.
func MonitorTargets(c Config) []Target {
	var out []Target
	for _, d := range c.Devices {
		for _, ifc := range d.Interfaces {
			if ifc.Monitor && ifc.IP != "" {
				out = append(out, Target{DeviceID: d.ID, Name: d.Name, IP: ifc.IP})
			}
		}
	}
	return out
}
