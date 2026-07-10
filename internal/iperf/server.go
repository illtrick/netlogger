package iperf

import (
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// DefaultServerPort is iperf3's default listen port.
const DefaultServerPort = 5201

// Server is a managed, always-on iperf3 server (iperf3 -s) so any agent can be a
// load-test target with no manual setup. It restarts the process if it exits and
// stops cleanly on Stop.
type Server struct {
	port    int
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

// StartServer launches iperf3 -s on port (0 = default 5201) and keeps it running
// until Stop. Returns nil if no iperf3 binary is available — load tests are then
// simply unavailable, which the readiness check already surfaces.
func StartServer(port int) *Server {
	bin := binary()
	if bin == "" {
		return nil
	}
	if port == 0 {
		port = DefaultServerPort
	}
	s := &Server{port: port, stop: make(chan struct{}), done: make(chan struct{})}
	// Best-effort and health-verified (spawns PowerShell probes) — run async
	// so server start isn't delayed. iperf3 binds regardless; the firewall
	// only gates inbound, and existing rules are usually still healthy. The
	// program rule covers the extracted binary on every port; the per-port
	// rule is belt-and-braces for layouts where the binary path shifts.
	go func() {
		ensureFirewallProgram(bin)
		ensureFirewallPort(port)
	}()
	go s.run()
	return s
}

func (s *Server) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		cmd := exec.Command(binary(), "-s", "-p", strconv.Itoa(s.port))
		hideConsole(cmd)
		s.mu.Lock()
		s.cmd = cmd
		s.mu.Unlock()
		_ = cmd.Run() // blocks until the process exits or is killed
		// Brief backoff before restart, unless we're stopping.
		select {
		case <-s.stop:
			return
		case <-time.After(time.Second):
		}
	}
}

// Stop terminates the server and waits for its loop to exit. Safe to call once.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.stop)
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.mu.Unlock()
	<-s.done
}

// Port returns the server's listen port (0 if the server is nil).
func (s *Server) Port() int {
	if s == nil {
		return 0
	}
	return s.port
}
