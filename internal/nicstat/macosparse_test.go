package nicstat

import "testing"

const hwPortsFixture = `Hardware Port: Ethernet
Device: en0
Ethernet Address: f0:18:98:aa:bb:cc

Hardware Port: Wi-Fi
Device: en1
Ethernet Address: f0:18:98:dd:ee:ff

Hardware Port: Thunderbolt Bridge
Device: bridge0
Ethernet Address: 36:5d:22:11:22:33

VLAN Configurations
===================`

func TestParseHardwarePorts(t *testing.T) {
	got := parseHardwarePorts(hwPortsFixture)
	if len(got) != 3 {
		t.Fatalf("ports = %d, want 3", len(got))
	}
	if got["en0"] != "Ethernet" || got["en1"] != "Wi-Fi" || got["bridge0"] != "Thunderbolt Bridge" {
		t.Errorf("map wrong: %#v", got)
	}
}

const ifconfigFixture = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	options=6463<RXCSUM,TXCSUM,TSO4,TSO6,CHANNEL_IO,PARTIAL_CSUM,ZEROINVERT_CSUM>
	ether f0:18:98:aa:bb:cc
	inet 192.168.0.42 netmask 0xffffff00 broadcast 192.168.0.255
	media: autoselect (1000baseT <full-duplex>)
	status: active
`

const ifconfigDownFixture = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	media: autoselect (none)
	status: inactive
`

func TestParseIfconfig(t *testing.T) {
	speed, status := parseIfconfig(ifconfigFixture)
	if speed != "1 Gbps" {
		t.Errorf("speed = %q, want 1 Gbps", speed)
	}
	if status != "Up" {
		t.Errorf("status = %q, want Up", status)
	}

	speed, status = parseIfconfig(ifconfigDownFixture)
	if status != "Disconnected" {
		t.Errorf("down status = %q, want Disconnected", status)
	}
	if speed != "" {
		t.Errorf("down speed = %q, want empty", speed)
	}
}

func TestMediaToSpeed(t *testing.T) {
	cases := []struct{ media, want string }{
		{"10Gbase-T <full-duplex>", "10 Gbps"},
		{"5000Base-T <full-duplex>", "5 Gbps"},
		{"2500Base-T <full-duplex>", "2.5 Gbps"},
		{"1000baseT <full-duplex>", "1 Gbps"},
		{"100baseTX <full-duplex>", "100 Mbps"},
		{"autoselect", "autoselect"}, // unknown → raw passthrough
		{"none", ""},
	}
	for _, c := range cases {
		if got := mediaToSpeed(c.media); got != c.want {
			t.Errorf("mediaToSpeed(%q) = %q, want %q", c.media, got, c.want)
		}
	}
}

const netstatFixture = `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll Drop
lo0        16384 <Link#1>                          41684     0    9152351    41684     0    9152351     0   0
lo0        16384 127           127.0.0.1           41684     -    9152351    41684     -    9152351     -   -
en0        1500  <Link#12>   f0:18:98:aa:bb:cc   9876543     2 9876543210  8765432     1 8765432109     0   5
en0        1500  192.168.0     192.168.0.42      9876543     - 9876543210  8765432     - 8765432109     -   -
en1        1500  <Link#13>   f0:18:98:dd:ee:ff    123456     0  123456789   654321     0  987654321     0   0
`

func TestParseNetstatIB(t *testing.T) {
	got := parseNetstatIB(netstatFixture)
	en0, ok := got["en0"]
	if !ok {
		t.Fatalf("en0 missing: %#v", got)
	}
	if en0.RxErrors != 2 || en0.TxErrors != 1 || en0.RxDiscards != 5 {
		t.Errorf("en0 counters = %+v", en0)
	}
	if en0.RxBytes != 9876543210 || en0.TxBytes != 8765432109 {
		t.Errorf("en0 bytes = %+v", en0)
	}
	if _, ok := got["lo0"]; !ok {
		t.Error("lo0 should parse (filtering happens later, by hardware-ports map)")
	}
}
