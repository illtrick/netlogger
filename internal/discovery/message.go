package discovery

import "encoding/json"

// magic guards our datagrams from other traffic that may land on the port.
const magic = "nlldisc1"

// announce is the discovery wire record (small JSON), sent to the multicast
// group, to subnet broadcast addresses, and as direct unicast replies.
type announce struct {
	Magic   string `json:"m"`
	ID      string `json:"id"`
	Host    string `json:"host"`
	IP      string `json:"ip,omitempty"` // node's primary outbound IP (multi-homed hosts)
	Port    int    `json:"port"`
	Version string `json:"ver"`
	Bye     bool   `json:"bye,omitempty"`
	// Reply marks a unicast answer to a received announce. Replies are not
	// answered again (loop prevention). Older nodes ignore the field.
	Reply bool `json:"r,omitempty"`
}

func encode(a announce) []byte {
	a.Magic = magic
	b, _ := json.Marshal(a)
	return b
}

func decode(data []byte) (announce, bool) {
	var a announce
	if err := json.Unmarshal(data, &a); err != nil {
		return announce{}, false
	}
	if a.Magic != magic {
		return announce{}, false
	}
	a.Magic = ""
	return a, true
}
