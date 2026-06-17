package netmodel

import "strings"

// Agent is what an auto-discovered NetLogger instance reports about itself, used
// to prefill its device in the config without clobbering user-entered topology.
type Agent struct {
	NodeUUID   string
	Name       string
	Interfaces []Interface
}

// MergeAgent folds a discovered agent into the config: it finds the matching
// device (by NodeUUID, falling back to a case-insensitive Name), marks it an
// agent, and refreshes each interface's discovered fields (adapter/MAC/IP/speed/
// ssid/band) while preserving the user's Via/Link/Monitor. An unmatched agent is
// appended as a new pc device for the user to place.
func MergeAgent(c Config, a Agent) Config {
	idx := -1
	for i, d := range c.Devices {
		if a.NodeUUID != "" && d.NodeUUID == a.NodeUUID {
			idx = i
			break
		}
	}
	if idx < 0 {
		for i, d := range c.Devices {
			if strings.EqualFold(d.Name, a.Name) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		nd := Device{ID: slug(a.Name), Name: a.Name, Type: TypePC, Agent: true, NodeUUID: a.NodeUUID}
		nd.Interfaces = append(nd.Interfaces, a.Interfaces...)
		c.Devices = append(c.Devices, nd)
		return c
	}

	d := c.Devices[idx]
	d.Agent = true
	if a.NodeUUID != "" {
		d.NodeUUID = a.NodeUUID
	}
	for _, di := range a.Interfaces {
		mi := matchInterface(d.Interfaces, di)
		if mi < 0 {
			d.Interfaces = append(d.Interfaces, di)
			continue
		}
		// refresh discovered fields; preserve user topology choices
		cur := d.Interfaces[mi]
		cur.Medium = di.Medium
		if di.Adapter != "" {
			cur.Adapter = di.Adapter
		}
		if di.MAC != "" {
			cur.MAC = di.MAC
		}
		cur.IP = di.IP
		if di.Speed != "" {
			cur.Speed = di.Speed
		}
		if di.SSID != "" {
			cur.SSID = di.SSID
		}
		if di.Band != "" {
			cur.Band = di.Band
		}
		d.Interfaces[mi] = cur
	}
	c.Devices[idx] = d
	return c
}

// matchInterface finds the existing interface a discovered one updates: by MAC
// if known, else the first interface of the same medium.
func matchInterface(existing []Interface, di Interface) int {
	if di.MAC != "" {
		for i, e := range existing {
			if strings.EqualFold(e.MAC, di.MAC) {
				return i
			}
		}
	}
	for i, e := range existing {
		if e.Medium == di.Medium {
			return i
		}
	}
	return -1
}

func slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		s = "device"
	}
	return s
}
