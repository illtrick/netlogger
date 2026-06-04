// Package launch holds the "feels like an app" helpers: opening the dashboard
// in the user's browser and normalizing the listen address for display.
package launch

import (
	"net"
	"os/exec"
	"runtime"
)

// HostPort normalizes a listen address for reaching the server from this
// machine: a wildcard bind (0.0.0.0 / :: / empty host) becomes loopback.
func HostPort(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// BrowserURL is the http URL to open for a given listen address.
func BrowserURL(listen string) string {
	return "http://" + HostPort(listen)
}

// OpenBrowser opens url in the default browser (best-effort, non-blocking).
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
