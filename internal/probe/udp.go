package probe

import (
	"encoding/binary"
	"net"
	"time"
)

// UDPStats summarizes one isochronous UDP probe run.
type UDPStats struct {
	Sent     int
	Received int
	LossPct  float64
	AvgRTT   time.Duration
	Jitter   time.Duration // mean abs diff of consecutive RTTs (IPDV)
}

// UDPEcho is a minimal UDP echo server: the probe target.
type UDPEcho struct{ conn *net.UDPConn }

// StartUDPEcho binds addr (e.g. "127.0.0.1:0") and echoes every packet back.
func StartUDPEcho(addr string) (*UDPEcho, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	e := &UDPEcho{conn: conn}
	go e.serve()
	return e, nil
}

// Addr returns the bound address (host:port).
func (e *UDPEcho) Addr() string { return e.conn.LocalAddr().String() }

// Close stops the echo server.
func (e *UDPEcho) Close() error { return e.conn.Close() }

func (e *UDPEcho) serve() {
	buf := make([]byte, 2048)
	for {
		n, raddr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_, _ = e.conn.WriteToUDP(buf[:n], raddr)
	}
}

// ProbeUDP sends count packets at a fixed interval (isochronous — it does not
// wait for replies between sends) and measures loss and jitter from the echoes.
func ProbeUDP(target string, count int, interval, timeout time.Duration) (UDPStats, error) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return UDPStats{}, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return UDPStats{}, err
	}
	defer conn.Close()

	sendTimes := make([]time.Time, count)
	received := make([]bool, count)
	var rtts []time.Duration
	done := make(chan struct{})

	go func() {
		buf := make([]byte, 64)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
			n, err := conn.Read(buf)
			if err != nil {
				close(done)
				return
			}
			if n >= 4 {
				seq := int(binary.BigEndian.Uint32(buf[:4]))
				if seq >= 0 && seq < count && !received[seq] {
					received[seq] = true
					rtts = append(rtts, time.Since(sendTimes[seq]))
				}
			}
		}
	}()

	pkt := make([]byte, 16)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint32(pkt[:4], uint32(i))
		sendTimes[i] = time.Now()
		_, _ = conn.Write(pkt)
		if i < count-1 {
			<-ticker.C
		}
	}

	time.Sleep(timeout)                  // let stragglers arrive
	_ = conn.SetReadDeadline(time.Now()) // unblock the reader
	<-done                               // reader has stopped; safe to read results

	recv := 0
	for _, r := range received {
		if r {
			recv++
		}
	}
	stats := UDPStats{
		Sent:     count,
		Received: recv,
		LossPct:  float64(count-recv) / float64(count) * 100,
	}
	if len(rtts) > 0 {
		var sum time.Duration
		for _, r := range rtts {
			sum += r
		}
		stats.AvgRTT = sum / time.Duration(len(rtts))
		if len(rtts) > 1 {
			var jsum time.Duration
			for i := 1; i < len(rtts); i++ {
				d := rtts[i] - rtts[i-1]
				if d < 0 {
					d = -d
				}
				jsum += d
			}
			stats.Jitter = jsum / time.Duration(len(rtts)-1)
		}
	}
	return stats, nil
}
