package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

// Config configures a discovery Service.
type Config struct {
	SelfID      string
	Host        string
	ControlPort int
	Version     string
	Group       string        // multicast group, e.g. "239.255.74.76"
	Port        int           // discovery UDP port
	Interval    time.Duration // announce heartbeat (default 3s)
	TTL         time.Duration // peer expiry (default 12s)
}

// Service announces this instance and tracks discovered peers.
type Service struct {
	cfg      Config
	group    net.IP
	tbl      *table
	conn     *net.UDPConn
	pc       *ipv4.PacketConn
	stop     chan struct{}
	wg       sync.WaitGroup
	sendMu   sync.Mutex // serializes SetMulticastInterface+WriteTo across goroutines
	closed   sync.Once
	selfMsg  []byte // our announce, prebuilt in Start
	replyMsg []byte // same announce with the reply flag set
	replyMu  sync.Mutex
	replied  map[string]time.Time // peer ID → last unicast reply, rate limit
}

// New creates a Service. Call Start to begin.
func New(cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 12 * time.Second
	}
	s := &Service{
		cfg:     cfg,
		group:   net.ParseIP(cfg.Group),
		tbl:     newTable(cfg.TTL, time.Now),
		stop:    make(chan struct{}),
		replied: make(map[string]time.Time),
	}
	self := announce{
		ID: cfg.SelfID, Host: cfg.Host, IP: primaryIP(),
		Port: cfg.ControlPort, Version: cfg.Version,
	}
	s.selfMsg = encode(self)
	self.Reply = true
	s.replyMsg = encode(self)
	return s
}

// Start binds the socket, joins the group on all real interfaces, and launches
// the announce + listen loops.
func (s *Service) Start() error {
	lc := net.ListenConfig{Control: reuseControl}
	pktConn, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:"+strconv.Itoa(s.cfg.Port))
	if err != nil {
		return fmt.Errorf("bind discovery socket: %w", err)
	}
	udp, ok := pktConn.(*net.UDPConn)
	if !ok {
		pktConn.Close()
		return fmt.Errorf("discovery socket is not *net.UDPConn")
	}
	s.conn = udp
	s.pc = ipv4.NewPacketConn(udp)
	_ = s.pc.SetMulticastLoopback(true)
	_ = s.pc.SetMulticastTTL(1)

	gaddr := &net.UDPAddr{IP: s.group, Port: s.cfg.Port}
	joined := 0
	for _, ifi := range multicastInterfaces() {
		if err := s.pc.JoinGroup(&ifi, gaddr); err == nil {
			joined++
		}
	}
	if joined == 0 {
		s.conn.Close()
		return fmt.Errorf("could not join multicast group on any interface")
	}

	s.wg.Add(2)
	go s.announceLoop(gaddr)
	go s.listenLoop()
	return nil
}

func (s *Service) announceLoop(gaddr *net.UDPAddr) {
	defer s.wg.Done()
	for i := 0; i < 3; i++ {
		s.sendTo(gaddr, s.selfMsg)
		select {
		case <-s.stop:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sendTo(gaddr, s.selfMsg)
		}
	}
}

func (s *Service) sendTo(gaddr *net.UDPAddr, msg []byte) {
	// SetMulticastInterface + WriteTo is a non-atomic pair on shared socket
	// state; serialize it since both announceLoop and Stop call sendTo.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	for _, ifi := range multicastInterfaces() {
		ifi := ifi
		_ = s.pc.SetMulticastInterface(&ifi)
		_, _ = s.pc.WriteTo(msg, nil, gaddr)
	}
	// Also announce to each interface's subnet broadcast: Wi-Fi access
	// points commonly filter multicast toward wireless clients (IGMP
	// snooping) while broadcast passes, so this reaches nodes the group
	// send cannot. Listeners bind 0.0.0.0 and accept either.
	for _, ip := range broadcastIPs() {
		_, _ = s.conn.WriteToUDP(msg, &net.UDPAddr{IP: ip, Port: s.cfg.Port})
	}
}

// shouldReply rate-limits unicast replies to one per peer per interval.
func (s *Service) shouldReply(peerID string) bool {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	now := time.Now()
	if last, seen := s.replied[peerID]; seen && now.Sub(last) < s.cfg.Interval {
		return false
	}
	s.replied[peerID] = now
	return true
}

func (s *Service) listenLoop() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, src, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if reply := s.handleDatagram(buf[:n], src.IP.String()); reply != nil {
			// Unicast straight back to the sender's discovery port. This is
			// the path that survives one-way multicast (Wi-Fi APs commonly
			// filter group traffic toward wireless clients): if their sends
			// reach us but ours can't reach them, the reply still completes
			// discovery in both directions.
			_, _ = s.conn.WriteToUDP(reply, &net.UDPAddr{IP: src.IP, Port: s.cfg.Port})
		}
	}
}

