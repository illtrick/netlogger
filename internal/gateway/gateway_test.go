package gateway

import (
	"net"
	"testing"
)

func TestDefaultReturnsIPOrEmpty(t *testing.T) {
	got := Default()
	if got == "" {
		t.Skip("no default gateway in this environment")
	}
	if net.ParseIP(got) == nil {
		t.Fatalf("Default() returned a non-IP: %q", got)
	}
}
