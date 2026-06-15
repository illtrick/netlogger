package discovery

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	a := announce{ID: "abc", Host: "ryzen", Port: 8088, Version: "1.0", Bye: false}
	data := encode(a)
	got, ok := decode(data)
	if !ok {
		t.Fatalf("decode failed on valid payload")
	}
	if got != a {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, a)
	}
}

func TestDecodeRejectsForeignPayload(t *testing.T) {
	if _, ok := decode([]byte(`{"hello":"world"}`)); ok {
		t.Fatalf("expected decode to reject payload without our magic")
	}
	if _, ok := decode([]byte("garbage")); ok {
		t.Fatalf("expected decode to reject non-JSON")
	}
}