// handleDatagram processes one received datagram and returns the unicast
// reply to send back (nil for none). Split from listenLoop so the reply
// protocol is testable without racing real socket delivery.
func (s *Service) handleDatagram(data []byte, srcIP string) []byte {
	a, ok := decode(data)
	if !ok {
		return nil
	}
	if isSelfAnnounce(a, srcIP, s.cfg.SelfID, s.cfg.Host, localIPSet()) {
		return nil
	}
	if a.Bye {
		s.tbl.remove(a.ID)
		return nil
	}
	// Prefer the node's self-reported primary outbound IP (consistent on
	// multi-homed hosts) over the multicast source IP, which varies by the
	// interface the announce egressed from.
	ip := a.IP
	if ip == "" {
		ip = srcIP
	}
	s.tbl.upsert(Peer{
		ID:      a.ID,
		Host:    a.Host,
		Addr:    net.JoinHostPort(ip, strconv.Itoa(a.Port)),
		Version: a.Version,
	})
	// Answer heard announces; never answer replies (loop prevention), so a
	// pair exchanges at most one extra unicast per interval each.
	if !a.Reply && s.shouldReply(a.ID) {
		return s.replyMsg
	}
	return nil
}

// Peers returns the currently-live discovered peers (excludes self and expired).
func (s *Service) Peers() []Peer { return s.tbl.list() }

// AddPeer registers a peer learned out-of-band — e.g. from the source IP of
// inbound control-plane traffic, which keeps working when this node cannot
// receive the peer's announces at all (multicast-filtering APs, old peers
// without unicast replies). Normal TTL expiry applies, so the caller should
// re-add while the evidence keeps arriving.
func (s *Service) AddPeer(p Peer) { s.tbl.upsert(p) }

// isSelfAnnounce reports whether an announce actually originated from this node:
// either the same node id, or a *different* identity that shares both our
// hostname and one of our local IPs — i.e. this same machine running a second
// instance (a stale process during a redeploy, or a second data dir), which must
// not be treated as a peer. Two test services on one host use distinct hostnames,
// so they still discover each other.
func isSelfAnnounce(a announce, srcIP, selfID, selfHost string, selfIPs map[string]bool) bool {
	if a.ID == selfID {
		return true
	}
	if a.Host == "" || a.Host != selfHost {
		return false
	}
	ip := a.IP
	if ip == "" {
		ip = srcIP
	}
	return selfIPs[ip] || selfIPs[srcIP]
}

// localIPSet returns this machine's unicast interface IPs, used to recognize a
// second local identity in isSelfAnnounce.
func localIPSet() map[string]bool {
	set := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return set
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP != nil {
			set[ipnet.IP.String()] = true
		}
	}
	return set
}

// Stop announces a bye, stops the loops, and closes the socket.
func (s *Service) Stop() error {
	s.closed.Do(func() {
		if s.pc != nil {
			bye := encode(announce{ID: s.cfg.SelfID, Bye: true})
			s.sendTo(&net.UDPAddr{IP: s.group, Port: s.cfg.Port}, bye)
		}
		close(s.stop)
		if s.conn != nil {
			s.conn.Close()
		}
		s.wg.Wait()
	})
	return nil
}

// PrimaryIP returns the local IP the OS would use for outbound traffic — the
// same address this node advertises to peers in its announces. Exported so the
// engine can hand out a LAN-reachable self address (a remote node told to test
// against "127.0.0.1" would test itself). Returns "" if it can't be determined.
func PrimaryIP() string { return primaryIP() }

// primaryIP returns the local IP the OS would use for outbound traffic (the
// default-route source). On a multi-homed host this gives one stable, routable
// address instead of letting the receiver guess from a per-interface multicast
// source. Returns "" if it can't be determined.
func primaryIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80") // no packet sent; just a route lookup
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP != nil {
		return a.IP.String()
	}
	return ""
}

// multicastInterfaces returns up, multicast-capable interfaces.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// broadcastIPs returns the IPv4 subnet broadcast address of every up,
// broadcast-capable, non-loopback interface.
func broadcastIPs() []net.IP {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagBroadcast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		out = append(out, broadcastFromAddrs(addrs)...)
	}
	return out
}

// broadcastFromAddrs computes ip|^mask for each IPv4 network. Pure for tests.
func broadcastFromAddrs(addrs []net.Addr) []net.IP {
	var out []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		mask := ipnet.Mask
		if len(mask) == 16 {
			mask = mask[12:]
		}
		if len(mask) != 4 {
			continue
		}
		b := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			b[i] = ip4[i] | ^mask[i]
		}
		out = append(out, b)
	}
	return out
}
