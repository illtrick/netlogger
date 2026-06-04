// Package config loads the user-supplied network topology + inventory file.
// model/nic are labels only — the tool applies no behavior keyed off them.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NodeType is the role a node plays in the topology.
type NodeType string

const (
	NodeModem    NodeType = "modem"
	NodeRouter   NodeType = "router"
	NodeSwitch   NodeType = "switch"
	NodePassive  NodeType = "passive"
	NodeEndpoint NodeType = "endpoint"
	NodeCloud    NodeType = "cloud"
)

// Node is one element of the network (a device, switch, or passive segment).
type Node struct {
	ID        string   `yaml:"id"`
	Type      NodeType `yaml:"type"`
	Label     string   `yaml:"label"`
	Model     string   `yaml:"model,omitempty"`
	NIC       string   `yaml:"nic,omitempty"`
	Address   string   `yaml:"address,omitempty"`
	LinkSpeed string   `yaml:"link_speed,omitempty"`
	Role      string   `yaml:"role,omitempty"`
	ClockRes  string   `yaml:"clock_res,omitempty"`
	Managed   bool     `yaml:"managed,omitempty"`
}

// Config is the whole network config file: nodes plus their links.
type Config struct {
	Nodes []Node     `yaml:"nodes"`
	Links [][]string `yaml:"links"`
}

// Load reads and validates a network config file from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks IDs are unique and every link references known nodes.
func (c *Config) Validate() error {
	ids := make(map[string]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node with empty id")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
	}
	for _, l := range c.Links {
		if len(l) != 2 {
			return fmt.Errorf("link must have exactly 2 endpoints, got %v", l)
		}
		for _, id := range l {
			if !ids[id] {
				return fmt.Errorf("link references unknown node %q", id)
			}
		}
	}
	return nil
}

// Node returns the node with the given id, if present.
func (c *Config) Node(id string) (Node, bool) {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}
