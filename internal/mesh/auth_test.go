package mesh

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthClientAttachesBearer(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := AuthClient("s3cret", 2*time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got != "Bearer s3cret" {
		t.Fatalf("want bearer header, got %q", got)
	}
}

func TestAuthClientEmptyTokenSendsNoHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := AuthClient("", 2*time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got != "" {
		t.Fatalf("want no auth header for empty token, got %q", got)
	}
}
