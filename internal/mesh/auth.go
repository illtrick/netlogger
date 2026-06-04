package mesh

import (
	"net/http"
	"time"
)

// authTransport adds a bearer token to every outbound request.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (a authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+a.token)
	}
	return a.base.RoundTrip(r)
}

// AuthClient returns an HTTP client that attaches the bearer token (if non-empty)
// to every request, used for coordinator→agent control-plane calls.
func AuthClient(token string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: authTransport{token: token, base: http.DefaultTransport},
	}
}
