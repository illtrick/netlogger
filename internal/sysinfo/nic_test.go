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
