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

// Real associated Wi-Fi: bare "media: autoselect", no parenthesized rate.
const ifconfigWifiFixture = `en1: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	media: autoselect
	status: active
`

// Unknown status token: the vocabulary must stay closed (Up/Disconnected/
// Unknown) or the change detector fires spurious link-flap events.
const ifconfigOddStatusFixture = `en2: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	media: <unknown type>
	status: attaching
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

	speed, status = parseIfconfig(ifconfigWifiFixture)
	if status != "Up" {
		t.Errorf("wifi status = %q, want Up", status)
	}
	if speed != "" {
		t.Errorf("wifi speed = %q, want empty (bare autoselect names no rate)", speed)
	}

	speed, status = parseIfconfig(ifconfigOddStatusFixture)
	if status != "Unknown" {
		t.Errorf("odd status = %q, want Unknown (closed vocabulary)", status)
	}
	if speed != "" {
		t.Errorf("odd speed = %q, want empty", speed)
	}
}

func TestMediaToSpeed(t *testing.T) {
	cases := []struct{ media, want string }{
		{"10Gbase-T <full-duplex>", "10 Gbps"},
		{"5000Base-T <full-duplex>", "5 Gbps"},
		{"2500Base-T <full-duplex>", "2.5 Gbps"},
		{"1000baseT <full-duplex>", "1 Gbps"},
		{"100baseTX <full-duplex>", "100 Mbps"},
		{"autoselect", ""},               // bare autoselect names no rate (Wi-Fi)
		{"autoselect <full-duplex>", ""}, // idle Thunderbolt/USB ports
		{"<unknown type>", ""},           // driver reports no usable media type
		{"none", ""},
		{"10GbaseCR <full-duplex>", "10 Gbps"},         // prefix table still wins
		{"exotic-token <half-duplex>", "exotic-token"}, // unknown wired token passes through, duplex stripped
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
	// Drop is BSD's output-queue drop counter → TxDiscards, never RxDiscards.
	if en0.RxErrors != 2 || en0.TxErrors != 1 || en0.TxDiscards != 5 {
		t.Errorf("en0 counters = %+v", en0)
	}
	if en0.RxBytes != 9876543210 || en0.TxBytes != 8765432109 {
		t.Errorf("en0 bytes = %+v", en0)
	}
	if _, ok := got["lo0"]; !ok {
		t.Error("lo0 should parse (filtering happens later, by hardware-ports map)")
	}
}

const ifconfigAllFixture = `lo0: flags=8049<UP,LOOPBACK,RUNNING,MULTICAST> mtu 16384
	inet 127.0.0.1 netmask 0xff000000
en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	ether f0:18:98:aa:bb:cc
	media: autoselect (1000baseT <full-duplex>)
	status: active
en1: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	media: autoselect
	status: inactive
`

func TestSplitIfconfigSections(t *testing.T) {
	got := splitIfconfigSections(ifconfigAllFixture)
	if len(got) != 3 {
		t.Fatalf("sections = %d, want 3 (%#v)", len(got), got)
	}
	for _, dev := range []string{"lo0", "en0", "en1"} {
		if _, ok := got[dev]; !ok {
			t.Fatalf("missing section %q", dev)
		}
	}
	// Each section must parse identically to a per-device ifconfig call.
	speed, status := parseIfconfig(got["en0"])
	if speed != "1 Gbps" || status != "Up" {
		t.Errorf("en0 section parsed to (%q, %q), want (1 Gbps, Up)", speed, status)
	}
	if speed, status = parseIfconfig(got["en1"]); speed != "" || status != "Disconnected" {
		t.Errorf("en1 section parsed to (%q, %q), want (, Disconnected)", speed, status)
	}
	// Sections must not bleed into each other: en0's media line stays out of lo0.
	if s := got["lo0"]; s == "" || len(s) > 120 {
		t.Errorf("lo0 section looks wrong (len %d): %q", len(s), s)
	}
}
