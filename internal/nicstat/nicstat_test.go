package nicstat

import "testing"

func TestParseNICsArray(t *testing.T) {
	data := []byte(`[
		{"Name":"Ethernet","Description":"Killer E3100G","LinkSpeed":"2.5 Gbps","Status":"Up","RxErrors":0,"RxDiscards":47,"TxErrors":0,"TxDiscards":0,"RxBytes":1000,"TxBytes":2000,"Power":"Energy-Efficient Ethernet=Disabled; Green Ethernet=Enabled"},
		{"Name":"Wi-Fi","Description":"AX1675x","LinkSpeed":"866 Mbps","Status":"Up","RxErrors":0,"RxDiscards":0,"TxErrors":0,"TxDiscards":0,"RxBytes":3,"TxBytes":4,"Power":""}
	]`)
	nics, err := parseNICs(data)
	if err != nil {
		t.Fatalf("parseNICs: %v", err)
	}
	if len(nics) != 2 {
		t.Fatalf("expected 2 nics, got %d", len(nics))
	}
	if nics[0].Name != "Ethernet" || nics[0].RxDiscards != 47 || nics[0].Power != "Energy-Efficient Ethernet=Disabled; Green Ethernet=Enabled" {
		t.Fatalf("nic0 wrong: %+v", nics[0])
	}
	if nics[1].Power != "" {
		t.Fatalf("nic1 Power should be empty: %+v", nics[1])
	}
}

func TestParseNICsSingleObject(t *testing.T) {
	// PowerShell ConvertTo-Json emits a bare object for a single adapter.
	data := []byte(`{"Name":"Ethernet","Description":"d","LinkSpeed":"2.5 Gbps","Status":"Up","RxDiscards":5,"Power":"Energy-Efficient Ethernet=Disabled"}`)
	nics, err := parseNICs(data)
	if err != nil {
		t.Fatalf("parseNICs single: %v", err)
	}
	if len(nics) != 1 || nics[0].RxDiscards != 5 || nics[0].Power != "Energy-Efficient Ethernet=Disabled" {
		t.Fatalf("single parse wrong: %+v", nics)
	}
}

func TestParseNICsEmpty(t *testing.T) {
	if nics, err := parseNICs([]byte("")); err != nil || nics != nil {
		t.Fatalf("empty should be nil,nil; got %v,%v", nics, err)
	}
}
