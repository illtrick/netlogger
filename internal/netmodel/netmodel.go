// Package netmodel is the portable description of the user's network: typed
// devices, each with one or more interfaces (wired/Wi-Fi) uniquely identified by
// MAC, an optional monitor flag (ping this link), and a `via` parent for the
// tier hierarchy. Persisted as JSON beside the executable.
package netmodel

import (
	"bytes"
	"encoding/json"
	"os"
)

type Medium string

const (
	MediumWired Medium = "wired"
	MediumWifi  Medium = "wifi"
)

type DeviceType string

const (
	TypeInternet DeviceType = "internet"
	TypeModem    DeviceType = "modem"
	TypeRouter   DeviceType = "router"
	TypeSwitch   DeviceType = "switch"
	TypeAP       DeviceType = "ap"
	TypeNAS      DeviceType = "nas"
	TypePC       DeviceType = "pc"
	TypeOther    DeviceType = "other"
)

// Interface is one physical/logical link on a device. MAC is the unique id; an
// IP is an attribute (DHCP-churn, multi-homed reuse), never the identity.
type Interface struct {
	ID      string `json:"id"`
	Medium  Medium `json:"medium"`
	Adapter string `json:"adapter,omitempty"`
	MAC     string `json:"mac,omitempty"`
	IP      string `json:"ip,omitempty"`
	Speed   string `json:"speed,omitempty"`
	Via     string `json:"via,omitempty"`  // parent device id
	Link    string `json:"link,omitempty"` // inline annotation, e.g. "UGREEN coupler"
	SSID    string `json:"ssid,omitempty"` // wifi only
	Band    string `json:"band,omitempty"` // wifi only
	Monitor bool   `json:"monitor,omitempty"`
}

type Device struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       DeviceType  `json:"type"`
	Model      string      `json:"model,omitempty"`
	Role       string      `json:"role,omitempty"`      // "core" | "access"
	Target     string      `json:"target,omitempty"`    // internet probe host
	NodeUUID   string      `json:"node_uuid,omitempty"` // agent identity (ties interfaces together)
	Agent      bool        `json:"agent,omitempty"`     // runs NetLogger (auto-discovered)
	Interfaces []Interface `json:"interfaces,omitempty"`
}

type Config struct {
	Network struct {
		Name string `json:"name"`
	} `json:"network"`
	Devices []Device `json:"devices"`
}

// Device returns the device with the given id.
func (c Config) Device(id string) (Device, bool) {
	for _, d := range c.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

func Marshal(c Config) ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// Parse decodes a config; empty/blank input returns Default().
func Parse(data []byte) (Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Default(), nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	if len(c.Devices) == 0 {
		return Default(), nil
	}
	return c, nil
}

// Load reads the config at path; a missing file returns Default() with no error.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Save writes c as indented JSON to path.
func Save(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Default is the prepopulated home-LAN config used for this project.
func Default() Config {
	c := Config{}
	c.Network.Name = "Home LAN"
	c.Devices = []Device{
		{ID: "internet", Name: "Internet", Type: TypeInternet, Target: "8.8.8.8",
			Interfaces: []Interface{{ID: "net", Medium: MediumWired, IP: "8.8.8.8", Monitor: true}}},
		{ID: "modem", Name: "Modem", Type: TypeModem, Model: "Calix C5500XK",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Via: "internet"}}},
		{ID: "router", Name: "Router", Type: TypeRouter, Model: "TP-Link BE9300",
			Interfaces: []Interface{{ID: "lan", Medium: MediumWired, IP: "192.168.0.1", Via: "modem", Monitor: true}}},
		{ID: "sw1", Name: "Switch 1", Type: TypeSwitch, Model: "Tenda TEM2010F", Role: "core",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Speed: "2.5G", Via: "router"}}},
		{ID: "sw2", Name: "Switch 2", Type: TypeSwitch, Model: "Tenda", Role: "access",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Speed: "2.5G", Via: "sw1"}}},
		{ID: "nas", Name: "NAS", Type: TypeNAS, Model: "QNAP TS-563",
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Via: "sw1"}}},
		{ID: "projectorpc", Name: "ProjectorPC", Type: TypePC, Agent: true,
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Adapter: "Intel I219-V", MAC: "00:1B:21:00:00:01", Speed: "1G", Via: "sw1", Link: "UGREEN coupler", Monitor: true}}},
		{ID: "ryzen", Name: "ryzen", Type: TypePC, Agent: true,
			Interfaces: []Interface{
				{ID: "eth", Medium: MediumWired, Adapter: "Killer E3100G", MAC: "2C:F0:5D:11:22:33", IP: "192.168.0.154", Speed: "2.5G", Via: "sw2", Monitor: true},
				{ID: "wifi", Medium: MediumWifi, Adapter: "Intel AX1675x", MAC: "2C:F0:5D:44:55:66", IP: "192.168.0.24", Speed: "1.4G", Via: "router", SSID: "HomeNet", Band: "5GHz"},
			}},
		{ID: "ncase", Name: "NCASE", Type: TypePC, Agent: true,
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Adapter: "Intel I226-V", MAC: "00:1B:21:00:00:02", Speed: "2.5G", Via: "sw2"}}},
	}
	return c
}
