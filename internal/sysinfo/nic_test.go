package sysinfo

import "testing"

const procNetDevSample = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 500000   4000   3    7    0     0          0         0   400000    3500   1    2    0     0       0          0
`

func TestParseProcNetDev(t *testing.T) {
	nics := parseProcNetDev(procNetDevSample)
	var eth0 NIC
	for _, n := range nics {
		if n.Name == "eth0" {
			eth0 = n
		}
	}
	if eth0.Name != "eth0" {
		t.Fatalf("eth0 not parsed: %+v", nics)
	}
	if eth0.RxErrors != 3 || eth0.RxDropped != 7 || eth0.TxErrors != 1 || eth0.TxDropped != 2 {
		t.Fatalf("eth0 counters wrong: %+v", eth0)
	}
}

// Real kernels pack the rx-bytes column right against the colon when the count
// is large (no space after "eth0:"). The split-on-colon parser must still align.
func TestParseProcNetDevPackedColumns(t *testing.T) {
	const packed = "  eth0:999999999999 4000 5 9 0 0 0 0 400000 3500 2 4 0 0 0 0\n"
	nics := parseProcNetDev(packed)
	if len(nics) != 1 {
		t.Fatalf("want 1 nic, got %d", len(nics))
	}
	n := nics[0]
	if n.Name != "eth0" || n.RxErrors != 5 || n.RxDropped != 9 || n.TxErrors != 2 || n.TxDropped != 4 {
		t.Fatalf("packed columns misaligned: %+v", n)
	}
}

func TestParseProcNetDevSkipsMalformed(t *testing.T) {
	const bad = "garbage line no colon\n  eth0: 1 2 3\n" // second line has a colon but too few fields
	nics := parseProcNetDev(bad)
	if len(nics) != 0 {
		t.Fatalf("malformed/short lines must be skipped, got %+v", nics)
	}
}
