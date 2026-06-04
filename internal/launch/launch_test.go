package launch

import "testing"

func TestHostPortNormalizesWildcard(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:8088":      "127.0.0.1:8088",
		":8088":             "127.0.0.1:8088",
		"127.0.0.1:8088":    "127.0.0.1:8088",
		"192.168.0.11:8088": "192.168.0.11:8088",
	}
	for in, want := range cases {
		if got := HostPort(in); got != want {
			t.Fatalf("HostPort(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestBrowserURL(t *testing.T) {
	if got := BrowserURL("0.0.0.0:8088"); got != "http://127.0.0.1:8088" {
		t.Fatalf("BrowserURL wrong: %q", got)
	}
}
