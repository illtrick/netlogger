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
	cfg    Config
	group  net.IP
	tbl    *table
	conn   *net.UDPConn
	pc     *ipv4.PacketConn
	stop   chan struct{}
	wg     sync.WaitGroup
	closed sync.Once
}

// New creates a Service. Call Start to begin.
func New(cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 12 * time.Second
	}
	return &Service{
		cfg:   cfg,
		group: net.ParseIP(cfg.Group),
		tbl:   newTable(cfg.TTL, time.Now),
		stop:  make(chan struct{}),
	}
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
	msg := encode(announce{
		ID: s.cfg.SelfID, Host: s.cfg.Host, Port: s.cfg.ControlPort, Version: s.cfg.Version,
	})
	for i := 0; i < 3; i++ {
		s.sendTo(gaddr, msg)
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
			s.sendTo(gaddr, msg)
		}
	}
}

func (s *Service) sendTo(gaddr *net.UDPAddr, msg []byte) {
	for _, ifi := range multicastInterfaces() {
		ifi := ifi
		_ = s.pc.SetMulticastInterface(&ifi)
		_, _ = s.pc.WriteTo(msg, nil, gaddr)
	}
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
		a, ok := decode(buf[:n])
		if !ok || a.ID == s.cfg.SelfID {
			continue
		}
		if a.Bye {
			s.tbl.remove(a.ID)
			continue
		}
		ip := src.IP.String()
		s.tbl.upsert(Peer{
			ID:      a.ID,
			Host:    a.Host,
			Addr:    net.JoinHostPort(ip, strconv.Itoa(a.Port)),
			Version: a.Version,
		})
	}
}

// Peers returns the currently-live discovered peers (excludes self and expired).
func (s *Service) Peers() []Peer { return s.tbl.list() }

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
