// Package httpauth provides a control-plane auth middleware: a shared bearer
// token plus a Host-header allowlist (DNS-rebinding defense). Loopback requests
// and the static GUI + /api/status are exempt so the local dashboard works.
package httpauth

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// Middleware returns a wrapper that enforces the token on /api/* routes (except
// /api/status). If token is "", authentication is disabled (open), but the
// Host-header check still applies. Loopback clients are always allowed so the
// dashboard works on the coordinator host.
func Middleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hostAllowed(r.Host) {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
			if token == "" || !protected(r.URL.Path) || isLoopback(r.RemoteAddr) {
				next.ServeHTTP(w, r)
				return
			}
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// protected reports whether a path requires the token (all /api/* except status).
func protected(path string) bool {
	return strings.HasPrefix(path, "/api/") && path != "/api/status"
}

// hostAllowed rejects Host headers that aren't an IP literal or localhost,
// which blocks DNS-rebinding attacks that would otherwise reach the loopback
// bypass via an attacker-controlled domain name.
func hostAllowed(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if h == "localhost" {
		return true
	}
	return net.ParseIP(h) != nil
}

func isLoopback(remoteAddr string) bool {
	h := remoteAddr
	if hh, _, err := net.SplitHostPort(remoteAddr); err == nil {
		h = hh
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
