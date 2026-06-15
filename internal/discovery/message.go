package discovery

import "encoding/json"

// magic guards our datagrams from other traffic that may land on the port.
const magic = "nlldisc1"

// announce is the multicast wire record (small JSON).
type announce struct {
	Magic   string `json:"m"`
	ID      string `json:"id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Version string `json:"ver"`
	Bye     bool   `json:"bye,omitempty"`
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
