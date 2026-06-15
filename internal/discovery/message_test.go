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

func TestEncodeDecodeByeFlag(t *testing.T) {
	got, ok := decode(encode(announce{ID: "x", Bye: true}))
	if !ok || !got.Bye || got.ID != "x" {
		t.Fatalf("bye roundtrip failed: %+v ok=%v", got, ok)
	}
}

func TestDecodeEmptyInput(t *testing.T) {
	if _, ok := decode(nil); ok {
		t.Fatalf("expected nil to be rejected")
	}
	if _, ok := decode([]byte("")); ok {
		t.Fatalf("expected empty to be rejected")
	}
}
