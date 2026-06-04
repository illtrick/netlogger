package httpauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func do(t *testing.T, token, path, host, remote, auth string) int {
	t.Helper()
	h := Middleware(token)(http.HandlerFunc(ok))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	req.RemoteAddr = remote
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestTokenRequiredFromRemote(t *testing.T) {
	if c := do(t, "tok", "/api/samples", "192.168.0.5:8088", "192.168.0.9:5000", ""); c != http.StatusUnauthorized {
		t.Fatalf("remote without token should be 401, got %d", c)
	}
	if c := do(t, "tok", "/api/samples", "192.168.0.5:8088", "192.168.0.9:5000", "Bearer tok"); c != http.StatusOK {
		t.Fatalf("remote with correct token should be 200, got %d", c)
	}
	if c := do(t, "tok", "/api/samples", "192.168.0.5:8088", "192.168.0.9:5000", "Bearer wrong"); c != http.StatusUnauthorized {
		t.Fatalf("wrong token should be 401, got %d", c)
	}
}

func TestLoopbackBypassesToken(t *testing.T) {
	if c := do(t, "tok", "/api/samples", "127.0.0.1:8088", "127.0.0.1:5000", ""); c != http.StatusOK {
		t.Fatalf("loopback should bypass token, got %d", c)
	}
}

func TestStatusAndGUIExempt(t *testing.T) {
	if c := do(t, "tok", "/api/status", "192.168.0.5:8088", "192.168.0.9:5000", ""); c != http.StatusOK {
		t.Fatalf("/api/status should be exempt, got %d", c)
	}
	if c := do(t, "tok", "/", "192.168.0.5:8088", "192.168.0.9:5000", ""); c != http.StatusOK {
		t.Fatalf("GUI root should be exempt, got %d", c)
	}
}

func TestHostAllowlistBlocksRebinding(t *testing.T) {
	if c := do(t, "tok", "/api/samples", "evil.example.com:8088", "127.0.0.1:5000", ""); c != http.StatusForbidden {
		t.Fatalf("non-IP host should be 403 (rebinding defense), got %d", c)
	}
}

func TestEmptyTokenAllows(t *testing.T) {
	if c := do(t, "", "/api/samples", "192.168.0.5:8088", "192.168.0.9:5000", ""); c != http.StatusOK {
		t.Fatalf("empty token disables auth, got %d", c)
	}
}
